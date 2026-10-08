package transport

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"
)

// MaxPEMBytes bounds each credential file, including certificate bundles.
const MaxPEMBytes = 1 << 20

func readPEMFile(path string, private bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect TLS file %q: %w", path, err)
	}
	if !private && info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect TLS file %q: %w", path, err)
		}
	}
	if !info.Mode().IsRegular() || info.Size() > MaxPEMBytes {
		return nil, fmt.Errorf("TLS file %q must be a regular file of at most %d bytes", path, MaxPEMBytes)
	}
	if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("TLS private key %q must not be accessible by group or others (use mode 0600)", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open TLS file %q: %w", path, err)
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return nil, fmt.Errorf("TLS file %q changed while opening", path)
	}
	if private && runtime.GOOS != "windows" && current.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("TLS private key %q has unsafe permissions", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxPEMBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read TLS file %q: %w", path, err)
	}
	if len(data) > MaxPEMBytes {
		clear(data)
		return nil, fmt.Errorf("TLS file %q exceeds %d bytes", path, MaxPEMBytes)
	}
	return data, nil
}

func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(bytes.TrimSpace(data)) != 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("TLS certificate file contains unexpected PEM data")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("TLS certificate PEM is malformed")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("TLS certificate is malformed")
		}
		now := time.Now()
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, errors.New("TLS certificate is expired or not yet valid")
		}
		certs = append(certs, cert)
		data = rest
	}
	if len(certs) == 0 {
		return nil, errors.New("TLS certificate file contains no certificates")
	}
	return certs, nil
}

func loadRoots(path string) (*x509.CertPool, error) {
	data, err := readPEMFile(path, false)
	if err != nil {
		return nil, err
	}
	certs, err := parseCertificates(data)
	if err != nil {
		return nil, fmt.Errorf("TLS trust file %q: %w", path, err)
	}
	roots := x509.NewCertPool()
	for _, cert := range certs {
		if !cert.BasicConstraintsValid || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, fmt.Errorf("TLS trust file %q contains a certificate that cannot sign certificates", path)
		}
		if len(cert.UnhandledCriticalExtensions) != 0 {
			return nil, fmt.Errorf("TLS trust file %q contains unsupported critical extensions", path)
		}
		roots.AddCert(cert)
	}
	return roots, nil
}

func loadIdentity(files Files, usage x509.ExtKeyUsage) (tls.Certificate, error) {
	certPEM, err := readPEMFile(files.CertFile, false)
	if err != nil {
		return tls.Certificate{}, err
	}
	certs, err := parseCertificates(certPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("TLS identity %q: %w", files.CertFile, err)
	}
	if err := validateChain(certs, usage); err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := readPEMFile(files.KeyFile, true)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyPEM)
	keyPEM = bytes.TrimSpace(keyPEM)
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return tls.Certificate{}, errors.New("TLS private key must contain one unencrypted PEM key")
	}
	defer clear(block.Bytes)
	if !bytes.HasPrefix(keyPEM, []byte("-----BEGIN "+block.Type+"-----")) {
		return tls.Certificate{}, errors.New("TLS private key contains unexpected PEM data")
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, errors.New("TLS certificate and private key are invalid or do not match")
	}
	cert.Leaf = certs[0]
	return cert, nil
}

func validateChain(certs []*x509.Certificate, usage x509.ExtKeyUsage) error {
	leaf := certs[0]
	if leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("TLS identity must be an end-entity certificate usable for digital signatures")
	}
	if usage == x509.ExtKeyUsageServerAuth && len(leaf.DNSNames)+len(leaf.IPAddresses) == 0 {
		return errors.New("TLS server certificate requires a DNS or IP subject alternative name")
	}
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(certs[len(certs)-1])
	for i := 1; i < len(certs); i++ {
		if err := certs[i-1].CheckSignatureFrom(certs[i]); err != nil {
			return errors.New("TLS identity certificate chain is invalid or out of order")
		}
		intermediates.AddCert(certs[i])
	}
	// The peer's roots remain authoritative. This verifies only the supplied
	// portion of our chain (a leaf-only PEM is valid), its constraints and EKU.
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{usage},
	})
	if err != nil {
		return errors.New("TLS identity certificate chain is not usable for the requested role")
	}
	return nil
}
