package transport

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
)

// WrapListener checks the actual bound address before accepting any traffic,
// bounds active connections and applies TLS when configured. The caller owns ln
// on error and the returned listener on success. Pass an unwrapped TCP listener.
func WrapListener(ln net.Listener, config *tls.Config) (net.Listener, error) {
	if ln == nil {
		return nil, errors.New("server listener is nil")
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return nil, errors.New("server requires a TCP listener")
	}
	if config == nil {
		if !addr.IP.IsLoopback() || addr.Zone != "" {
			return nil, errors.New("plaintext listener must actually be bound to a loopback IP")
		}
	} else if err := validateServerTLS(config); err != nil {
		return nil, err
	}
	limited := &limitedListener{
		Listener: ln, tokens: make(chan struct{}, MaxConnections), done: make(chan struct{}),
	}
	if config != nil {
		return tls.NewListener(limited, config.Clone()), nil
	}
	return limited, nil
}

func validateServerTLS(config *tls.Config) error {
	if config.MinVersion != tls.VersionTLS13 || config.MaxVersion != tls.VersionTLS13 ||
		len(config.CurvePreferences) != 1 || config.CurvePreferences[0] != tls.X25519MLKEM768 ||
		config.ClientAuth != tls.RequireAndVerifyClientCert || config.ClientCAs == nil ||
		len(config.Certificates) == 0 || config.GetConfigForClient != nil {
		return errors.New("server TLS must require hybrid-only TLS 1.3 and verified client certificates")
	}
	return nil
}

type limitedListener struct {
	net.Listener
	tokens chan struct{}
	done   chan struct{}
	once   sync.Once
}

func (ln *limitedListener) Accept() (net.Conn, error) {
	select {
	case <-ln.done:
		return nil, net.ErrClosed
	case ln.tokens <- struct{}{}:
	}
	conn, err := ln.Listener.Accept()
	if err != nil {
		<-ln.tokens
		return nil, err
	}
	return &limitedConn{Conn: conn, release: func() { <-ln.tokens }}, nil
}

func (ln *limitedListener) Close() error {
	var err error
	ln.once.Do(func() {
		close(ln.done)
		err = ln.Listener.Close()
	})
	return err
}

type limitedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (conn *limitedConn) Close() error {
	err := conn.Conn.Close()
	conn.once.Do(conn.release)
	return err
}
