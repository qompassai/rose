package middleware

import (
	"testing"

	"github.com/qompassai/rose/envconfig"
)

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	envconfig.ReloadServerConfig()
}

// enableCloudForTest sets HOME to a clean temp dir and clears ROSE_NO_CLOUD
// so that cloud features are enabled for the duration of the test.
func enableCloudForTest(t *testing.T) {
	t.Helper()
	t.Setenv("ROSE_NO_CLOUD", "")
	setTestHome(t, t.TempDir())
}
