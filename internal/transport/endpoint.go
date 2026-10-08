// Package transport implements Rose's local-only plaintext and hybrid mTLS policy.
// Remote connections use the Go standard library's TLS 1.3 X25519MLKEM768 key
// exchange. X.509 certificate signatures are not post-quantum signatures.
package transport

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// MaxEndpointBytes bounds an endpoint before parsing or including it in errors.
const MaxEndpointBytes = 8 << 10

// ParseEndpoint parses ROSE_HOST without silently repairing invalid settings.
// An omitted scheme uses HTTP and port 11434; explicit schemes use 80 or 443.
// HTTP policy is checked separately so callers can report configuration errors.
func ParseEndpoint(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if len(value) > MaxEndpointBytes || strings.ContainsFunc(value, invalidEndpointRune) {
		return nil, errors.New("ROSE_HOST exceeds 8192 bytes or contains whitespace or control characters")
	}
	if value == "" {
		value = "127.0.0.1:11434"
	}
	scheme, rest, explicit := strings.Cut(value, "://")
	port := "11434"
	if !explicit {
		scheme, rest = "http", value
	} else if scheme == "http" {
		port = "80"
	} else if scheme == "https" {
		port = "443"
	} else {
		return nil, errors.New("ROSE_HOST must use http or https")
	}
	authority, path, _ := strings.Cut(rest, "/")
	if strings.ContainsAny(rest, "@?#\\") || strings.TrimSpace(authority) != authority {
		return nil, errors.New("ROSE_HOST must not contain credentials, queries, fragments or whitespace")
	}
	host := authority
	if ip, err := netip.ParseAddr(strings.Trim(authority, "[]")); err == nil {
		host = ip.String()
	} else if strings.Contains(authority, ":") {
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil {
			return nil, errors.New("ROSE_HOST has an invalid host or port")
		}
	}
	if strings.ContainsAny(authority, "[]") {
		if _, err := netip.ParseAddr(host); err != nil {
			return nil, errors.New("ROSE_HOST brackets require a literal IP address")
		}
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || port == "" || strings.HasPrefix(port, "+") {
		return nil, errors.New("ROSE_HOST port must be between 0 and 65535")
	}
	endpoint := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(int(n)))}
	if path != "" {
		endpoint.Path = "/" + path
	}
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	return endpoint, nil
}

func validateEndpoint(endpoint *url.URL) error {
	if endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("endpoint must use http or https")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return errors.New("endpoint credentials, queries and fragments are not supported")
	}
	if len(endpoint.String()) > MaxEndpointBytes || strings.ContainsFunc(endpoint.Host+endpoint.Path, invalidEndpointRune) {
		return errors.New("endpoint exceeds 8192 bytes or contains whitespace or control characters")
	}
	host, port, err := net.SplitHostPort(endpoint.Host)
	if err != nil {
		return errors.New("endpoint must include a valid host and port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil || strings.HasPrefix(port, "+") {
		return errors.New("endpoint port must be between 0 and 65535")
	}
	if strings.ContainsAny(host, " \t\r\n/@?#\\%") || !validHost(host) {
		return errors.New("endpoint contains an invalid host")
	}
	return nil
}

func invalidEndpointRune(c rune) bool {
	return unicode.IsControl(c) || unicode.IsSpace(c)
}

func validHost(host string) bool {
	if host == "" {
		return true // Wildcard bind is permitted only with TLS.
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return len(host) <= 253
}

func requireLoopback(host string) error {
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" {
		return errors.New("plaintext HTTP requires a literal loopback IP; use explicit HTTPS with mutual TLS for remote access")
	}
	return nil
}
