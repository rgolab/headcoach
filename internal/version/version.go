// Package version holds the version of headcoach.
package version

import "runtime/debug"

// Version is set at build time for a release: -ldflags "-X .../internal/version.Version=v1.2.3".
// Without it, a `go install ...@v1.2.3` build takes the module version, and a local build says dev.
var Version = "dev"

func init() {
	if Version == "dev" {
		Version = fromModule(debug.ReadBuildInfo())
	}
}

func fromModule(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
