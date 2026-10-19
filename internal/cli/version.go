package cli

import "runtime/debug"

// Set by GoReleaser through -ldflags.
var (
	ver    = ""
	commit = "none"
	date   = "unknown"
)

// version is the release version, or the module version for go install
// builds.
func version() string {
	if ver != "" {
		return ver
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
