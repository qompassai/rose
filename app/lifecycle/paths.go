package lifecycle

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	AppName    = "rose app"
	CLIName    = "rose"
	AppDir     = "/opt/Rose"
	AppDataDir = "/opt/Rose"
	// TODO - should there be a distinct log dir?
	UpdateStageDir   = "/tmp"
	AppLogFile       = "/tmp/rose_app.log"
	ServerLogFile    = "/tmp/rose.log"
	UpgradeLogFile   = "/tmp/rose_update.log"
	Installer        = "RoseSetup.exe"
	LogRotationCount = 5
)

func init() {
	if runtime.GOOS == "windows" {
		AppName += ".exe"
		CLIName += ".exe"
		// Logs, configs, downloads go to LOCALAPPDATA
		localAppData := os.Getenv("LOCALAPPDATA")
		AppDataDir = filepath.Join(localAppData, "Rose")
		UpdateStageDir = filepath.Join(AppDataDir, "updates")
		AppLogFile = filepath.Join(AppDataDir, "app.log")
		ServerLogFile = filepath.Join(AppDataDir, "server.log")
		UpgradeLogFile = filepath.Join(AppDataDir, "upgrade.log")

		exe, err := os.Executable()
		if err != nil {
			slog.Warn("error discovering executable directory", "error", err)
			AppDir = filepath.Join(localAppData, "Programs", "Rose")
		} else {
			AppDir = filepath.Dir(exe)
		}

		// Make sure we have PATH set correctly for any spawned children
		paths := strings.Split(os.Getenv("PATH"), ";")
		// Start with whatever we find in the PATH/LD_LIBRARY_PATH
		found := false
		for _, path := range paths {
			d, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			if strings.EqualFold(AppDir, d) {
				found = true
			}
		}
		if !found {
			paths = append(paths, AppDir)

			pathVal := strings.Join(paths, ";")
			slog.Debug("setting PATH=" + pathVal)
			err := os.Setenv("PATH", pathVal)
			if err != nil {
				slog.Error(fmt.Sprintf("failed to update PATH: %s", err))
			}
		}

		// Make sure our logging dir exists
		_, err = os.Stat(AppDataDir)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(AppDataDir, 0o755); err != nil {
				slog.Error(fmt.Sprintf("create rose dir %s: %v", AppDataDir, err))
			}
		}
	} else if runtime.GOOS == "darwin" {
		// TODO
		AppName += ".app"
		// } else if runtime.GOOS == "linux" {
		// TODO
	}
}
