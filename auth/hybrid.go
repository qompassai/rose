package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	AlgMLDSA87 = "ML-DSA-87"

	// Deprecated: legacy OQS algorithms are unsupported, not aliases for ML-DSA.
	AlgDilithium3 = "DILITHIUM_3"
	// Deprecated: legacy OQS algorithms are unsupported, not aliases for ML-DSA.
	AlgDilithium5 = "DILITHIUM_5"
	// Deprecated: legacy OQS algorithms are unsupported, not aliases for ML-DSA.
	AlgFalcon1024 = "FALCON_1024"

	defaultQuantumSigAlg = AlgMLDSA87
	hybridVersion        = "rose-hybrid-v1"
	hybridSuite          = "ssh-ed25519+" + AlgMLDSA87
	quantumContext       = "rose-quantum-v1:" + AlgMLDSA87
)

// ErrUnsupportedAlgorithm means a key/algorithm cannot be used by this package.
// Legacy Dilithium and Falcon blobs must not be interpreted as ML-DSA seeds.
var ErrUnsupportedAlgorithm = errors.New("unsupported signature algorithm")

func checkQuantumAlgorithm(algorithm string) error {
	if algorithm != "" && algorithm != AlgMLDSA87 {
		return fmt.Errorf("%w: %q; provision a new %s key", ErrUnsupportedAlgorithm, algorithm, AlgMLDSA87)
	}
	return nil
}

// GetQuantumPublicKey returns the canonical base64 ML-DSA-87 public key derived
// from the new, versioned private seed. An empty algorithm means ML-DSA-87.
func GetQuantumPublicKey(algorithm string) (string, error) {
	if err := checkQuantumAlgorithm(algorithm); err != nil {
		return "", err
	}
	key, err := loadQuantumKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// GetHybridPublicKeys returns "ssh" (an authorized key) and "ML-DSA-87" (base64).
// A verifier must obtain these through a trusted, independent provisioning path.
func GetHybridPublicKeys() (map[string]string, error) {
	classical, err := GetPublicKey()
	if err != nil {
		return nil, err
	}
	quantum, err := GetQuantumPublicKey(AlgMLDSA87)
	if err != nil {
		return nil, err
	}
	return map[string]string{"ssh": classical, AlgMLDSA87: quantum}, nil
}

func encodeQuantumSignature(key *mldsa.PrivateKey, data []byte, signatureContext string) (string, error) {
	sig, err := key.Sign(rand.Reader, data, &mldsa.Options{Context: signatureContext})
	if err != nil {
		return "", fmt.Errorf("sign ML-DSA-87 message: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()) + ":" +
		base64.StdEncoding.EncodeToString(sig), nil
}

func signQuantum(ctx context.Context, data []byte, algorithm string) (string, error) {
	if err := checkQuantumAlgorithm(algorithm); err != nil {
		return "", err
	}
	if err := checkSigningInput(ctx, data); err != nil {
		return "", err
	}
	key, err := loadQuantumKey()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return encodeQuantumSignature(key, data, quantumContext)
}

// hybridTranscript binds both keys and the suite to each signature. Fixed-size,
// canonical base64 keys cannot contain '|'; the remainder is the original data.
// This is protocol framing, not a replacement for either signature primitive.
func hybridTranscript(data, classicalKey, quantumKey []byte) []byte {
	header := hybridVersion + "|" + hybridSuite + "|" +
		base64.StdEncoding.EncodeToString(classicalKey) + "|" +
		base64.StdEncoding.EncodeToString(quantumKey) + "|"
	transcript := make([]byte, 0, len(header)+len(data))
	transcript = append(transcript, header...)
	return append(transcript, data...)
}

// SignHybrid explicitly signs using BOTH Ed25519 and ML-DSA-87, without fallback.
// The exact, versioned wire form is:
//
//	rose-hybrid-v1|ssh-ed25519+ML-DSA-87|<SSH pub>:<SSH sig>|<ML-DSA pub>:<ML-DSA sig>
//
// All key/signature fields use canonical padded standard base64. Each signature
// covers the version, suite, both public keys and data. ML-DSA also uses the
// "rose-hybrid-v1" context. Old unversioned OQS envelopes are not supported.
// This protocol requires explicit peer support; it does not configure TLS.
func SignHybrid(ctx context.Context, data []byte) (string, error) {
	if err := checkSigningInput(ctx, data); err != nil {
		return "", err
	}
	classical, err := loadSSHSigner()
	if err != nil {
		return "", err
	}
	quantum, err := loadQuantumKey()
	if err != nil {
		return "", err
	}
	transcript := hybridTranscript(data, classical.PublicKey().Marshal(), quantum.PublicKey().Bytes())
	if err := ctx.Err(); err != nil {
		return "", err
	}
	classicalSig, err := encodeSSHSignature(classical, transcript)
	if err != nil {
		return "", err
	}
	quantumSig, err := encodeQuantumSignature(quantum, transcript, hybridVersion)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hybridVersion + "|" + hybridSuite + "|" + classicalSig + "|" + quantumSig, nil
}

type hybridSignature struct {
	classicalKey []byte
	classicalSig []byte
	quantumKey   []byte
	quantumSig   []byte
}

func parseHybridSignature(encoded string) (hybridSignature, error) {
	var result hybridSignature
	if len(encoded) > MaxSignatureBytes {
		return result, errors.New("hybrid signature exceeds size limit")
	}
	parts := strings.Split(encoded, "|")
	if len(parts) != 4 || parts[0] != hybridVersion || parts[1] != hybridSuite {
		return result, errors.New("unsupported or malformed hybrid signature version/suite")
	}
	var err error
	result.classicalKey, result.classicalSig, err = splitSignature(parts[2], sshPublicKeySize, ed25519.SignatureSize)
	if err != nil {
		return result, fmt.Errorf("classical component: %w", err)
	}
	result.quantumKey, result.quantumSig, err = splitSignature(parts[3], mldsa.MLDSA87PublicKeySize, mldsa.MLDSA87SignatureSize)
	if err != nil {
		return result, fmt.Errorf("ML-DSA-87 component: %w", err)
	}
	return result, nil
}

func verifyQuantumParts(data, keyBytes, sigBytes []byte, signatureContext string) (bool, error) {
	key, err := mldsa.NewPublicKey(mldsa.MLDSA87(), keyBytes)
	if err != nil {
		return false, fmt.Errorf("parse ML-DSA-87 public key: %w", err)
	}
	if len(sigBytes) != mldsa.MLDSA87SignatureSize {
		return false, errors.New("incorrect ML-DSA-87 signature size")
	}
	if err := mldsa.Verify(key, data, sigBytes, &mldsa.Options{Context: signatureContext}); err != nil {
		return false, nil
	}
	return true, nil
}

func verifyQuantumSignature(data []byte, encoded, algorithm string) (bool, error) {
	if err := checkQuantumAlgorithm(algorithm); err != nil {
		return false, err
	}
	if err := checkMessage(data); err != nil {
		return false, err
	}
	key, sig, err := splitSignature(encoded, mldsa.MLDSA87PublicKeySize, mldsa.MLDSA87SignatureSize)
	if err != nil {
		return false, err
	}
	return verifyQuantumParts(data, key, sig, quantumContext)
}

func verifyHybridParts(data []byte, sig hybridSignature) (bool, error) {
	transcript := hybridTranscript(data, sig.classicalKey, sig.quantumKey)
	valid, err := verifySSHParts(transcript, sig.classicalKey, sig.classicalSig)
	if err != nil || !valid {
		return false, err
	}
	return verifyQuantumParts(transcript, sig.quantumKey, sig.quantumSig, hybridVersion)
}

// VerifyHybridSignature requires BOTH signatures to be valid, but checks
// cryptographic validity only. The embedded keys are self-asserted, NOT trusted
// identities. Use VerifyHybridSignatureWithKeys for authentication. Callers must
// independently enforce freshness/replay protection and explicit hybrid policy.
func VerifyHybridSignature(data []byte, encoded string) (bool, error) {
	if err := checkMessage(data); err != nil {
		return false, err
	}
	sig, err := parseHybridSignature(encoded)
	if err != nil {
		return false, err
	}
	return verifyHybridParts(data, sig)
}

// VerifyHybridSignatureWithKeys verifies both signatures AND both expected keys.
// trustedSSHPublicKey is an authorized key from GetPublicKey; trustedQuantumKey
// is canonical base64 from GetQuantumPublicKey. Provision both out of band:
// extracting these values from the incoming envelope does not establish trust.
func VerifyHybridSignatureWithKeys(data []byte, encoded, trustedSSHPublicKey, trustedQuantumKey string) (bool, error) {
	if err := checkMessage(data); err != nil {
		return false, err
	}
	classical, err := trustedSSHKey(trustedSSHPublicKey)
	if err != nil {
		return false, fmt.Errorf("trusted SSH key: %w", err)
	}
	quantum, err := decodeBase64(trustedQuantumKey, mldsa.MLDSA87PublicKeySize)
	if err != nil {
		return false, fmt.Errorf("trusted ML-DSA-87 key: %w", err)
	}
	sig, err := parseHybridSignature(encoded)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(classical, sig.classicalKey) || !bytes.Equal(quantum, sig.quantumKey) {
		return false, nil
	}
	return verifyHybridParts(data, sig)
}
