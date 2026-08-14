//go:build !linux

// SimpleClient talks to /dev/fb0 and /dev/input/event* directly, so it only
// builds and runs on Linux. This stub exists so `go build ./...` and
// `go vet ./...` work on a developer's macOS or Windows machine; cross-compile
// with GOOS=linux to produce the real binary.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "simpleclient: only supported on Linux (build with GOOS=linux)")
	os.Exit(1)
}
