package store

import (
	"os"
	"path/filepath"
)

func getStorePath() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	return filepath.Join(localAppData, "Rose", "config.json")
}
