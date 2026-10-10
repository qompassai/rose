// Package certhelper provisions the local certificate authority and the
// server/client identities that Rose's hybrid-TLS transport consumes
// through the ROSE_TLS_* file settings.
//
// The store is a directory (default ~/.rose/tls) holding one CA, one
// server identity, and any number of named client identities. Private
// keys are single unencrypted PKCS#8 PEM files with mode 0600 in
// owner-only directories; identity certificate files carry the leaf
// first followed by the issuing CA, matching the chain format the
// transport loader validates. The package never overwrites an existing
// file: re-issuing an identity is refused until the operator removes or
// archives the old one, and CA replacement happens only through
// RotateCA, which archives the old CA and writes a combined trust
// bundle so trust-store rotation is the revocation mechanism.
package certhelper

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Bounds for every operator-controlled input. Work in this package is
// a handful of key generations and file writes; nothing is unbounded.
const (
	DefaultCAValidityDays   = 365
	DefaultLeafValidityDays = 90
	MinValidityDays         = 1
	MaxValidityDays         = 3650
	MaxNameBytes            = 64
	MaxHosts                = 8
	MaxHostBytes            = 253
	MaxDirBytes             = 4096

	CAFileName         = "ca.crt"
	CAKeyFileName      = "ca.key"
	ServerCertFileName = "server.crt"
	ServerKeyFileName  = "server.key"
	BundleFileName     = "trust-bundle.crt"
	ClientsDirName     = "clients"
	ArchiveDirName     = "archive"

	clockSkew = 5 * time.Minute
)

// Roles reported by List.
const (
	RoleCA     = "ca"
	RoleServer = "server"
	RoleClient = "client"
)

// Info describes one stored certificate. SHA256 is the hex fingerprint
// of the leaf (or CA) DER encoding.
type Info struct {
	Role      string
	Name      string
	Subject   string
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	SHA256    string
	CertPath  string
	KeyPath   string
}

// Rotation reports the outcome of RotateCA.
type Rotation struct {
	NewCA       Info
	ArchivedDir string
	BundlePath  string
}

// Store is one certificate directory. It is immutable after
// construction and safe to use from a single goroutine; the CLI is the
// only caller and runs one command at a time.
type Store struct {
	dir string
}

// DefaultDir returns ~/.rose/tls under the user's home directory.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".rose", "tls"), nil
}

// NewStore validates dir as the store directory. The directory does not
// need to exist yet; write operations create it with owner-only
// permissions and refuse to use it if it exists with wider access.
func NewStore(dir string) (*Store, error) {
	if dir == "" || len(dir) > MaxDirBytes {
		return nil, errors.New("certificate directory path is empty or too long")
	}
	if strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, errors.New("certificate directory path contains control characters")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve certificate directory: %w", err)
	}
	return &Store{dir: abs}, nil
}

// Dir returns the store directory.
func (s *Store) Dir() string { return s.dir }

// CACertPath returns the CA certificate path (the ROSE_TLS_CLIENT_CA /
// ROSE_TLS_CA trust file when a single CA is in use).
func (s *Store) CACertPath() string { return filepath.Join(s.dir, CAFileName) }

// BundlePath returns the combined trust bundle path written by
// RotateCA (both CAs during a rotation transition).
func (s *Store) BundlePath() string { return filepath.Join(s.dir, BundleFileName) }

// ServerCertPath and ServerKeyPath locate the server identity (the
// ROSE_TLS_CERT / ROSE_TLS_KEY pair on the server host).
func (s *Store) ServerCertPath() string { return filepath.Join(s.dir, ServerCertFileName) }
func (s *Store) ServerKeyPath() string  { return filepath.Join(s.dir, ServerKeyFileName) }

// ClientCertPath and ClientKeyPath locate a named client identity.
func (s *Store) ClientCertPath(name string) string {
	return filepath.Join(s.dir, ClientsDirName, name+".crt")
}
func (s *Store) ClientKeyPath(name string) string {
	return filepath.Join(s.dir, ClientsDirName, name+".key")
}

type caMaterial struct {
	cert    *x509.Certificate
	certPEM []byte
	key     ed25519.PrivateKey
	keyPEM  []byte
}

// InitCA creates the store CA. It refuses to run when a CA already
// exists; rotation is RotateCA's job, so an existing CA is never
// silently replaced. name becomes the CA subject common name.
func (s *Store) InitCA(name string, validityDays int) (Info, error) {
	if err := validateName("CA name", name); err != nil {
		return Info{}, err
	}
	if err := validateValidity(validityDays); err != nil {
		return Info{}, err
	}
	if err := s.ensureDir(s.dir); err != nil {
		return Info{}, err
	}
	if exists(s.CACertPath()) || exists(filepath.Join(s.dir, CAKeyFileName)) {
		return Info{}, errors.New("a CA already exists in this store; use rotate-ca to replace it")
	}
	now := time.Now()
	ca, err := makeCA(name, now.Add(-clockSkew), now.Add(time.Duration(validityDays)*24*time.Hour))
	if err != nil {
		return Info{}, err
	}
	if err := writeExclusive(filepath.Join(s.dir, CAKeyFileName), ca.keyPEM, 0o600); err != nil {
		return Info{}, err
	}
	if err := writeExclusive(s.CACertPath(), ca.certPEM, 0o644); err != nil {
		return Info{}, err
	}
	return infoFor(RoleCA, name, ca.cert, s.CACertPath(), filepath.Join(s.dir, CAKeyFileName)), nil
}

// IssueServer creates the server identity for the given DNS names or IP
// addresses (at least one; the transport loader rejects server
// certificates without a subject alternative name). The certificate
// file carries the leaf followed by the CA.
func (s *Store) IssueServer(hosts []string, validityDays int) (Info, error) {
	dns, ips, err := parseHosts(hosts)
	if err != nil {
		return Info{}, err
	}
	if err := validateValidity(validityDays); err != nil {
		return Info{}, err
	}
	ca, err := s.loadCA()
	if err != nil {
		return Info{}, err
	}
	if exists(s.ServerCertPath()) || exists(s.ServerKeyPath()) {
		return Info{}, errors.New("a server identity already exists; remove server.crt/server.key to re-issue")
	}
	now := time.Now()
	leaf, keyPEM, err := makeLeaf(ca, leafSpec{
		commonName: "rose-server",
		dnsNames:   dns,
		ips:        ips,
		usage:      x509.ExtKeyUsageServerAuth,
		notBefore:  now.Add(-clockSkew),
		notAfter:   now.Add(time.Duration(validityDays) * 24 * time.Hour),
	})
	if err != nil {
		return Info{}, err
	}
	if err := writeExclusive(s.ServerKeyPath(), keyPEM, 0o600); err != nil {
		return Info{}, err
	}
	chain := append(pemEncode("CERTIFICATE", leaf.Raw), ca.certPEM...)
	if err := writeExclusive(s.ServerCertPath(), chain, 0o644); err != nil {
		return Info{}, err
	}
	return infoFor(RoleServer, "server", leaf, s.ServerCertPath(), s.ServerKeyPath()), nil
}

// IssueClient creates a named client identity. The name is the
// operator-facing identity label and the certificate common name.
func (s *Store) IssueClient(name string, validityDays int) (Info, error) {
	if err := validateName("client name", name); err != nil {
		return Info{}, err
	}
	if err := validateValidity(validityDays); err != nil {
		return Info{}, err
	}
	ca, err := s.loadCA()
	if err != nil {
		return Info{}, err
	}
	clientsDir := filepath.Join(s.dir, ClientsDirName)
	if err := s.ensureDir(clientsDir); err != nil {
		return Info{}, err
	}
	certPath, keyPath := s.ClientCertPath(name), s.ClientKeyPath(name)
	if exists(certPath) || exists(keyPath) {
		return Info{}, fmt.Errorf("client %q already exists; remove its files to re-issue", name)
	}
	now := time.Now()
	leaf, keyPEM, err := makeLeaf(ca, leafSpec{
		commonName: name,
		usage:      x509.ExtKeyUsageClientAuth,
		notBefore:  now.Add(-clockSkew),
		notAfter:   now.Add(time.Duration(validityDays) * 24 * time.Hour),
	})
	if err != nil {
		return Info{}, err
	}
	if err := writeExclusive(keyPath, keyPEM, 0o600); err != nil {
		return Info{}, err
	}
	chain := append(pemEncode("CERTIFICATE", leaf.Raw), ca.certPEM...)
	if err := writeExclusive(certPath, chain, 0o644); err != nil {
		return Info{}, err
	}
	return infoFor(RoleClient, name, leaf, certPath, keyPath), nil
}

// List inspects every certificate in the store, sorted by path. A
// missing store is an empty result, not an error; a malformed stored
// certificate is an error, because silently skipping it would hide a
// broken deployment.
func (s *Store) List() ([]Info, error) {
	var paths []string
	if exists(s.CACertPath()) {
		paths = append(paths, s.CACertPath())
	}
	if exists(s.ServerCertPath()) {
		paths = append(paths, s.ServerCertPath())
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, ClientsDirName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read clients directory: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".crt") {
			paths = append(paths, filepath.Join(s.dir, ClientsDirName, e.Name()))
		}
	}
	sort.Strings(paths)
	infos := make([]Info, 0, len(paths))
	for _, p := range paths {
		info, err := inspectCertFile(p)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// RotateCA replaces the CA as the revocation mechanism: the old CA is
// archived under archive/<UTC timestamp>/, a new CA takes its place,
// and trust-bundle.crt is written containing the new CA followed by
// the old one. Operators distribute the bundle as the trust file
// while peers still hold old-CA identities, re-issue identities under
// the new CA, then drop the bundle for the new ca.crt alone — at that
// point every old-CA identity is revoked.
func (s *Store) RotateCA(name string, validityDays int) (Rotation, error) {
	if err := validateName("CA name", name); err != nil {
		return Rotation{}, err
	}
	if err := validateValidity(validityDays); err != nil {
		return Rotation{}, err
	}
	old, err := s.loadCA()
	if err != nil {
		return Rotation{}, err
	}
	now := time.Now()
	fresh, err := makeCA(name, now.Add(-clockSkew), now.Add(time.Duration(validityDays)*24*time.Hour))
	if err != nil {
		return Rotation{}, err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	archiveDir := filepath.Join(s.dir, ArchiveDirName, stamp)
	if err := s.ensureDir(filepath.Join(s.dir, ArchiveDirName)); err != nil {
		return Rotation{}, err
	}
	if err := s.ensureDir(archiveDir); err != nil {
		return Rotation{}, err
	}
	if err := writeExclusive(filepath.Join(archiveDir, CAFileName), old.certPEM, 0o644); err != nil {
		return Rotation{}, err
	}
	if err := writeExclusive(filepath.Join(archiveDir, CAKeyFileName), old.keyPEM, 0o600); err != nil {
		return Rotation{}, err
	}
	if err := writeReplace(filepath.Join(s.dir, CAKeyFileName), fresh.keyPEM, 0o600); err != nil {
		return Rotation{}, err
	}
	if err := writeReplace(s.CACertPath(), fresh.certPEM, 0o644); err != nil {
		return Rotation{}, err
	}
	bundle := append(append([]byte{}, fresh.certPEM...), old.certPEM...)
	if err := writeReplace(s.BundlePath(), bundle, 0o644); err != nil {
		return Rotation{}, err
	}
	return Rotation{
		NewCA:       infoFor(RoleCA, name, fresh.cert, s.CACertPath(), filepath.Join(s.dir, CAKeyFileName)),
		ArchivedDir: archiveDir,
		BundlePath:  s.BundlePath(),
	}, nil
}

func (s *Store) loadCA() (caMaterial, error) {
	certPEM, err := readBounded(s.CACertPath())
	if err != nil {
		return caMaterial{}, fmt.Errorf("load CA certificate (run init-ca first): %w", err)
	}
	certs, err := parseCertChain(certPEM)
	if err != nil {
		return caMaterial{}, fmt.Errorf("load CA certificate: %w", err)
	}
	if len(certs) != 1 || !certs[0].IsCA {
		return caMaterial{}, errors.New("stored ca.crt is not a single CA certificate")
	}
	keyPEM, err := readBounded(filepath.Join(s.dir, CAKeyFileName))
	if err != nil {
		return caMaterial{}, fmt.Errorf("load CA key: %w", err)
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return caMaterial{}, fmt.Errorf("load CA key: %w", err)
	}
	pub, ok := certs[0].PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		return caMaterial{}, errors.New("stored CA certificate and key do not match")
	}
	return caMaterial{cert: certs[0], certPEM: certPEM, key: key, keyPEM: keyPEM}, nil
}

// ensureDir creates path (and any missing parents under the store)
// with owner-only permissions and refuses a pre-existing directory
// that group or others can access.
func (s *Store) ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create directory %q: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("directory %q must not be accessible by group or others (use mode 0700)", path)
	}
	return nil
}

func makeCA(name string, notBefore, notAfter time.Time) (caMaterial, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return caMaterial{}, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return caMaterial{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		return caMaterial{}, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return caMaterial{}, fmt.Errorf("parse new CA certificate: %w", err)
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return caMaterial{}, err
	}
	return caMaterial{cert: cert, certPEM: pemEncode("CERTIFICATE", der), key: key, keyPEM: keyPEM}, nil
}

type leafSpec struct {
	commonName string
	dnsNames   []string
	ips        []net.IP
	usage      x509.ExtKeyUsage
	notBefore  time.Time
	notAfter   time.Time
}

func makeLeaf(ca caMaterial, spec leafSpec) (*x509.Certificate, []byte, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate identity key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: spec.commonName},
		NotBefore:             spec.notBefore,
		NotAfter:              spec.notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{spec.usage},
		BasicConstraintsValid: true,
		DNSNames:              spec.dnsNames,
		IPAddresses:           spec.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("create identity certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse new identity certificate: %w", err)
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return nil, nil, err
	}
	return leaf, keyPEM, nil
}

func marshalKey(key ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return pemEncode("PRIVATE KEY", der), nil
}

func pemEncode(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial.Add(serial, big.NewInt(1)), nil
}

func infoFor(role, name string, cert *x509.Certificate, certPath, keyPath string) Info {
	sum := sha256.Sum256(cert.Raw)
	return Info{
		Role: role, Name: name, Subject: cert.Subject.String(), Issuer: cert.Issuer.String(),
		NotBefore: cert.NotBefore, NotAfter: cert.NotAfter, SHA256: hex.EncodeToString(sum[:]),
		CertPath: certPath, KeyPath: keyPath,
	}
}

func inspectCertFile(path string) (Info, error) {
	data, err := readBounded(path)
	if err != nil {
		return Info{}, err
	}
	certs, err := parseCertChain(data)
	if err != nil {
		return Info{}, fmt.Errorf("inspect %q: %w", path, err)
	}
	leaf := certs[0]
	role, name := RoleClient, leaf.Subject.CommonName
	switch {
	case leaf.IsCA:
		role, name = RoleCA, leaf.Subject.CommonName
	case containsEKU(leaf, x509.ExtKeyUsageServerAuth):
		role, name = RoleServer, "server"
	}
	keyPath := strings.TrimSuffix(path, ".crt") + ".key"
	return infoFor(role, name, leaf, path, keyPath), nil
}

func containsEKU(cert *x509.Certificate, want x509.ExtKeyUsage) bool {
	for _, u := range cert.ExtKeyUsage {
		if u == want {
			return true
		}
	}
	return false
}
