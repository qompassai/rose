package main

import (
	"fmt"
	"os"

	"github.com/qompassai/rose/runner"
)

func main() {
	if err := runner.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}
