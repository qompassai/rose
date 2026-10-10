package certhelper

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readBounded reads a stored credential file with the transport
// loader's size bound.
func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPEMBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	if len(data) > maxPEMBytes {
		return nil, fmt.Errorf("file %q exceeds %d bytes", path, maxPEMBytes)
	}
	return data, nil
}

// writeExclusive creates path with exactly the given permissions and
// fails rather than overwriting anything that already exists.
func writeExclusive(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	if err := writeAndClose(file, data); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// writeReplace atomically replaces path via a staged sibling file,
// used only by CA rotation after the old CA has been archived. The
// staged file is created exclusively, so a leftover stage file fails
// the rotation instead of being silently reused.
func writeReplace(path string, data []byte, perm os.FileMode) error {
	stage := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".new")
	file, err := os.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("stage %q: %w", path, err)
	}
	if err := writeAndClose(file, data); err != nil {
		_ = os.Remove(stage)
		return err
	}
	if err := os.Rename(stage, path); err != nil {
		_ = os.Remove(stage)
		return fmt.Errorf("replace %q: %w", path, err)
	}
	return nil
}

func writeAndClose(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %q: %w", file.Name(), err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync %q: %w", file.Name(), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %q: %w", file.Name(), err)
	}
	return nil
}
