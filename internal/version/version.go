// Package version carries build metadata injected at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set with -ldflags "-X github.com/jtsang4/herdr-share/internal/version.Version=v1.2.3 ...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// ProtocolVersion is the relay wire protocol spoken by this build. A relay
// rejects hosts and guests that speak a different major protocol.
const ProtocolVersion = 1

func init() {
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		Version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "" {
				Commit = s.Value
			}
		case "vcs.time":
			if Date == "" {
				Date = s.Value
			}
		}
	}
}

// String is a one-line human description.
func String() string {
	commit := Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	s := Version
	if commit != "" {
		s += " (" + commit + ")"
	}
	return fmt.Sprintf("%s %s/%s", s, runtime.GOOS, runtime.GOARCH)
}
