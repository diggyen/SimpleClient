// Package version carries the build's version string.
//
// The kiosk has no shell and no way to be interrogated, so the only way anyone
// can tell which build is in front of them is to read it off the screen. The
// issue template asks reporters for it; the discovery screen is where they get
// it.
package version

// Version is stamped at build time with:
//
//	-ldflags "-X github.com/diggyen/SimpleClient/internal/version.Version=v1.2.3"
//
// It stays "dev" for a plain `go build`, which is honest: a binary that was not
// built from a tag should not claim to be a release.
var Version = "dev"
