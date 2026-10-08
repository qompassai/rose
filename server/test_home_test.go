package server

import (
	"testing"

	"github.com/qompassai/rose/envconfig"
)

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ROSE_MODELS", "")
	envconfig.ReloadServerConfig()
}
