package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/qompassai/rose/auth"
)

// These tests prove the hardened auth package (D3 wholesale) is what the
// client's signing path actually executes: getAuthorizationToken is the
// production caller behind do/stream request signing, so its behaviour
// with each class of bad key material is the wiring contract.

func wiringTestHome(t *testing.T) string {
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

func writeWiringKey(t *testing.T, home string, block *pem.Block, perm os.FileMode) {
	t.Helper()
	path := filepath.Join(home, ".rose", "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func ed25519KeyBlock(t *testing.T) *pem.Block {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func TestGetAuthorizationTokenSignsWithValidKey(t *testing.T) {
	home := wiringTestHome(t)
	writeWiringKey(t, home, ed25519KeyBlock(t), 0o600)

	const challenge = "GET,/api/tags"
	token, err := getAuthorizationToken(context.Background(), challenge)
	if err != nil {
		t.Fatalf("getAuthorizationToken with a valid key: %v", err)
	}
	if token == "" {
		t.Fatal("getAuthorizationToken returned an empty token")
	}
	valid, err := auth.VerifySignature([]byte(challenge), token)
	if err != nil {
		t.Fatalf("VerifySignature on the caller's token: %v", err)
	}
	if !valid {
		t.Fatal("token produced by getAuthorizationToken does not verify")
	}
}

func TestGetAuthorizationTokenRejectsNonEd25519Key(t *testing.T) {
	home := wiringTestHome(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(rsaKey, "")
	if err != nil {
		t.Fatal(err)
	}
	writeWiringKey(t, home, block, 0o600)

	_, err = getAuthorizationToken(context.Background(), "GET,/api/tags")
	if err == nil {
		t.Fatal("getAuthorizationToken accepted a non-Ed25519 key")
	}
	if !strings.Contains(err.Error(), "only ssh-ed25519 private keys are supported") {
		t.Fatalf("unexpected error for non-Ed25519 key: %v", err)
	}
}

func TestGetAuthorizationTokenRejectsLooseKeyPermissions(t *testing.T) {
	home := wiringTestHome(t)
	writeWiringKey(t, home, ed25519KeyBlock(t), 0o644)

	_, err := getAuthorizationToken(context.Background(), "GET,/api/tags")
	if err == nil {
		t.Fatal("getAuthorizationToken accepted a group/other-readable key file")
	}
	if !strings.Contains(err.Error(), "unsafe private key permissions") {
		t.Fatalf("unexpected error for loose key permissions: %v", err)
	}
}

func TestGetAuthorizationTokenRejectsOversizedChallenge(t *testing.T) {
	home := wiringTestHome(t)
	writeWiringKey(t, home, ed25519KeyBlock(t), 0o600)

	challenge := strings.Repeat("a", auth.MaxMessageBytes+1)
	_, err := getAuthorizationToken(context.Background(), challenge)
	if err == nil {
		t.Fatal("getAuthorizationToken signed a challenge over the message bound")
	}
	if !strings.Contains(err.Error(), "authentication message exceeds") {
		t.Fatalf("unexpected error for oversized challenge: %v", err)
	}
}
