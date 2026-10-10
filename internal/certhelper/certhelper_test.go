package certhelper

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func provision(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.InitCA("rose-ca", 365); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	if _, err := store.IssueServer([]string{"localhost", "127.0.0.1"}, 90); err != nil {
		t.Fatalf("IssueServer: %v", err)
	}
	if _, err := store.IssueClient("laptop", 90); err != nil {
		t.Fatalf("IssueClient: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func loadPair(t *testing.T, certPath, keyPath string) tls.Certificate {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair(%s): %v", certPath, err)
	}
	return pair
}

func caPool(t *testing.T, store *Store) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(readFile(t, store.CACertPath())) {
		t.Fatal("CA cert did not load into pool")
	}
	return pool
}

// --- validation ---

func TestProvisionAndVerifyChains(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)

	serverCerts, err := parseCertChain(readFile(t, store.ServerCertPath()))
	if err != nil {
		t.Fatalf("parse server chain: %v", err)
	}
	if len(serverCerts) != 2 {
		t.Fatalf("server file holds %d certs, want leaf+CA", len(serverCerts))
	}
	leaf := serverCerts[0]
	if leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("server leaf must be a non-CA digital-signature cert")
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" || len(leaf.IPAddresses) != 1 {
		t.Fatalf("server SANs = %v / %v", leaf.DNSNames, leaf.IPAddresses)
	}
	roots := caPool(t, store)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("server leaf does not verify: %v", err)
	}
	clientCerts, err := parseCertChain(readFile(t, store.ClientCertPath("laptop")))
	if err != nil {
		t.Fatalf("parse client chain: %v", err)
	}
	if _, err := clientCerts[0].Verify(x509.VerifyOptions{Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client leaf does not verify: %v", err)
	}
}

// TestTransportLoaderShape asserts the stored files satisfy the rules
// the hybrid transport loader enforces (cert-only PEM, single PKCS#8
// key block, CA roots able to sign, chain in leaf-first order).
func TestTransportLoaderShape(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)

	caCerts, err := parseCertChain(readFile(t, store.CACertPath()))
	if err != nil {
		t.Fatal(err)
	}
	ca := caCerts[0]
	if !ca.BasicConstraintsValid || !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatal("CA would be rejected as a trust root by the transport loader")
	}
	keyData := readFile(t, store.ServerKeyPath())
	if !strings.HasPrefix(string(keyData), "-----BEGIN PRIVATE KEY-----") {
		t.Fatal("server key is not a single PKCS#8 PEM block")
	}
	if _, err := parsePrivateKey(keyData); err != nil {
		t.Fatalf("server key rejected by loader key rules: %v", err)
	}
	serverCerts, err := parseCertChain(readFile(t, store.ServerCertPath()))
	if err != nil {
		t.Fatal(err)
	}
	if err := serverCerts[0].CheckSignatureFrom(serverCerts[1]); err != nil {
		t.Fatalf("chain order/signature invalid for loader: %v", err)
	}
}

func TestListAndRotate(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)

	infos, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("List returned %d entries, want 3", len(infos))
	}
	roles := map[string]bool{}
	for _, info := range infos {
		roles[info.Role] = true
		if len(info.SHA256) != 64 {
			t.Fatalf("bad fingerprint %q", info.SHA256)
		}
	}
	if !roles[RoleCA] || !roles[RoleServer] || !roles[RoleClient] {
		t.Fatalf("roles = %v", roles)
	}

	oldCA := readFile(t, store.CACertPath())
	rot, err := store.RotateCA("rose-ca-2", 365)
	if err != nil {
		t.Fatalf("RotateCA: %v", err)
	}
	if got := readFile(t, filepath.Join(rot.ArchivedDir, CAFileName)); string(got) != string(oldCA) {
		t.Fatal("archived CA differs from the pre-rotation CA")
	}
	bundle, err := parseCertChain(readFile(t, rot.BundlePath))
	if err != nil || len(bundle) != 2 {
		t.Fatalf("bundle = %d certs, err %v; want new+old CA", len(bundle), err)
	}
	// An old-CA client still verifies against the bundle (transition),
	// but not against the new CA alone (revocation completes on swap).
	oldClient, err := parseCertChain(readFile(t, store.ClientCertPath("laptop")))
	if err != nil {
		t.Fatal(err)
	}
	bundlePool := x509.NewCertPool()
	for _, c := range bundle {
		bundlePool.AddCert(c)
	}
	if _, err := oldClient[0].Verify(x509.VerifyOptions{Roots: bundlePool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("old client should verify against bundle: %v", err)
	}
	if _, err := oldClient[0].Verify(x509.VerifyOptions{Roots: caPool(t, store),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err == nil {
		t.Fatal("old client must NOT verify against the new CA alone")
	}
	if _, err := store.IssueClient("phone", 90); err != nil {
		t.Fatalf("issue under new CA: %v", err)
	}
}

func TestPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on windows")
	}
	store := newTestStore(t)
	provision(t, store)
	check := func(path string, want os.FileMode) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	check(store.Dir(), 0o700)
	check(filepath.Join(store.Dir(), CAKeyFileName), 0o600)
	check(store.ServerKeyPath(), 0o600)
	check(store.ClientKeyPath("laptop"), 0o600)
	check(store.CACertPath(), 0o644)
	check(store.ServerCertPath(), 0o644)
}

// --- adversarial ---

func TestNoOverwrite(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)
	caBefore := readFile(t, store.CACertPath())
	serverBefore := readFile(t, store.ServerCertPath())
	clientBefore := readFile(t, store.ClientCertPath("laptop"))

	if _, err := store.InitCA("rose-ca", 365); err == nil {
		t.Fatal("second InitCA must fail")
	}
	if _, err := store.IssueServer([]string{"localhost"}, 90); err == nil {
		t.Fatal("second IssueServer must fail")
	}
	if _, err := store.IssueClient("laptop", 90); err == nil {
		t.Fatal("duplicate IssueClient must fail")
	}
	if string(readFile(t, store.CACertPath())) != string(caBefore) ||
		string(readFile(t, store.ServerCertPath())) != string(serverBefore) ||
		string(readFile(t, store.ClientCertPath("laptop"))) != string(clientBefore) {
		t.Fatal("a refused operation modified an existing file")
	}
}

func TestInvalidInputs(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.IssueClient("laptop", 90); err == nil {
		t.Fatal("IssueClient before InitCA must fail")
	}
	for _, days := range []int{0, -1, MaxValidityDays + 1} {
		if _, err := store.InitCA("rose-ca", days); err == nil {
			t.Fatalf("InitCA with %d days must fail", days)
		}
	}
	for _, name := range []string{"", "..", ".", "a/b", `a\b`, "-lead", ".lead", strings.Repeat("x", MaxNameBytes+1), "has space"} {
		if _, err := store.InitCA(name, 365); err == nil {
			t.Fatalf("InitCA with name %q must fail", name)
		}
	}
	if _, err := store.InitCA("rose-ca", 365); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	for _, hosts := range [][]string{
		{}, {"localhost", "localhost"}, {"bad host!"}, {"-bad.example"}, {"a..b"},
		{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
	} {
		if _, err := store.IssueServer(hosts, 90); err == nil {
			t.Fatalf("IssueServer with %v must fail", hosts)
		}
	}
}

func TestTamperedStore(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InitCA("rose-ca", 365); err != nil {
		t.Fatal(err)
	}
	other := newTestStore(t)
	if _, err := other.InitCA("other-ca", 365); err != nil {
		t.Fatal(err)
	}
	// Swap in another CA's key: cert/key mismatch must fail closed.
	foreign := readFile(t, filepath.Join(other.Dir(), CAKeyFileName))
	if err := os.WriteFile(filepath.Join(store.Dir(), CAKeyFileName), foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueServer([]string{"localhost"}, 90); err == nil {
		t.Fatal("issue with mismatched CA key must fail")
	}
	// Corrupt the CA cert: every CA-dependent operation must fail.
	if err := os.WriteFile(store.CACertPath(), []byte("not a pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueClient("x", 90); err == nil {
		t.Fatal("issue with corrupt CA cert must fail")
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List over a corrupt store must fail, not silently skip")
	}
}

func TestWideDirPermissionsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on windows")
	}
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitCA("rose-ca", 365); err == nil {
		t.Fatal("InitCA in a group/world-accessible directory must fail")
	}
}
