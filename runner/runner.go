package runner

import (
	"github.com/qompassai/rose/runner/llamarunner"
	"github.com/qompassai/rose/runner/roserunner"
)

func Execute(args []string) error {
	if args[0] == "runner" {
		args = args[1:]
	}

	var newRunner bool
	if args[0] == "--rose-engine" {
		args = args[1:]
		newRunner = true
	}

	if newRunner {
		return roserunner.Execute(args)
	} else {
		return llamarunner.Execute(args)
	}
}
