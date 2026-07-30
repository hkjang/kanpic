package buildinfo

import "runtime"

// Values are replaced by release builds through -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type BuildInfo struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

func Current() BuildInfo {
	return BuildInfo{Product: "kanpic", Version: Version, Commit: Commit, BuildTime: BuildTime, GoVersion: runtime.Version()}
}
