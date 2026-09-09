package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testHybridSignature(t *testing.T, data []byte) (string, map[string]string) {
	t.Helper()
	home := testHome(t)
	writeTestSSHKey(t, home)
	if err := GenerateKeys(); err != nil {
		t.Fatal(err)
	}
	keys, err := GetHybridPublicKeys()
	if err != nil {
		t.Fatal(err)
	}
	signature, err := SignHybrid(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	return signature, keys
}

func TestHybridSignatureRoundTripAndIndependentVerification(t *testing.T) {
	data := []byte("request\x00with|separators:and\nbinary\xff")
	signature, keys := testHybridSignature(t, data)
	for _, verify := range []func() (bool, error){
		func() (bool, error) { return VerifyHybridSignature(data, signature) },
		func() (bool, error) {
			return VerifyHybridSignatureWithKeys(data, signature, keys["ssh"], keys[AlgMLDSA87])
		},
	} {
		if valid, err := verify(); err != nil || !valid {
			t.Fatalf("valid hybrid signature rejected: %v", err)
		}
	}
	parts, err := parseHybridSignature(signature)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the documented transcript independently of the implementation.
	transcript := []byte("rose-hybrid-v1|ssh-ed25519+ML-DSA-87|" +
		base64.StdEncoding.EncodeToString(parts.classicalKey) + "|" +
		base64.StdEncoding.EncodeToString(parts.quantumKey) + "|")
	transcript = append(transcript, data...)
	if !ed25519.Verify(ed25519.PublicKey(parts.classicalKey[len(parts.classicalKey)-ed25519.PublicKeySize:]),
		transcript, parts.classicalSig) {
		t.Fatal("Ed25519 hybrid signature failed independent verification")
	}
	quantum, err := mldsa.NewPublicKey(mldsa.MLDSA87(), parts.quantumKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := mldsa.Verify(quantum, transcript, parts.quantumSig, &mldsa.Options{Context: "rose-hybrid-v1"}); err != nil {
		t.Fatalf("standard-library ML-DSA hybrid signature failed independent verification: %v", err)
	}
	if err := mldsa.Verify(quantum, transcript, parts.quantumSig, &mldsa.Options{}); err == nil {
		t.Fatal("ML-DSA context was not enforced")
	}
	if valid, err := VerifySignature(data, signature); valid || err == nil {
		t.Fatal("classical verifier implicitly accepted hybrid protocol")
	}
}

func tamperHybridField(t *testing.T, signature string, component, field int) string {
	t.Helper()
	parts := strings.Split(signature, "|")
	fields := strings.Split(parts[component], ":")
	decoded, err := base64.StdEncoding.DecodeString(fields[field])
	if err != nil {
		t.Fatal(err)
	}
	decoded[len(decoded)-1] ^= 1
	fields[field] = base64.StdEncoding.EncodeToString(decoded)
	parts[component] = strings.Join(fields, ":")
	return strings.Join(parts, "|")
}

func TestHybridTamperedHalvesAndKeys(t *testing.T) {
	data := []byte("hybrid authorization challenge")
	signature, keys := testHybridSignature(t, data)
	for _, tc := range []struct {
		name             string
		component, field int
	}{
		{"classical signature", 2, 1},
		{"quantum signature", 3, 1},
		{"classical key", 2, 0},
		{"quantum key", 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := tamperHybridField(t, signature, tc.component, tc.field)
			valid, err := VerifyHybridSignature(data, invalid)
			requireInvalid(t, valid, err)
			valid, err = VerifyHybridSignatureWithKeys(data, invalid, keys["ssh"], keys[AlgMLDSA87])
			requireInvalid(t, valid, err)
		})
	}
	valid, err := VerifyHybridSignature([]byte("tampered message"), signature)
	requireInvalid(t, valid, err)
}

func TestHybridTrustedIdentity(t *testing.T) {
	data := []byte("identity")
	signature, keys := testHybridSignature(t, data)
	_, wrongSSH := testSSHKey(t)
	wrongQuantum, err := mldsa.GenerateKey(mldsa.MLDSA87())
	if err != nil {
		t.Fatal(err)
	}
	wrongQuantumText := base64.StdEncoding.EncodeToString(wrongQuantum.PublicKey().Bytes())
	for _, tc := range []struct{ name, ssh, quantum string }{
		{"wrong classical pin", wrongSSH, keys[AlgMLDSA87]},
		{"wrong quantum pin", keys["ssh"], wrongQuantumText},
		{"both wrong", wrongSSH, wrongQuantumText},
		{"missing classical pin", "", keys[AlgMLDSA87]},
		{"missing quantum pin", keys["ssh"], ""},
		{"malformed classical pin", "ssh-ed25519 not-base64", keys[AlgMLDSA87]},
		{"malformed quantum pin", keys["ssh"], "not-base64"},
		{"oversized quantum pin", keys["ssh"], strings.Repeat("x", MaxSignatureBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := VerifyHybridSignatureWithKeys(data, signature, tc.ssh, tc.quantum)
			requireInvalid(t, valid, err)
		})
	}
	// A separate, valid identity must not authenticate against the first pins.
	attackerSig, attackerKeys := testHybridSignature(t, data)
	if valid, err := VerifyHybridSignature(data, attackerSig); err != nil || !valid {
		t.Fatalf("self-contained second identity should verify: %v", err)
	}
	valid, err := VerifyHybridSignatureWithKeys(data, attackerSig, keys["ssh"], keys[AlgMLDSA87])
	requireInvalid(t, valid, err)
	if valid, err := VerifyHybridSignatureWithKeys(data, attackerSig, attackerKeys["ssh"], attackerKeys[AlgMLDSA87]); err != nil || !valid {
		t.Fatalf("second identity's own pins should verify: %v", err)
	}
}

func TestHybridRejectsMalformedProtocol(t *testing.T) {
	data := []byte("protocol framing")
	signature, _ := testHybridSignature(t, data)
	parts := strings.Split(signature, "|")
	cases := map[string]string{
		"empty":                 "",
		"classical only":        parts[2],
		"unversioned OQS":       parts[2] + "|" + parts[3],
		"missing quantum":       strings.Join(parts[:3], "|"),
		"missing classical":     parts[0] + "|" + parts[1] + "||" + parts[3],
		"extra component":       signature + "|extra",
		"extra empty component": signature + "|",
		"truncated":             signature[:len(signature)-1],
		"extra colon":           signature + ":",
		"unsupported version":   strings.Replace(signature, hybridVersion, "rose-hybrid-v2", 1),
		"unsupported suite":     strings.Replace(signature, AlgMLDSA87, AlgDilithium5, 1),
		"wrong ML-DSA set":      strings.Replace(signature, AlgMLDSA87, "ML-DSA-65", 1),
		"missing padding":       strings.TrimRight(signature, "="),
		"whitespace":            signature + "\n",
		"oversized":             strings.Repeat("x", MaxSignatureBytes+1),
	}
	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			if valid, err := VerifyHybridSignature(data, invalid); valid || err == nil {
				t.Fatalf("malformed protocol should fail parsing: valid=%v err=%v", valid, err)
			}
		})
	}
}

func TestHybridBindsBothKeysAndRejectsComponentSplicing(t *testing.T) {
	data := []byte("same message, different identities")
	first, _ := testHybridSignature(t, data)
	second, _ := testHybridSignature(t, data)
	firstParts, secondParts := strings.Split(first, "|"), strings.Split(second, "|")
	firstParts[3] = secondParts[3]
	valid, err := VerifyHybridSignature(data, strings.Join(firstParts, "|"))
	requireInvalid(t, valid, err)
	// The same quantum key and message, but two classical keys, must produce
	// different transcripts. No half can be borrowed across these identities.
	firstParsed, err := parseHybridSignature(first)
	if err != nil {
		t.Fatal(err)
	}
	secondParsed, err := parseHybridSignature(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hybridTranscript(data, firstParsed.classicalKey, firstParsed.quantumKey),
		hybridTranscript(data, secondParsed.classicalKey, firstParsed.quantumKey)) {
		t.Fatal("transcript omitted classical identity")
	}
	if bytes.Equal(hybridTranscript(data, firstParsed.classicalKey, firstParsed.quantumKey),
		hybridTranscript(data, firstParsed.classicalKey, secondParsed.quantumKey)) {
		t.Fatal("transcript omitted quantum identity")
	}
}

func TestStandaloneQuantumAndContextSeparation(t *testing.T) {
	data := []byte("separate signature domain")
	_, _ = testHybridSignature(t, data)
	signature, err := signQuantum(context.Background(), data, "")
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := verifyQuantumSignature(data, signature, AlgMLDSA87); err != nil || !valid {
		t.Fatalf("standalone ML-DSA round trip failed: %v", err)
	}
	key, sig, err := splitSignature(signature, mldsa.MLDSA87PublicKeySize, mldsa.MLDSA87SignatureSize)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := verifyQuantumParts(data, key, sig, hybridVersion)
	requireInvalid(t, valid, err)
	valid, err = verifyQuantumSignature([]byte("tampered"), signature, AlgMLDSA87)
	requireInvalid(t, valid, err)
	sig[0] ^= 1
	invalid := base64.StdEncoding.EncodeToString(key) + ":" + base64.StdEncoding.EncodeToString(sig)
	valid, err = verifyQuantumSignature(data, invalid, AlgMLDSA87)
	requireInvalid(t, valid, err)
}

func TestUnsafeLegacyAlgorithmsAreExplicitErrors(t *testing.T) {
	for _, algorithm := range []string{AlgDilithium3, AlgDilithium5, AlgFalcon1024, "../id_ed25519", "/tmp/key", "ML-DSA-65", "ml-dsa-87"} {
		t.Run(algorithm, func(t *testing.T) {
			if _, err := GetQuantumPublicKey(algorithm); !errors.Is(err, ErrUnsupportedAlgorithm) {
				t.Fatalf("GetQuantumPublicKey error = %v", err)
			}
			if _, err := signQuantum(context.Background(), nil, algorithm); !errors.Is(err, ErrUnsupportedAlgorithm) {
				t.Fatalf("signQuantum error = %v", err)
			}
			if _, err := verifyQuantumSignature(nil, "", algorithm); !errors.Is(err, ErrUnsupportedAlgorithm) {
				t.Fatalf("verifyQuantumSignature error = %v", err)
			}
			if err := generateQuantumKeypair(algorithm); !errors.Is(err, ErrUnsupportedAlgorithm) {
				t.Fatalf("generateQuantumKeypair error = %v", err)
			}
			if _, err := keyPath("quantum", algorithm); !errors.Is(err, ErrUnsupportedAlgorithm) {
				t.Fatalf("keyPath error = %v", err)
			}
		})
	}
}

func TestMLDSASignatureCannotBeForgedWithPublicKey(t *testing.T) {
	data := []byte("real primitive")
	signature, _ := testHybridSignature(t, data)
	parts := strings.Split(signature, "|")
	fields := strings.Split(parts[3], ":")
	fake := make([]byte, mldsa.MLDSA87SignatureSize)
	if _, err := rand.Read(fake); err != nil {
		t.Fatal(err)
	}
	fields[1] = base64.StdEncoding.EncodeToString(fake)
	parts[3] = strings.Join(fields, ":")
	valid, err := VerifyHybridSignature(data, strings.Join(parts, "|"))
	requireInvalid(t, valid, err)
}

func FuzzVerifyHybridSignature(f *testing.F) {
	f.Add([]byte("message"), "rose-hybrid-v1|ssh-ed25519+ML-DSA-87|x:y|z:w")
	f.Add([]byte{}, "")
	f.Add([]byte("message"), strings.Repeat("|", MaxSignatureBytes+1))
	f.Fuzz(func(t *testing.T, data []byte, signature string) {
		_, _ = VerifyHybridSignature(data, signature)
	})
}
