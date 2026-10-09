package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompassai/rose/envconfig"
	"github.com/qompassai/rose/types/model"
)

// ResolutionPin records the outcome of the first successful remote
// resolution of a model name: the registry host that served the manifest
// and the manifest's digest. Pins live in one JSON file at the root of
// the models directory, alongside (not inside) the manifest trees, so
// the manifest walkers never see them.
//
// A pin makes later pulls of the same name deterministic: the manifest
// is fetched by digest from the pinned host instead of by tag, so a tag
// that moved upstream cannot silently change what the name resolves
// to. Re-resolution happens only when the pin is removed.
type ResolutionPin struct {
	Host   string `json:"host"`
	Digest string `json:"digest"`
}

const (
	pinsFileName = "resolution-pins.json"

	// maxPinsFileSize bounds the pins file read; a larger file is
	// treated as corrupt rather than loaded into memory.
	maxPinsFileSize = 4 << 20
	// maxResolutionPins bounds how many pins the store will hold.
	maxResolutionPins = 10000
)

// pinKey identifies a name inside the pins file. The host is deliberately
// absent: union hosts are one identity, so namespace/model:tag is the
// name. Parts are lowercased to match the case-insensitive comparisons
// used everywhere else for names.
func pinKey(n model.Name) string {
	return strings.ToLower(n.Namespace) + "/" + strings.ToLower(n.Model) + ":" + strings.ToLower(n.Tag)
}

func pinsPath() (string, error) {
	dir := envconfig.Models()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("%w: ensure path elements are traversable", err)
	}

	return filepath.Join(dir, pinsFileName), nil
}

func readResolutionPins() (map[string]ResolutionPin, error) {
	path, err := pinsPath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]ResolutionPin{}, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxPinsFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPinsFileSize {
		return nil, fmt.Errorf("resolution pins file %s exceeds %d bytes", path, maxPinsFileSize)
	}

	pins := map[string]ResolutionPin{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &pins); err != nil {
			return nil, fmt.Errorf("parse resolution pins file %s: %w", path, err)
		}
	}

	return pins, nil
}

func writeResolutionPins(pins map[string]ResolutionPin) error {
	if len(pins) > maxResolutionPins {
		return fmt.Errorf("resolution pins exceed the %d pin limit", maxResolutionPins)
	}

	path, err := pinsPath()
	if err != nil {
		return err
	}

	// Marshal from sorted keys so the file is deterministic.
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make(map[string]ResolutionPin, len(pins))
	for _, k := range keys {
		ordered[k] = pins[k]
	}

	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".resolution-pins-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

// ReadResolutionPin returns the pin recorded for n, if any.
func ReadResolutionPin(n model.Name) (ResolutionPin, bool, error) {
	pins, err := readResolutionPins()
	if err != nil {
		return ResolutionPin{}, false, err
	}

	pin, ok := pins[pinKey(n)]
	return pin, ok, nil
}

// WriteResolutionPin records (or replaces) the pin for n.
func WriteResolutionPin(n model.Name, pin ResolutionPin) error {
	pins, err := readResolutionPins()
	if err != nil {
		return err
	}

	pins[pinKey(n)] = pin
	return writeResolutionPins(pins)
}

// DeleteResolutionPin removes the pin for n, if any. Deleting a pin is
// the explicit re-resolution action: the next pull resolves by tag
// again and records a fresh pin.
func DeleteResolutionPin(n model.Name) error {
	pins, err := readResolutionPins()
	if err != nil {
		return err
	}

	key := pinKey(n)
	if _, ok := pins[key]; !ok {
		return nil
	}
	delete(pins, key)
	return writeResolutionPins(pins)
}

// PinnedHostsForNamespace returns the set of registry hosts that hold at
// least one pin in the given namespace. Union resolution uses it to
// decide whether a namespace has only ever resolved on one host.
func PinnedHostsForNamespace(namespace string) (map[string]bool, error) {
	pins, err := readResolutionPins()
	if err != nil {
		return nil, err
	}

	prefix := strings.ToLower(namespace) + "/"
	hosts := map[string]bool{}
	for key, pin := range pins {
		if strings.HasPrefix(key, prefix) {
			hosts[pin.Host] = true
		}
	}

	return hosts, nil
}
