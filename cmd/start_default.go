//go:build !windows && !darwin

package cmd

import (
	"context"
	"errors"

	"github.com/qompassai/rose/api"
)

func startApp(ctx context.Context, client *api.Client) error {
	return errors.New("could not connect to rose server, run 'rose serve' to start it")
}
