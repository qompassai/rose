// Package errtypes contains custom error types
package errtypes

import (
	"fmt"
	"strings"
)

const (
	UnknownRoseKeyErrMsg = "unknown rose key"
	InvalidModelNameErrMsg = "invalid model name"
)

// TODO: This should have a structured response from the API
type UnknownRoseKey struct {
	Key string
}

func (e *UnknownRoseKey) Error() string {
	return fmt.Sprintf("unauthorized: %s %q", UnknownRoseKeyErrMsg, strings.TrimSpace(e.Key))
}
