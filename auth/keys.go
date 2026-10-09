package auth

import (
	"crypto/mldsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const (
	maxSSHPrivateKeyBytes = 16 << 10
	quantumKeyFile        = AlgMLDSA87 + ".seed"
	quantumSeedPrefix     = "rose-key-v1:" + AlgMLDSA87 + ":"
	quantumSeedFileSize   = len(quantumSeedPrefix) + 44 + 1 // Base64 32-byte seed + newline.
)

// keyPath validates identifiers before constructing paths. Legacy OQS filenames
// are deliberately not consulted, even when a new ML-DSA key is absent.
func keyPath(keyType, algorithm string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch keyType {
	case "ssh":
		if algorithm != "" {
			return "", fmt.Errorf("%w: SSH %q", ErrUnsupportedAlgorithm, algorithm)
		}
		return filepath.Join(home, ".rose", defaultPrivateKey), nil
	case "quantum":
		if err := checkQuantumAlgorithm(algorithm); err != nil {
			return "", err
		}
		dir := filepath.Join(home, ".rose", "quantum_keys")
		if algorithm == "" {
			return dir, nil
		}
		return filepath.Join(dir, quantumKeyFile), nil
	default:
		return "", fmt.Errorf("unknown key type %q", keyType)
	}
}

func legacyKeyPath() (string, error) {
	return keyPath("ssh", "")
}

// checkDirectory permits the CLI's existing 0755 .rose directory, but never
// group/other writable directories. The quantum directory must be private.
func checkDirectory(info os.FileInfo, private bool) error {
	if !info.IsDir() {
		return errors.New("key directory is not a directory or is a symlink")
	}
	forbidden := os.FileMode(0o022)
	if private {
		forbidden = 0o077
	}
	if info.Mode().Perm()&forbidden != 0 || info.Mode().Perm()&0o700 != 0o700 {
		return fmt.Errorf("unsafe key directory permissions %04o", info.Mode().Perm())
	}
	return nil
}

// openChildRoot rejects symlinks before opening and checks the opened directory's
// identity afterwards. os.Root contains all subsequent traversal. A directory
// writable by another account is rejected, not silently chmodded.
func openChildRoot(parent *os.Root, name string, private bool) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkDirectory(before, private); err != nil {
		return nil, err
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err == nil && !os.SameFile(before, after) {
		err = errors.New("key directory changed while opening")
	}
	if err == nil {
		err = checkDirectory(after, private)
	}
	if err != nil {
		child.Close()
		return nil, err
	}
	return child, nil
}

func openRoseRoot() (*os.Root, error) {
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		return nil, errors.New("private key storage requires POSIX permissions and rooted filesystem operations; native Windows ACL validation is not implemented (use WSL)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("open home directory: %w", err)
	}
	defer homeRoot.Close()
	info, err := homeRoot.Stat(".")
	if err != nil {
		return nil, err
	}
	if err := checkDirectory(info, false); err != nil {
		return nil, fmt.Errorf("home directory: %w", err)
	}
	root, err := openChildRoot(homeRoot, ".rose", false)
	if err != nil {
		return nil, fmt.Errorf("open Rose key directory: %w", err)
	}
	return root, nil
}

func openQuantumRoot(create bool) (*os.Root, error) {
	rose, err := openRoseRoot()
	if err != nil {
		return nil, err
	}
	defer rose.Close()
	if create {
		if err := rose.Mkdir("quantum_keys", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create quantum key directory: %w", err)
		}
	}
	root, err := openChildRoot(rose, "quantum_keys", true)
	if err != nil {
		return nil, fmt.Errorf("open quantum key directory: %w", err)
	}
	return root, nil
}

func checkPrivateFile(info os.FileInfo, limit int) error {
	if !info.Mode().IsRegular() {
		return errors.New("private key is not a regular file or is a symlink")
	}
	if info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o400 {
		return fmt.Errorf("unsafe private key permissions %04o; require owner-readable 0600 or 0400", info.Mode().Perm())
	}
	if info.Size() < 1 || info.Size() > int64(limit) {
		return fmt.Errorf("private key size must be between 1 and %d bytes", limit)
	}
	return nil
}

func readPrivateFile(root *os.Root, name string, limit int) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkPrivateFile(before, limit); err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("private key changed while opening")
	}
	if err := checkPrivateFile(after, limit); err != nil {
		return nil, err
	}
	encoded, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		clear(encoded)
		return nil, err
	}
	if len(encoded) > limit {
		clear(encoded)
		return nil, errors.New("private key exceeds size limit")
	}
	return encoded, nil
}

func parseQuantumSeed(encoded []byte) (*mldsa.PrivateKey, error) {
	if len(encoded) != quantumSeedFileSize ||
		string(encoded[:len(quantumSeedPrefix)]) != quantumSeedPrefix ||
		encoded[len(encoded)-1] != '\n' {
		return nil, errors.New("unsupported or malformed ML-DSA private seed envelope; legacy OQS keys cannot be imported")
	}
	seed, err := decodeBase64(string(encoded[len(quantumSeedPrefix):len(encoded)-1]), mldsa.PrivateKeySize)
	if err != nil {
		return nil, fmt.Errorf("ML-DSA private seed: %w", err)
	}
	defer clear(seed)
	return mldsa.NewPrivateKey(mldsa.MLDSA87(), seed)
}

func readQuantumKey(root *os.Root) (*mldsa.PrivateKey, error) {
	encoded, err := readPrivateFile(root, quantumKeyFile, quantumSeedFileSize)
	if err != nil {
		return nil, err
	}
	defer clear(encoded)
	return parseQuantumSeed(encoded)
}

func loadQuantumKey() (*mldsa.PrivateKey, error) {
	root, err := openQuantumRoot(false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	key, err := readQuantumKey(root)
	if err != nil {
		return nil, fmt.Errorf("read ML-DSA-87 private seed: %w", err)
	}
	return key, nil
}

// writePrivateFile publishes a fully written, synced private file without
// replacement. A hard link, unlike Rename, fails if the destination exists,
// including a symlink. Unsupported filesystems fail closed; no unsafe fallback.
// The containing directory must already be private and held through os.Root.
// An error after publication (for example, a directory sync failure) can leave
// the complete key installed. Callers must inspect it, never overwrite it.
func writePrivateFile(root *os.Root, name string, encoded []byte) (err error) {
	if name != quantumKeyFile || len(encoded) != quantumSeedFileSize {
		return errors.New("invalid private seed filename or size")
	}
	temp := "." + quantumKeyFile + "." + rand.Text()
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private seed temporary file: %w", err)
	}
	defer func() {
		err = errors.Join(err, root.Remove(temp))
	}()
	writeErr := file.Chmod(0o600)
	if writeErr == nil {
		var n int
		n, writeErr = file.Write(encoded)
		if writeErr == nil && n != len(encoded) {
			writeErr = io.ErrShortWrite
		}
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write private seed temporary file: %w", err)
	}
	if err := root.Link(temp, name); err != nil {
		return fmt.Errorf("publish private seed without replacement: %w", err)
	}
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open private seed directory for sync: %w", err)
	}
	syncErr := dir.Sync()
	closeErr = dir.Close()
	return errors.Join(syncErr, closeErr)
}

// GenerateQuantumKeys provisions ML-DSA-87, or validates and preserves an
// existing key. It never overwrites keys or imports legacy OQS files.
// The existing ~/.rose directory must have safe permissions.
func GenerateQuantumKeys() error {
	return generateQuantumKeypair(AlgMLDSA87)
}

func generateQuantumKeypair(algorithm string) error {
	if err := checkQuantumAlgorithm(algorithm); err != nil {
		return err
	}
	root, err := openQuantumRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := readQuantumKey(root); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("existing ML-DSA-87 private seed is unusable: %w", err)
	}
	key, err := mldsa.GenerateKey(mldsa.MLDSA87())
	if err != nil {
		return fmt.Errorf("generate ML-DSA-87 key: %w", err)
	}
	seed := key.Bytes()
	defer clear(seed)
	encoded := []byte(quantumSeedPrefix + base64.StdEncoding.EncodeToString(seed) + "\n")
	defer clear(encoded)
	// A concurrent generator may win publication. Return its ErrExist conflict,
	// rather than hiding any other publication or temporary-file cleanup error.
	return writePrivateFile(root, quantumKeyFile, encoded)
}

// CheckKeys reports missing, malformed, inaccessible or unsafe required keys.
func CheckKeys() error {
	if _, err := loadSSHSigner(); err != nil {
		return fmt.Errorf("classical key: %w", err)
	}
	if _, err := loadQuantumKey(); err != nil {
		return fmt.Errorf("ML-DSA key: %w", err)
	}
	return nil
}

// IsKeyGenerated is the compatibility boolean form of CheckKeys. All key errors
// return false; use CheckKeys to distinguish missing keys from unsafe ones.
func IsKeyGenerated() bool {
	return CheckKeys() == nil
}

// GenerateKeys validates the externally provisioned classical Ed25519 key before
// provisioning ML-DSA. Missing or malformed classical keys are errors, not success.
func GenerateKeys() error {
	if _, err := loadSSHSigner(); err != nil {
		return fmt.Errorf("provision a valid ~/.rose/id_ed25519 first: %w", err)
	}
	if err := GenerateQuantumKeys(); err != nil {
		return err
	}
	return CheckKeys()
}
