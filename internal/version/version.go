// Package version carries the build's version string.
//
// The kiosk has no shell and no way to be interrogated, so the only way anyone
// can tell which build is in front of them is to read it off the screen. The
// issue template asks reporters for it; the discovery screen is where they get
// it.
package version

import (
	"strconv"
	"time"
)

// Version is stamped at build time with:
//
//	-ldflags "-X github.com/diggyen/SimpleClient/internal/version.Version=v1.2.3"
//
// It stays "dev" for a plain `go build`, which is honest: a binary that was not
// built from a tag should not claim to be a release.
var Version = "dev"

// BuildTime is when the source the build came from was committed, in Unix
// seconds, stamped like Version. It stays empty for a plain `go build`.
var BuildTime = ""

// Built is BuildTime as a time, or the zero time when it was not stamped.
func Built() time.Time {
	s, err := strconv.ParseInt(BuildTime, 10, 64)
	if err != nil || s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}
