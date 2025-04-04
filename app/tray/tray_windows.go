package tray

import (
	"github.com/qompassai/rose/app/tray/commontray"
	"github.com/qompassai/rose/app/tray/wintray"
)

func InitPlatformTray(icon, updateIcon []byte) (commontray.RoseTray, error) {
	return wintray.InitTray(icon, updateIcon)
}
