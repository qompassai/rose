// Package auth signs Rose application messages. Registry authentication remains
// classical Ed25519; hybrid authentication is an explicit, separately versioned
// protocol. Neither protocol supplies freshness or trusts a self-asserted key.
package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	// MinNonceBytes provides at least 128 bits when the reader is cryptographic.
	MinNonceBytes = 16
	MaxNonceBytes = 1024

	// MaxMessageBytes bounds application authentication work, not request bodies.
	MaxMessageBytes = 1 << 20
	// MaxSignatureBytes bounds an encoded classical or hybrid signature.
	MaxSignatureBytes = 16 << 10

	defaultPrivateKey = "id_ed25519"
	sshPublicKeySize  = 4 + len(ssh.KeyAlgoED25519) + 4 + ed25519.PublicKeySize
)

// NewNonce returns unpadded URL-safe base64 from 16..1024 bytes supplied by r.
// Callers must supply a cryptographically secure reader, normally rand.Reader.
func NewNonce(r io.Reader, length int) (string, error) {
	if r == nil {
		return "", errors.New("nonce reader is nil")
	}
	if length < MinNonceBytes || length > MaxNonceBytes {
		return "", fmt.Errorf("nonce length must be between %d and %d bytes", MinNonceBytes, MaxNonceBytes)
	}
	nonce := make([]byte, length)
	if _, err := io.ReadFull(r, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

func checkMessage(data []byte) error {
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("authentication message exceeds %d bytes", MaxMessageBytes)
	}
	return nil
}

func checkSigningInput(ctx context.Context, data []byte) error {
	if ctx == nil {
		return errors.New("signing context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkMessage(data)
}

// GetPublicKey returns the authorized-key encoding of ~/.rose/id_ed25519.
// Non-Ed25519 keys and unsafe or malformed private-key files are rejected.
func GetPublicKey() (string, error) {
	signer, err := loadSSHSigner()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func loadSSHSigner() (ssh.Signer, error) {
	root, err := openRoseRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	encoded, err := readPrivateFile(root, defaultPrivateKey, maxSSHPrivateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("read SSH private key: %w", err)
	}
	defer clear(encoded)
	signer, err := ssh.ParsePrivateKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key: %w", err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("only ssh-ed25519 private keys are supported")
	}
	return signer, nil
}

func encodeSSHSignature(signer ssh.Signer, data []byte) (string, error) {
	sig, err := signer.Sign(rand.Reader, data)
	if err != nil {
		return "", fmt.Errorf("sign SSH message: %w", err)
	}
	if sig.Format != ssh.KeyAlgoED25519 || len(sig.Blob) != ed25519.SignatureSize {
		return "", errors.New("unexpected SSH signature type or size")
	}
	return base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal()) + ":" +
		base64.StdEncoding.EncodeToString(sig.Blob), nil
}

func signSSH(ctx context.Context, data []byte) (string, error) {
	if err := checkSigningInput(ctx, data); err != nil {
		return "", err
	}
	signer, err := loadSSHSigner()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return encodeSSHSignature(signer, data)
}

// Sign uses the existing registry/updater Ed25519 protocol:
// "<base64 SSH public key>:<base64 raw Ed25519 signature>".
// This is intentionally classical, not a fallback from hybrid signing.
// Use SignHybrid only with a peer that explicitly implements its wire protocol.
func Sign(ctx context.Context, data []byte) (string, error) {
	return signSSH(ctx, data)
}

// decodeBase64 requires exact sizes and canonical padded base64. In particular,
// CR/LF, alternate padding bits, and trailing bytes are not accepted.
func decodeBase64(encoded string, size int) ([]byte, error) {
	if len(encoded) != base64.StdEncoding.EncodedLen(size) {
		return nil, errors.New("incorrect base64 field size")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(decoded) != size || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("noncanonical base64 field")
	}
	return decoded, nil
}

func splitSignature(encoded string, publicKeySize, signatureSize int) ([]byte, []byte, error) {
	if len(encoded) > MaxSignatureBytes {
		return nil, nil, errors.New("signature exceeds size limit")
	}
	keyField, sigField, ok := strings.Cut(encoded, ":")
	if !ok {
		return nil, nil, errors.New("signature must contain a public key and signature")
	}
	key, err := decodeBase64(keyField, publicKeySize)
	if err != nil {
		return nil, nil, fmt.Errorf("public key: %w", err)
	}
	sig, err := decodeBase64(sigField, signatureSize)
	if err != nil {
		return nil, nil, fmt.Errorf("signature: %w", err)
	}
	return key, sig, nil
}

func parseSSHPublicKey(encoded []byte) (ssh.PublicKey, error) {
	if len(encoded) != sshPublicKeySize {
		return nil, errors.New("incorrect SSH public key size")
	}
	key, err := ssh.ParsePublicKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("parse SSH public key: %w", err)
	}
	if key.Type() != ssh.KeyAlgoED25519 || !bytes.Equal(key.Marshal(), encoded) {
		return nil, errors.New("only canonical ssh-ed25519 public keys are supported")
	}
	return key, nil
}

func verifySSHParts(data, keyBytes, sigBytes []byte) (bool, error) {
	key, err := parseSSHPublicKey(keyBytes)
	if err != nil {
		return false, err
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return false, errors.New("incorrect Ed25519 signature size")
	}
	if err := key.Verify(data, &ssh.Signature{Format: ssh.KeyAlgoED25519, Blob: sigBytes}); err != nil {
		return false, nil
	}
	return true, nil
}

func verifySSHSignature(data []byte, encoded string) (bool, error) {
	if err := checkMessage(data); err != nil {
		return false, err
	}
	key, sig, err := splitSignature(encoded, sshPublicKeySize, ed25519.SignatureSize)
	if err != nil {
		return false, err
	}
	return verifySSHParts(data, key, sig)
}

// VerifySignature verifies Sign's classical format. It checks cryptographic
// validity only: the embedded public key is NOT a trusted identity.
// Malformed input returns an error; an invalid signature returns false, nil.
func VerifySignature(data []byte, encoded string) (bool, error) {
	return verifySSHSignature(data, encoded)
}

// trustedSSHKey accepts exactly the authorized-key form returned by GetPublicKey,
// with optional surrounding whitespace but no options, comments or extra keys.
func trustedSSHKey(encoded string) ([]byte, error) {
	if len(encoded) > 256 {
		return nil, errors.New("trusted SSH public key exceeds size limit")
	}
	fields := strings.Fields(encoded)
	if len(fields) != 2 || fields[0] != ssh.KeyAlgoED25519 {
		return nil, errors.New("trusted public key must be a single ssh-ed25519 authorized key without options or comments")
	}
	key, err := decodeBase64(fields[1], sshPublicKeySize)
	if err != nil {
		return nil, err
	}
	if _, err := parseSSHPublicKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

// VerifySignatureWithKey verifies both the classical signature and its identity.
// trustedPublicKey must be provisioned out of band, never copied from the
// incoming signature. Its format is the authorized key returned by GetPublicKey.
func VerifySignatureWithKey(data []byte, encoded, trustedPublicKey string) (bool, error) {
	if err := checkMessage(data); err != nil {
		return false, err
	}
	expected, err := trustedSSHKey(trustedPublicKey)
	if err != nil {
		return false, fmt.Errorf("trusted SSH public key: %w", err)
	}
	key, sig, err := splitSignature(encoded, sshPublicKeySize, ed25519.SignatureSize)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(expected, key) {
		return false, nil
	}
	return verifySSHParts(data, key, sig)
}

// GetPQSSHRecommendedConfig suggests hybrid SSH key exchange, where supported by
// the installed OpenSSH. SSH host/user signatures below are classical Ed25519.
// This does not configure Rose TLS or provide post-quantum SSH authentication.
func GetPQSSHRecommendedConfig() string {
	return `
HostKeyAlgorithms ssh-ed25519
PubkeyAcceptedAlgorithms ssh-ed25519
KexAlgorithms mlkem768x25519-sha256,sntrup761x25519-sha512@openssh.com
MACs hmac-sha2-512-etm@openssh.com,hmac-sha2-256-etm@openssh.com
Ciphers chacha20-poly1305@openssh.com,aes256-gcm@openssh.com,aes128-gcm@openssh.com
`
}
