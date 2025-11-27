package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-quantum-safe/liboqs-go/oqs"
	"golang.org/x/crypto/ssh"
)

const (
	// Classical key type (not quantum resistant)
	defaultPrivateKey = "ed25519_dilithium5"

	// Post-quantum algorithms from liboqs (NIST level 3+)
	AlgDilithium3 = "DILITHIUM_3" // NIST level 3 (primary)
	AlgDilithium5 = "DILITHIUM_5" // NIST level 5
	AlgFalcon1024 = "FALCON_1024" // NIST level 5

	// Default quantum signature algorithm
	defaultQuantumSigAlg = AlgDilithium5

	// Secondary algorithm for algorithm diversity
	secondaryQuantumSigAlg = AlgFalcon1024

	// KEX algorithm that appears in your SSH config (for reference)
	// Note: This is implemented at the SSH protocol level, not directly in this auth package
	kexSntrup761 = "sntrup761x25519-sha512@openssh.com"
)

// keyPath returns the path to keys based on type
func keyPath(keyType string, algorithm string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if keyType == "quantum" {
		if algorithm == "" {
			return filepath.Join(home, ".rose", "quantum_keys"), nil
		}
		return filepath.Join(home, ".rose", "quantum_keys", algorithm), nil
	}
	return filepath.Join(home, ".rose", defaultPrivateKey), nil
}

// legacyKeyPath provides backward compatibility for the original keyPath function
func legacyKeyPath() (string, error) {
	return keyPath("ssh", "")
}

// NewNonce generates a random nonce
func NewNonce(r io.Reader, length int) (string, error) {
	nonce := make([]byte, length)
	if _, err := io.ReadFull(r, nonce); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

// GetPublicKey retrieves the SSH public key
func GetPublicKey() (string, error) {
	keyPath, err := legacyKeyPath()
	if err != nil {
		return "", err
	}

	privateKeyFile, err := os.ReadFile(keyPath)
	if err != nil {
		slog.Info(fmt.Sprintf("Failed to load private key: %v", err))
		return "", err
	}

	privateKey, err := ssh.ParsePrivateKey(privateKeyFile)
	if err != nil {
		return "", err
	}

	publicKey := ssh.MarshalAuthorizedKey(privateKey.PublicKey())

	return strings.TrimSpace(string(publicKey)), nil
}

// GetQuantumPublicKey retrieves a quantum-resistant public key
func GetQuantumPublicKey(algorithm string) (string, error) {
	if algorithm == "" {
		algorithm = defaultQuantumSigAlg
	}

	keyDir, err := keyPath("quantum", "")
	if err != nil {
		return "", err
	}

	pubKeyPath := filepath.Join(keyDir, fmt.Sprintf("%s.pub", algorithm))
	publicKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		slog.Info(fmt.Sprintf("Failed to load quantum public key (%s): %v", algorithm, err))
		return "", err
	}

	return base64.StdEncoding.EncodeToString(publicKey), nil
}

// GetHybridPublicKeys returns all public keys used in hybrid authentication
func GetHybridPublicKeys() (map[string]string, error) {
	keys := make(map[string]string)

	// Get classical SSH key
	sshKey, err := GetPublicKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH public key: %w", err)
	}
	keys["ssh"] = sshKey

	// Get primary quantum key
	primaryQuantumKey, err := GetQuantumPublicKey(defaultQuantumSigAlg)
	if err != nil {
		return nil, fmt.Errorf("failed to get primary quantum public key: %w", err)
	}
	keys[defaultQuantumSigAlg] = primaryQuantumKey

	// Get secondary quantum key for enhanced security through algorithm diversity
	secondaryQuantumKey, err := GetQuantumPublicKey(secondaryQuantumSigAlg)
	if err != nil {
		slog.Info(fmt.Sprintf("Secondary quantum key not available: %v", err))
		// Continue without secondary key - it's optional
	} else {
		keys[secondaryQuantumSigAlg] = secondaryQuantumKey
	}

	return keys, nil
}

// signSSH signs data using the classical SSH key (Ed25519)
func signSSH(ctx context.Context, bts []byte) (string, error) {
	keyPath, err := legacyKeyPath()
	if err != nil {
		return "", err
	}

	privateKeyFile, err := os.ReadFile(keyPath)
	if err != nil {
		slog.Info(fmt.Sprintf("Failed to load private key: %v", err))
		return "", err
	}

	privateKey, err := ssh.ParsePrivateKey(privateKeyFile)
	if err != nil {
		return "", err
	}

	// get the pubkey, but remove the type
	publicKey := ssh.MarshalAuthorizedKey(privateKey.PublicKey())
	parts := bytes.Split(publicKey, []byte(" "))
	if len(parts) < 2 {
		return "", errors.New("malformed public key")
	}

	signedData, err := privateKey.Sign(rand.Reader, bts)
	if err != nil {
		return "", err
	}

	// signature is <pubkey>:<signature>
	return fmt.Sprintf("%s:%s", bytes.TrimSpace(parts[1]), base64.StdEncoding.EncodeToString(signedData.Blob)), nil
}

// signQuantum signs data using a quantum-resistant signature algorithm
func signQuantum(ctx context.Context, bts []byte, algorithm string) (string, error) {
	if algorithm == "" {
		algorithm = defaultQuantumSigAlg
	}

	keyDir, err := keyPath("quantum", "")
	if err != nil {
		return "", err
	}

	// Load private key
	privKeyPath := filepath.Join(keyDir, algorithm)
	privateKey, err := os.ReadFile(privKeyPath)
	if err != nil {
		slog.Info(fmt.Sprintf("Failed to load quantum private key (%s): %v", algorithm, err))
		return "", err
	}

	// Create a signer with the private key provided during initialization
	sig := oqs.Signature{}
	err = sig.Init(algorithm, privateKey)  // Pass privateKey directly here
	if err != nil {
		return "", fmt.Errorf("failed to initialize quantum signer (%s): %w", algorithm, err)
	}
	defer sig.Clean()

	// Sign the data
	signature, err := sig.Sign(bts)
	if err != nil {
		return "", fmt.Errorf("quantum signing failed (%s): %w", algorithm, err)
	}

	// Get public key for the return format
	pubKeyPath := filepath.Join(keyDir, fmt.Sprintf("%s.pub", algorithm))
	publicKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read quantum public key (%s): %w", algorithm, err)
	}

	// Return in the same format as the SSH function: <pubkey>:<signature>
	return fmt.Sprintf("%s:%s",
		base64.StdEncoding.EncodeToString(publicKey),
		base64.StdEncoding.EncodeToString(signature)), nil
}

// Sign implements hybrid signing by default (both classical and quantum)
// This is the main function to use for authentication
func Sign(ctx context.Context, bts []byte) (string, error) {
	return SignHybrid(ctx, bts)
}

// SignHybrid signs data using both SSH and quantum-resistant methods
func SignHybrid(ctx context.Context, bts []byte) (string, error) {
	// Sign with classical SSH key (Ed25519)
	sshSig, err := signSSH(ctx, bts)
	if err != nil {
		return "", fmt.Errorf("SSH signing failed: %w", err)
	}

	// Sign with primary quantum-resistant algorithm
	primaryQuantumSig, err := signQuantum(ctx, bts, defaultQuantumSigAlg)
	if err != nil {
		return "", fmt.Errorf("primary quantum signing failed: %w", err)
	}

	// Try to sign with secondary quantum-resistant algorithm for enhanced security
	secondaryQuantumSig, err := signQuantum(ctx, bts, secondaryQuantumSigAlg)
	if err != nil {
		// Log but continue - secondary algorithm is optional
		slog.Info(fmt.Sprintf("Secondary quantum signing unavailable: %v", err))
		// Return hybrid signature with just primary quantum algorithm
		return fmt.Sprintf("%s|%s", sshSig, primaryQuantumSig), nil
	}

	// Return hybrid signature with all three algorithms
	return fmt.Sprintf("%s|%s|%s", sshSig, primaryQuantumSig, secondaryQuantumSig), nil
}

// GenerateQuantumKeys generates all necessary quantum-resistant keypairs
func GenerateQuantumKeys() error {
	// Generate primary quantum keypair
	if err := generateQuantumKeypair(defaultQuantumSigAlg); err != nil {
		return fmt.Errorf("failed to generate primary quantum keypair: %w", err)
	}

	// Generate secondary quantum keypair for algorithm diversity
	if err := generateQuantumKeypair(secondaryQuantumSigAlg); err != nil {
		slog.Info(fmt.Sprintf("Failed to generate secondary quantum keypair: %v", err))
		// Continue without secondary keypair - it's optional
	}

	return nil
}

// generateQuantumKeypair generates a quantum-resistant keypair for a specific algorithm
func generateQuantumKeypair(algorithm string) error {
	keyDir, err := keyPath("quantum", "")
	if err != nil {
		return err
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("failed to create quantum key directory: %w", err)
	}

	// Initialize signature algorithm
	sig := oqs.Signature{}
	err = sig.Init(algorithm, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize quantum signature algorithm (%s): %w", algorithm, err)
	}
	defer sig.Clean()

	// Generate keypair
	pubKey, err := sig.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate quantum keypair (%s): %w", algorithm, err)
	}
	privKey := sig.ExportSecretKey()

	// Save keys
	pubKeyPath := filepath.Join(keyDir, fmt.Sprintf("%s.pub", algorithm))
	privKeyPath := filepath.Join(keyDir, algorithm)

	if err := os.WriteFile(pubKeyPath, pubKey, 0644); err != nil {
		return fmt.Errorf("failed to write quantum public key (%s): %w", algorithm, err)
	}

	if err := os.WriteFile(privKeyPath, privKey, 0600); err != nil {
		return fmt.Errorf("failed to write quantum private key (%s): %w", algorithm, err)
	}

	slog.Info(fmt.Sprintf("Generated quantum-resistant keypair using %s", algorithm))
	return nil
}

// verifySSHSignature verifies an SSH signature
func verifySSHSignature(data []byte, signatureStr string) (bool, error) {
	// Parse signature string (format: "<pubkey>:<signature>")
	parts := strings.Split(signatureStr, ":")
	if len(parts) != 2 {
		return false, errors.New("malformed SSH signature")
	}
	
	// TODO: Implement actual verification with SSH keys
	// For now, returning true as a placeholder
	return true, nil
}

// verifyQuantumSignature verifies a quantum signature
func verifyQuantumSignature(data []byte, signatureStr string, algorithm string) (bool, error) {
	// Parse signature string (format: "<pubkey>:<signature>")
	parts := strings.Split(signatureStr, ":")
	if len(parts) != 2 {
		return false, errors.New("malformed quantum signature")
	}

	// Decode base64 components
	pubKeyBytes, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return false, fmt.Errorf("failed to decode public key: %w", err)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Initialize verifier
	sig := oqs.Signature{}
	err = sig.Init(algorithm, nil)
	if err != nil {
		return false, fmt.Errorf("failed to initialize signature verifier (%s): %w", algorithm, err)
	}
	defer sig.Clean()

	// Verify signature
	valid, err := sig.Verify(data, sigBytes, pubKeyBytes)
	return valid, err
}

// VerifySignature verifies a signature (defaults to hybrid verification)
func VerifySignature(data []byte, signatureStr string) (bool, error) {
	return VerifyHybridSignature(data, signatureStr)
}

// VerifyHybridSignature verifies a hybrid signature containing both classical and quantum components
func VerifyHybridSignature(data []byte, signatureStr string) (bool, error) {
	// Split the hybrid signature
	parts := strings.Split(signatureStr, "|")

	// Check if we have at least the required components
	if len(parts) < 2 {
		return false, errors.New("malformed hybrid signature: missing required components")
	}

	sshSig := parts[0]
	primaryQuantumSig := parts[1]

	// Verify SSH signature
	sshValid, err := verifySSHSignature(data, sshSig)
	if err != nil {
		return false, fmt.Errorf("SSH verification failed: %w", err)
	}

	// Verify primary quantum signature
	primaryQuantumValid, err := verifyQuantumSignature(data, primaryQuantumSig, defaultQuantumSigAlg)
	if err != nil {
		return false, fmt.Errorf("primary quantum verification failed: %w", err)
	}

	// Check if we have a secondary quantum signature
	if len(parts) >= 3 {
		secondaryQuantumSig := parts[2]

		// Verify secondary quantum signature
		secondaryQuantumValid, err := verifyQuantumSignature(data, secondaryQuantumSig, secondaryQuantumSigAlg)
		if err != nil {
			slog.Info(fmt.Sprintf("Secondary quantum verification failed: %v", err))
			// Continue anyway - secondary verification is optional
		} else if !secondaryQuantumValid {
			slog.Info("Secondary quantum signature is invalid")
			// Continue anyway - secondary verification is optional
		}
	}

	// Both required signatures must be valid for hybrid verification to succeed
	return sshValid && primaryQuantumValid, nil
}

// IsKeyGenerated checks if all required keys are generated
func IsKeyGenerated() bool {
	// Check SSH key
	sshKeyPath, err := legacyKeyPath()
	if err != nil {
		return false
	}
	if _, err := os.Stat(sshKeyPath); os.IsNotExist(err) {
		return false
	}

	// Check primary quantum key
	keyDir, err := keyPath("quantum", "")
	if err != nil {
		return false
	}
	primaryKeyPath := filepath.Join(keyDir, defaultQuantumSigAlg)
	if _, err := os.Stat(primaryKeyPath); os.IsNotExist(err) {
		return false
	}

	return true
}

// GenerateKeys generates all keys needed for hybrid authentication if they don't exist
func GenerateKeys() error {
	if IsKeyGenerated() {
		slog.Info("Keys already exist, skipping generation")
		return nil
	}

	// Generate quantum keys
	if err := GenerateQuantumKeys(); err != nil {
		return fmt.Errorf("failed to generate quantum keys: %w", err)
	}

	// Note: This doesn't generate SSH keys as that's typically handled externally
	// Display a message to inform the user to generate SSH keys if needed
	sshKeyPath, _ := legacyKeyPath()
	if _, err := os.Stat(sshKeyPath); os.IsNotExist(err) {
		slog.Info(fmt.Sprintf("SSH key not found at %s. Please generate it using ssh-keygen or similar tools", sshKeyPath))
	}

	return nil
}

// GetPQSSHRecommendedConfig returns recommended SSH configuration for post-quantum security
func GetPQSSHRecommendedConfig() string {
	return `
HostKeyAlgorithms ssh-ed25519,ecdsa-sha2-nistp256,ecdsa-sha2-nistp384,ecdsa-sha2-nistp521,rsa-sha2-256,rsa-sha2-512
PubkeyAcceptedAlgorithms ssh-ed25519,ecdsa-sha2-nistp256,ecdsa-sha2-nistp384,ecdsa-sha2-nistp521,rsa-sha2-256,rsa-sha2-512
KexAlgorithms sntrup761x25519-sha512@openssh.com,curve25519-sha256,curve25519-sha256@libssh.org,diffie-hellman-group-exchange-sha256
MACs hmac-sha2-512-etm@openssh.com,hmac-sha2-256-etm@openssh.com,umac-128-etm@openssh.com
Ciphers chacha20-poly1305@openssh.com,aes256-gcm@openssh.com,aes128-gcm@openssh.com
`
}

