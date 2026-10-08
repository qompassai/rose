package envconfig

import "testing"

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ROSE_MODELS", "")
	t.Setenv("XDG_DATA_HOME", "")
	ReloadServerConfig()
}
