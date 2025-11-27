package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/qompassai/rose/api"
)

func startApp(ctx context.Context, client *api.Client) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	link, err := os.Readlink(exe)
	if err != nil {
		return err
	}
	if !strings.Contains(link, "Rose.app") {
		return errors.New("could not find rose app")
	}
	path := strings.Split(link, "Rose.app")
	if err := exec.Command("/usr/bin/open", "-a", path[0]+"Rose.app").Run(); err != nil {
		return err
	}
	return waitForServer(ctx, client)
}
