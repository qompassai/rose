//go:build !windows

package tray

import (
	"errors"

	"github.com/qompassai/rose/app/tray/commontray"
)

func InitPlatformTray(icon, updateIcon []byte) (commontray.RoseTray, error) {
	return nil, errors.New("not implemented")
}
