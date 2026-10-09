package manifest

import (
	"testing"

	"github.com/qompassai/rose/types/model"
)

func TestResolutionPinRoundTrip(t *testing.T) {
	t.Setenv("ROSE_MODELS", t.TempDir())

	n := model.ParseName("library/model:latest")

	if _, ok, err := ReadResolutionPin(n); err != nil || ok {
		t.Fatalf("fresh store: ok=%v err=%v, want no pin", ok, err)
	}

	want := ResolutionPin{Host: "registry.ollama.ai", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := WriteResolutionPin(n, want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := ReadResolutionPin(n)
	if err != nil || !ok {
		t.Fatalf("after write: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("pin = %+v, want %+v", got, want)
	}

	// The pin key ignores the host spelling: the same name under a
	// union host finds the same pin.
	alt := model.ParseName("ollama.com/library/model:latest")
	if _, ok, err := ReadResolutionPin(alt); err != nil || !ok {
		t.Fatalf("union spelling: ok=%v err=%v, want the same pin", ok, err)
	}

	hosts, err := PinnedHostsForNamespace("library")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || !hosts["registry.ollama.ai"] {
		t.Fatalf("pinned hosts = %v, want only registry.ollama.ai", hosts)
	}

	if err := DeleteResolutionPin(n); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadResolutionPin(n); err != nil || ok {
		t.Fatalf("after delete: ok=%v err=%v, want no pin", ok, err)
	}
}
