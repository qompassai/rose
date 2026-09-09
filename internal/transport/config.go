package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Files names PEM files, never PEM contents. CAFile replaces system server roots;
// ClientCAFile is the server's independent trust store for client certificates.
type Files struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
	CAFile       string
}

// ServerTLSConfig validates configuration and loads all server credentials before
// opening a socket. A nil configuration means literal-loopback plaintext HTTP.
func ServerTLSConfig(endpoint *url.URL, files Files) (*tls.Config, error) {
	if err := validateFiles(endpoint, files); err != nil {
		return nil, err
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, errors.New("server ROSE_HOST must not include a path")
	}
	if endpoint.Scheme == "http" {
		return nil, nil
	}
	if files.ClientCAFile == "" {
		return nil, errors.New("HTTPS server requires ROSE_TLS_CLIENT_CA")
	}
	cert, err := loadIdentity(files, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return nil, err
	}
	roots, err := loadRoots(files.ClientCAFile)
	if err != nil {
		return nil, err
	}
	if files.CAFile != "" {
		if _, err := loadRoots(files.CAFile); err != nil {
			return nil, err
		}
	}
	config := hybridConfig(cert)
	config.ClientAuth = tls.RequireAndVerifyClientCert
	config.ClientCAs = roots
	return config, nil
}

// ClientTLSConfig requires a client identity for HTTPS. With no CAFile, Go's
// platform roots verify the server; normal hostname verification always applies.
func ClientTLSConfig(endpoint *url.URL, files Files) (*tls.Config, error) {
	if err := validateFiles(endpoint, files); err != nil {
		return nil, err
	}
	if endpoint.Scheme == "http" {
		return nil, nil
	}
	if endpoint.Hostname() == "" {
		return nil, errors.New("HTTPS client requires a server hostname")
	}
	cert, err := loadIdentity(files, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return nil, err
	}
	config := hybridConfig(cert)
	config.ServerName = endpoint.Hostname()
	if files.CAFile != "" {
		config.RootCAs, err = loadRoots(files.CAFile)
		if err != nil {
			return nil, err
		}
	}
	if files.ClientCAFile != "" {
		if _, err := loadRoots(files.ClientCAFile); err != nil {
			return nil, err
		}
	}
	return config, nil
}

func validateFiles(endpoint *url.URL, files Files) error {
	if err := validateEndpoint(endpoint); err != nil {
		return err
	}
	for _, path := range []string{files.CertFile, files.KeyFile, files.ClientCAFile, files.CAFile} {
		if len(path) > MaxEndpointBytes || strings.Contains(path, "-----BEGIN") || strings.ContainsFunc(path, unicode.IsControl) {
			return errors.New("TLS settings must contain bounded file paths, not PEM contents or control characters")
		}
	}
	if (files.CertFile == "") != (files.KeyFile == "") {
		return errors.New("ROSE_TLS_CERT and ROSE_TLS_KEY must be configured together")
	}
	if endpoint.Scheme == "http" {
		if files != (Files{}) {
			return errors.New("TLS settings require an explicit https ROSE_HOST")
		}
		return requireLoopback(endpoint.Hostname())
	}
	if files.CertFile == "" {
		return errors.New("HTTPS requires ROSE_TLS_CERT and ROSE_TLS_KEY")
	}
	return nil
}

func hybridConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		CurvePreferences:       []tls.CurveID{tls.X25519MLKEM768},
		Certificates:           []tls.Certificate{cert},
		NextProtos:             []string{"http/1.1"},
		SessionTicketsDisabled: true,
	}
}
