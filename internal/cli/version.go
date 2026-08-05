package cli

import "runtime/debug"

const (
	defaultVersion = "dev"
	unknownValue   = "unknown"
)

// These values are replaced by the release build's ldflags. The build-info
// fallback keeps binaries installed with `go install` just as useful for
// diagnostics as release binaries.
var (
	Version = defaultVersion
	Commit  = unknownValue
	Date    = unknownValue

	buildInfoGetter = debug.ReadBuildInfo
)

func currentBuildMetadata() buildMetadata {
	info, ok := buildInfoGetter()
	if !ok {
		info = nil
	}
	return resolveBuildMetadata(Version, Commit, Date, info)
}

type buildMetadata struct {
	version string
	commit  string
	date    string
}

func resolveBuildMetadata(version, commit, date string, info *debug.BuildInfo) buildMetadata {
	if info == nil {
		return buildMetadata{version: version, commit: commit, date: date}
	}
	if version == "" || version == defaultVersion {
		if buildVersion := info.Main.Version; buildVersion != "" && buildVersion != "(devel)" {
			version = buildVersion
		}
	}
	if commit == "" || commit == unknownValue {
		if buildCommit := buildSetting(info, "vcs.revision"); buildCommit != "" {
			commit = buildCommit
		}
	}
	if date == "" || date == unknownValue {
		if buildDate := buildSetting(info, "vcs.time"); buildDate != "" {
			date = buildDate
		}
	}
	return buildMetadata{version: version, commit: commit, date: date}
}

func buildSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}
