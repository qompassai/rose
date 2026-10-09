package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testHome(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		t.Skip("private key storage requires POSIX permissions and rooted filesystem operations")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".rose"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func testSSHKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

func writeTestSSHKey(t *testing.T, home string) (ed25519.PrivateKey, string) {
	t.Helper()
	priv, pub := testSSHKey(t)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".rose", defaultPrivateKey)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func mustSign(t *testing.T, data []byte) string {
	t.Helper()
	sig, err := Sign(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func requireInvalid(t *testing.T, valid bool, err error) {
	t.Helper()
	if valid {
		t.Fatalf("invalid input accepted (error=%v)", err)
	}
}

func TestSignRegistryCompatibility(t *testing.T) {
	home := testHome(t)
	private, public := writeTestSSHKey(t, home)
	data := []byte("GET,/v2/token?nonce=challenge")
	signature := mustSign(t, data) // No quantum key is installed.
	if strings.Contains(signature, "|") || strings.Count(signature, ":") != 1 {
		t.Fatalf("registry wire changed: %q", signature)
	}
	keyBytes, sigBytes, err := splitSignature(signature, sshPublicKeySize, ed25519.SignatureSize)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(private.Public().(ed25519.PublicKey), data, sigBytes) {
		t.Fatal("registry signature does not verify independently using crypto/ed25519")
	}
	if base64.StdEncoding.EncodeToString(keyBytes) != strings.Fields(public)[1] {
		t.Fatal("registry public key does not match id_ed25519")
	}
	got, err := GetPublicKey()
	if err != nil || got != public {
		t.Fatalf("GetPublicKey = %q, %v; want %q", got, err, public)
	}
	for _, verify := range []func() (bool, error){
		func() (bool, error) { return VerifySignature(data, signature) },
		func() (bool, error) { return VerifySignatureWithKey(data, signature, public) },
	} {
		if valid, err := verify(); err != nil || !valid {
			t.Fatalf("valid classical signature rejected: %v", err)
		}
	}
	if _, err := SignHybrid(context.Background(), data); err == nil {
		t.Fatal("SignHybrid silently fell back without its quantum key")
	}
}

func TestClassicalSignatureTamperingAndTrust(t *testing.T) {
	home := testHome(t)
	_, pub := writeTestSSHKey(t, home)
	data := []byte("registry request")
	sig := mustSign(t, data)
	key, rawSig, err := splitSignature(sig, sshPublicKeySize, ed25519.SignatureSize)
	if err != nil {
		t.Fatal(err)
	}
	rawSig[0] ^= 1
	tampered := base64.StdEncoding.EncodeToString(key) + ":" + base64.StdEncoding.EncodeToString(rawSig)
	for name, signature := range map[string]string{
		"tampered signature": tampered,
		"placeholder":        "anything:anything",
		"empty":              "",
		"extra field":        sig + ":extra",
		"extra component":    sig + "|extra",
		"truncated":          sig[:len(sig)-1],
		"newline":            sig + "\n",
		"oversized":          strings.Repeat("x", MaxSignatureBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			valid, err := verifySSHSignature(data, signature)
			requireInvalid(t, valid, err)
		})
	}
	valid, err := VerifySignature([]byte("modified"), sig)
	requireInvalid(t, valid, err)
	_, wrongPub := testSSHKey(t)
	valid, err = VerifySignatureWithKey(data, sig, wrongPub)
	requireInvalid(t, valid, err)
	for _, malformed := range []string{"", pub + " comment", pub + "\n" + pub, "command=\"x\" " + pub} {
		valid, err = VerifySignatureWithKey(data, sig, malformed)
		if valid || err == nil {
			t.Fatalf("malformed trusted key accepted: %q", malformed)
		}
	}
	// An attacker signing with their own key is cryptographically valid, but
	// must not be authenticated as the original pinned identity.
	_, attackerPub := writeTestSSHKey(t, home)
	attackerSig := mustSign(t, data)
	if valid, err := VerifySignature(data, attackerSig); err != nil || !valid {
		t.Fatalf("self-contained attacker signature should be valid: %v", err)
	}
	valid, err = VerifySignatureWithKey(data, attackerSig, pub)
	requireInvalid(t, valid, err)
	if valid, err := VerifySignatureWithKey(data, attackerSig, attackerPub); err != nil || !valid {
		t.Fatalf("attacker's own pin should validate: %v", err)
	}
}

func TestClassicalKeyTypeIsEnforced(t *testing.T) {
	home := testHome(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("typed public key test")
	sig, err := signer.Sign(rand.Reader, data)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal()) + ":" +
		base64.StdEncoding.EncodeToString(sig.Blob)
	valid, err := verifySSHSignature(data, encoded)
	requireInvalid(t, valid, err)
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".rose", defaultPrivateKey), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(context.Background(), data); err == nil {
		t.Fatal("non-Ed25519 private key was accepted")
	}
}

func TestSigningAndVerificationBounds(t *testing.T) {
	home := testHome(t)
	writeTestSSHKey(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	signers := []func(context.Context, []byte) (string, error){
		Sign, SignHybrid,
		func(ctx context.Context, data []byte) (string, error) { return signQuantum(ctx, data, AlgMLDSA87) },
	}
	for _, sign := range signers {
		if _, err := sign(nil, nil); err == nil {
			t.Fatal("nil context accepted")
		}
		if _, err := sign(ctx, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled context: %v", err)
		}
		if _, err := sign(context.Background(), make([]byte, MaxMessageBytes+1)); err == nil {
			t.Fatal("oversized message accepted by signer")
		}
	}
	for _, verify := range []func([]byte, string) (bool, error){VerifySignature, VerifyHybridSignature} {
		if valid, err := verify(make([]byte, MaxMessageBytes+1), ""); valid || err == nil {
			t.Fatal("oversized message accepted by verifier")
		}
	}
	data := make([]byte, MaxMessageBytes)
	signature := mustSign(t, data)
	if valid, err := VerifySignature(data, signature); err != nil || !valid {
		t.Fatalf("message at maximum size rejected: %v", err)
	}
	signature = mustSign(t, nil)
	if valid, err := VerifySignature(nil, signature); err != nil || !valid {
		t.Fatalf("empty message should be valid: %v", err)
	}
}

type failingNonceReader struct{ err error }

func (r failingNonceReader) Read([]byte) (int, error) { return 0, r.err }

func TestNewNonceBoundsAndErrors(t *testing.T) {
	for _, length := range []int{-1, 0, MinNonceBytes - 1, MaxNonceBytes + 1, int(^uint(0) >> 1)} {
		if _, err := NewNonce(bytes.NewReader(nil), length); err == nil {
			t.Fatalf("invalid nonce length accepted: %d", length)
		}
	}
	for _, length := range []int{MinNonceBytes, MaxNonceBytes} {
		data := bytes.Repeat([]byte{0xab}, length)
		got, err := NewNonce(bytes.NewReader(data), length)
		if err != nil || got != base64.RawURLEncoding.EncodeToString(data) {
			t.Fatalf("nonce length %d = %q, %v", length, got, err)
		}
	}
	if _, err := NewNonce(nil, MinNonceBytes); err == nil {
		t.Fatal("nil nonce reader accepted")
	}
	if _, err := NewNonce(bytes.NewReader([]byte{1}), MinNonceBytes); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated entropy reader: %v", err)
	}
	sentinel := errors.New("entropy unavailable")
	if _, err := NewNonce(failingNonceReader{sentinel}, MinNonceBytes); !errors.Is(err, sentinel) {
		t.Fatalf("entropy error not propagated: %v", err)
	}
}

func FuzzVerifySignature(f *testing.F) {
	f.Add([]byte("message"), "anything:anything")
	f.Add([]byte{}, "")
	f.Add([]byte("message"), strings.Repeat("x", MaxSignatureBytes+1))
	f.Fuzz(func(t *testing.T, data []byte, signature string) {
		_, _ = VerifySignature(data, signature)
	})
}
