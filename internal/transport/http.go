package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Network bounds do not limit model uploads or the lifetime of streamed output.
const (
	DialTimeout            = 10 * time.Second
	TLSHandshakeTimeout    = 10 * time.Second
	ResponseHeaderTimeout  = 10 * time.Minute
	ReadHeaderTimeout      = 10 * time.Second
	IdleTimeout            = 60 * time.Second
	MaxHeaderBytes         = 64 << 10
	MaxResponseHeaderBytes = 1 << 20
	MaxConnections         = 256
	MaxClientConnections   = 32
	MaxJSONResponseBytes   = 32 << 20
)

// NewHTTPClient owns an isolated transport: no environment proxies, no redirects,
// no TLS downgrade and no implicit DNS use for plaintext. Callers should close
// idle connections when finished and use request contexts for overall deadlines.
func NewHTTPClient(endpoint *url.URL, files Files) (*http.Client, error) {
	config, err := ClientTLSConfig(endpoint, files)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: DialTimeout, KeepAlive: 30 * time.Second}
	plaintext := config == nil
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if plaintext {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid plaintext dial address")
			}
			if err := requireLoopback(host); err != nil {
				return nil, err
			}
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil, DialContext: dial, TLSClientConfig: config,
			TLSHandshakeTimeout:   TLSHandshakeTimeout,
			ResponseHeaderTimeout: ResponseHeaderTimeout,
			IdleConnTimeout:       IdleTimeout, ExpectContinueTimeout: time.Second,
			MaxResponseHeaderBytes: MaxResponseHeaderBytes,
			MaxIdleConns:           MaxClientConnections, MaxIdleConnsPerHost: MaxClientConnections,
			MaxConnsPerHost: MaxClientConnections,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Rose API redirects are disabled")
		},
	}, nil
}
