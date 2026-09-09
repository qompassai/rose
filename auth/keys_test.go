package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestKeyGenerationNoOverwriteAndPrivateModes(t *testing.T) {
	home := testHome(t)
	writeTestSSHKey(t, home)
	classicalPath := filepath.Join(home, ".rose", defaultPrivateKey)
	classicalBefore, err := os.ReadFile(classicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if IsKeyGenerated() {
		t.Fatal("missing quantum key reported generated")
	}
	if err := GenerateKeys(); err != nil {
		t.Fatal(err)
	}
	quantumPath := filepath.Join(home, ".rose", "quantum_keys", quantumKeyFile)
	before, err := os.ReadFile(quantumPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(quantumPath)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != int64(quantumSeedFileSize) {
		t.Fatalf("unsafe private file: info=%v err=%v", info, err)
	}
	dirInfo, err := os.Stat(filepath.Dir(quantumPath))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("unsafe private directory: info=%v err=%v", dirInfo, err)
	}
	if !strings.HasPrefix(string(before), quantumSeedPrefix) || !IsKeyGenerated() {
		t.Fatal("versioned quantum key not generated correctly")
	}
	if err := GenerateKeys(); err != nil {
		t.Fatal(err)
	}
	if err := GenerateQuantumKeys(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(quantumPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing quantum key overwritten: %v", err)
	}
	afterInfo, err := os.Stat(quantumPath)
	if err != nil || !os.SameFile(info, afterInfo) {
		t.Fatalf("existing quantum key replaced: %v", err)
	}
	classicalAfter, err := os.ReadFile(classicalPath)
	if err != nil || !bytes.Equal(classicalBefore, classicalAfter) {
		t.Fatalf("classical key was overwritten: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(quantumPath))
	if err != nil || len(entries) != 1 || entries[0].Name() != quantumKeyFile {
		t.Fatalf("unexpected sidecars or temporary files: %v, %v", entries, err)
	}
}

func TestGenerateKeysFailsWithoutValidClassicalKey(t *testing.T) {
	home := testHome(t)
	if err := GenerateKeys(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing classical key should be an error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".rose", "quantum_keys")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("quantum side effects occurred before classical validation: %v", err)
	}
	path := filepath.Join(home, ".rose", defaultPrivateKey)
	if err := os.WriteFile(path, []byte("malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := GenerateKeys(); err == nil {
		t.Fatal("malformed classical key reported success")
	}
	if err := CheckKeys(); err == nil || IsKeyGenerated() {
		t.Fatal("bad key state accepted")
	}
}

func TestPrivateKeyFailuresAreNotIgnored(t *testing.T) {
	for _, name := range []string{"missing", "malformed", "oversized", "world-readable", "unreadable", "executable", "directory", "symlink"} {
		t.Run(name, func(t *testing.T) {
			home := testHome(t)
			writeTestSSHKey(t, home)
			path := filepath.Join(home, ".rose", defaultPrivateKey)
			switch name {
			case "missing":
				mustRemoveTestFile(t, path)
			case "malformed":
				mustWriteTestFile(t, path, []byte("invalid key"))
			case "oversized":
				mustWriteTestFile(t, path, bytes.Repeat([]byte{'x'}, maxSSHPrivateKeyBytes+1))
			case "world-readable":
				mustChmodTestFile(t, path, 0o644)
			case "unreadable":
				mustChmodTestFile(t, path, 0o000)
			case "executable":
				mustChmodTestFile(t, path, 0o700)
			case "directory":
				mustRemoveTestFile(t, path)
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(home, "outside")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := GetPublicKey(); err == nil {
				t.Fatal("GetPublicKey ignored unsafe key")
			}
			if _, err := Sign(context.Background(), nil); err == nil {
				t.Fatal("Sign ignored unsafe key")
			}
			if err := GenerateKeys(); err == nil {
				t.Fatal("GenerateKeys ignored unsafe key")
			}
			if IsKeyGenerated() {
				t.Fatal("IsKeyGenerated ignored unsafe key")
			}
		})
	}
}

func mustRemoveTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func mustWriteTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustChmodTestFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestQuantumKeyFailuresDoNotOverwrite(t *testing.T) {
	for _, name := range []string{"malformed", "oversized", "old raw seed", "old algorithm label", "world-readable", "unreadable", "directory", "symlink"} {
		t.Run(name, func(t *testing.T) {
			home := testHome(t)
			writeTestSSHKey(t, home)
			if err := GenerateKeys(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, ".rose", "quantum_keys", quantumKeyFile)
			switch name {
			case "malformed":
				mustWriteTestFile(t, path, []byte("garbage"))
			case "oversized":
				mustWriteTestFile(t, path, bytes.Repeat([]byte{'x'}, quantumSeedFileSize+1))
			case "old raw seed":
				mustWriteTestFile(t, path, bytes.Repeat([]byte{1}, 32))
			case "old algorithm label":
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				mustWriteTestFile(t, path, bytes.Replace(data, []byte(AlgMLDSA87), []byte("DILITHIUM"), 1))
			case "world-readable":
				mustChmodTestFile(t, path, 0o644)
			case "unreadable":
				mustChmodTestFile(t, path, 0o000)
			case "directory":
				mustRemoveTestFile(t, path)
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(home, "outside-seed")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := GetQuantumPublicKey(""); err == nil {
				t.Fatal("unsafe quantum key was accepted")
			}
			if _, err := SignHybrid(context.Background(), nil); err == nil {
				t.Fatal("unsafe quantum key was ignored by hybrid signing")
			}
			if err := GenerateQuantumKeys(); err == nil {
				t.Fatal("unsafe existing quantum key was regenerated")
			}
			if err := CheckKeys(); err == nil || IsKeyGenerated() {
				t.Fatal("unsafe quantum key reported valid")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() {
				t.Fatalf("unsafe existing quantum key was modified: %v", err)
			}
		})
	}
}

func TestLegacyKeyFilesAreNeverReinterpreted(t *testing.T) {
	home := testHome(t)
	writeTestSSHKey(t, home)
	dir := filepath.Join(home, ".rose", "quantum_keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := bytes.Repeat([]byte{0xab}, 64)
	for _, name := range []string{AlgDilithium3, AlgDilithium5, AlgFalcon1024} {
		mustWriteTestFile(t, filepath.Join(dir, name), old)
	}
	if _, err := GetQuantumPublicKey(""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy file reused instead of missing new-format key: %v", err)
	}
	if err := GenerateQuantumKeys(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{AlgDilithium3, AlgDilithium5, AlgFalcon1024} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, old) {
			t.Fatalf("legacy key modified: %s %v", name, err)
		}
	}
}

func TestUnsafeDirectoriesAreRejected(t *testing.T) {
	for _, name := range []string{"home-writable", "rose-writable", "rose-symlink", "quantum-readable", "quantum-unwritable", "quantum-file", "quantum-symlink"} {
		t.Run(name, func(t *testing.T) {
			home := testHome(t)
			writeTestSSHKey(t, home)
			rose := filepath.Join(home, ".rose")
			dir := filepath.Join(rose, "quantum_keys")
			switch name {
			case "home-writable":
				mustChmodTestFile(t, home, 0o777)
			case "rose-writable":
				mustChmodTestFile(t, rose, 0o777)
			case "rose-symlink":
				target := filepath.Join(home, "other")
				if err := os.Rename(rose, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, rose); err != nil {
					t.Fatal(err)
				}
			case "quantum-readable", "quantum-unwritable":
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0o755)
				if name == "quantum-unwritable" {
					mode = 0o500
				}
				mustChmodTestFile(t, dir, mode)
			case "quantum-file":
				mustWriteTestFile(t, dir, []byte("not a directory"))
			case "quantum-symlink":
				target := filepath.Join(home, "outside")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := GenerateQuantumKeys(); err == nil {
				t.Fatal("unsafe quantum directory accepted")
			}
		})
	}
}

func TestAtomicPrivatePublicationDoesNotReplace(t *testing.T) {
	home := testHome(t)
	writeTestSSHKey(t, home)
	if err := GenerateQuantumKeys(); err != nil {
		t.Fatal(err)
	}
	root, err := openQuantumRoot(false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := root.ReadFile(quantumKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(root, quantumKeyFile, bytes.Repeat([]byte{'x'}, quantumSeedFileSize)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("atomic no-replace error = %v", err)
	}
	after, err := root.ReadFile(quantumKeyFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("atomic publication replaced the key: %v", err)
	}
	for _, data := range [][]byte{nil, bytes.Repeat([]byte{1}, quantumSeedFileSize+1)} {
		if err := writePrivateFile(root, quantumKeyFile, data); err == nil {
			t.Fatal("unbounded private seed write accepted")
		}
	}
	if err := writePrivateFile(root, "../escape", before); err == nil {
		t.Fatal("unsafe private seed filename accepted")
	}
}

func TestConcurrentGenerationDoesNotOverwrite(t *testing.T) {
	testHome(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- GenerateQuantumKeys() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, os.ErrExist) {
			t.Fatalf("unexpected concurrent generation failure: %v", err)
		}
	}
	if _, err := GetQuantumPublicKey(""); err != nil {
		t.Fatalf("concurrent generation left no valid key: %v", err)
	}
}

func TestReadOnlyPrivateKeyIsUsable(t *testing.T) {
	home := testHome(t)
	writeTestSSHKey(t, home)
	if err := GenerateKeys(); err != nil {
		t.Fatal(err)
	}
	mustChmodTestFile(t, filepath.Join(home, ".rose", defaultPrivateKey), 0o400)
	mustChmodTestFile(t, filepath.Join(home, ".rose", "quantum_keys", quantumKeyFile), 0o400)
	if err := GenerateKeys(); err != nil {
		t.Fatalf("valid read-only keys should be preserved and usable: %v", err)
	}
}

func TestUnsupportedStoragePlatformIsExplicit(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		if _, err := openRoseRoot(); err == nil || !strings.Contains(err.Error(), "requires POSIX") {
			t.Fatalf("unsafe storage platform was not explicitly rejected: %v", err)
		}
	default:
		t.Skip("platform supports POSIX private key storage")
	}
}
