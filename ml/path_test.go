package ml

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindLibOllamaPath(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name   string
		search libOllamaPathSearch
		dirs   []string
		want   string
	}{
		{
			name: "darwin release layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "darwin-app", "Rose.app", "Contents", "Resources", "rose"),
				goos:       "darwin",
				goarch:     "arm64",
			},
			dirs: []string{filepath.Join(root, "darwin-app", "Rose.app", "Contents", "Resources")},
			want: filepath.Join(root, "darwin-app", "Rose.app", "Contents", "Resources"),
		},
		{
			name: "darwin standard install layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "darwin-install", "bin", "rose"),
				goos:       "darwin",
				goarch:     "arm64",
			},
			dirs: []string{filepath.Join(root, "darwin-install", "lib", "rose")},
			want: filepath.Join(root, "darwin-install", "lib", "rose"),
		},
		{
			name: "windows release layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "windows-release", "rose.exe"),
				goos:       "windows",
				goarch:     "amd64",
			},
			dirs: []string{filepath.Join(root, "windows-release", "lib", "rose")},
			want: filepath.Join(root, "windows-release", "lib", "rose"),
		},
		{
			name: "windows standard install layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "windows-install", "bin", "rose.exe"),
				goos:       "windows",
				goarch:     "amd64",
			},
			dirs: []string{filepath.Join(root, "windows-install", "lib", "rose")},
			want: filepath.Join(root, "windows-install", "lib", "rose"),
		},
		{
			name: "linux standard install layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "linux-install", "bin", "rose"),
				goos:       "linux",
				goarch:     "amd64",
			},
			dirs: []string{filepath.Join(root, "linux-install", "lib", "rose")},
			want: filepath.Join(root, "linux-install", "lib", "rose"),
		},
		{
			name: "local linux underscore dist layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "linux-dev", "rose"),
				workingDir: filepath.Join(root, "linux-dev"),
				goos:       "linux",
				goarch:     "amd64",
			},
			dirs: []string{filepath.Join(root, "linux-dev", "dist", "linux_amd64", "lib", "rose")},
			want: filepath.Join(root, "linux-dev", "dist", "linux_amd64", "lib", "rose"),
		},
		{
			name: "mlx-only standard install layout",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "mlx-install", "bin", "rose"),
				goos:       "linux",
				goarch:     "amd64",
			},
			dirs: []string{filepath.Join(root, "mlx-install", "lib", "rose")},
			want: filepath.Join(root, "mlx-install", "lib", "rose"),
		},
		{
			name: "darwin local build layout before executable directory fallback",
			search: libOllamaPathSearch{
				executable: filepath.Join(root, "darwin-dev", "rose"),
				workingDir: filepath.Join(root, "darwin-dev"),
				goos:       "darwin",
				goarch:     "arm64",
			},
			dirs: []string{filepath.Join(root, "darwin-dev", "build", "lib", "rose")},
			want: filepath.Join(root, "darwin-dev", "build", "lib", "rose"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, dir := range tt.dirs {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			got := findLibOllamaPath(tt.search)
			if got != tt.want {
				t.Fatalf("findLibOllamaPath() = %q, want %q; candidates: %v", got, tt.want, libOllamaPathCandidates(tt.search))
			}
		})
	}
}
