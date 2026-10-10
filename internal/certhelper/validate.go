package certhelper

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
)

// maxPEMBytes bounds each stored credential file, matching the
// transport loader's bound (internal/transport MaxPEMBytes).
const maxPEMBytes = 1 << 20

// validateName checks an operator-supplied identity label: bounded,
// path-safe, and unambiguous as a file name and common name.
func validateName(kind, name string) error {
	if name == "" || len(name) > MaxNameBytes {
		return fmt.Errorf("%s must be 1-%d bytes", kind, MaxNameBytes)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%s %q is not a valid name", kind, name)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return fmt.Errorf("%s %q contains characters outside [A-Za-z0-9._-]", kind, name)
		}
	}
	return nil
}

func validateValidity(days int) error {
	if days < MinValidityDays || days > MaxValidityDays {
		return fmt.Errorf("validity must be %d-%d days", MinValidityDays, MaxValidityDays)
	}
	return nil
}

// parseHosts splits operator hosts into DNS names and IP addresses,
// rejecting anything that is neither a valid IP nor a well-formed DNS
// name. At least one host is required: the transport loader rejects
// server certificates without a subject alternative name.
func parseHosts(hosts []string) (dns []string, ips []net.IP, err error) {
	if len(hosts) == 0 || len(hosts) > MaxHosts {
		return nil, nil, fmt.Errorf("provide 1-%d server hosts", MaxHosts)
	}
	seen := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h == "" || len(h) > MaxHostBytes || seen[h] {
			return nil, nil, fmt.Errorf("invalid or duplicate server host %q", h)
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
			continue
		}
		if !validDNSName(h) {
			return nil, nil, fmt.Errorf("server host %q is neither an IP address nor a valid DNS name", h)
		}
		dns = append(dns, h)
	}
	return dns, ips, nil
}

func validDNSName(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// parseCertChain parses a PEM file that must contain only CERTIFICATE
// blocks and nothing else — the same strictness the transport loader
// applies to identity and trust files.
func parseCertChain(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(bytes.TrimSpace(data)) != 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("certificate file contains unexpected PEM data")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("certificate PEM is malformed")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("certificate is malformed")
		}
		certs = append(certs, cert)
		data = rest
	}
	if len(certs) == 0 {
		return nil, errors.New("certificate file contains no certificates")
	}
	return certs, nil
}

// parsePrivateKey parses a file that must contain exactly one
// unencrypted PKCS#8 PEM key and nothing else, matching the transport
// loader's identity-key rules.
func parsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	trimmed := bytes.TrimSpace(data)
	block, rest := pem.Decode(trimmed)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, errors.New("private key must contain one unencrypted PEM key")
	}
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN "+block.Type+"-----")) {
		return nil, errors.New("private key contains unexpected PEM data")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not a valid PKCS#8 key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not an Ed25519 key")
	}
	return key, nil
}
