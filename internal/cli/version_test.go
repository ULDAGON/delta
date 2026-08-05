package cli

import (
	"bytes"
	"context"
	"runtime/debug"
	"testing"
)

func TestRunVersionReportsInjectedBuildMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	originalBuildInfoGetter := buildInfoGetter
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
		buildInfoGetter = originalBuildInfoGetter
	})

	Version, Commit, Date = defaultVersion, unknownValue, unknownValue
	buildInfoGetter = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v0.4.2"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc1234"},
				{Key: "vcs.time", Value: "2026-08-02T12:34:56Z"},
			},
		}, true
	}

	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--version"}, nil, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	const want = "delta v0.4.2 (commit abc1234, 2026-08-02T12:34:56Z)\n"
	if got := output.String(); got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRunVersionOmitsUnknownCommitAndDate(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	originalBuildInfoGetter := buildInfoGetter
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
		buildInfoGetter = originalBuildInfoGetter
	})

	Version, Commit, Date = defaultVersion, unknownValue, unknownValue
	buildInfoGetter = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}}, true
	}

	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--version"}, nil, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	const want = "delta v1.0.1\n"
	if got := output.String(); got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRunVersionOmitsOnlyUnknownDate(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	originalBuildInfoGetter := buildInfoGetter
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
		buildInfoGetter = originalBuildInfoGetter
	})

	Version, Commit, Date = defaultVersion, unknownValue, unknownValue
	buildInfoGetter = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main:     debug.Module{Version: "v1.0.1"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234"}},
		}, true
	}

	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--version"}, nil, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	const want = "delta v1.0.1 (commit abc1234)\n"
	if got := output.String(); got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestResolveBuildMetadataFallsBackToGoBuildInfo(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.4.2"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc1234"},
			{Key: "vcs.time", Value: "2026-08-02T12:34:56Z"},
		},
	}

	got := resolveBuildMetadata(defaultVersion, unknownValue, unknownValue, info)
	want := buildMetadata{version: "v0.4.2", commit: "abc1234", date: "2026-08-02T12:34:56Z"}
	if got != want {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}

func TestResolveBuildMetadataPreservesInjectedValues(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.4.2"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "build-info-commit"},
			{Key: "vcs.time", Value: "build-info-date"},
		},
	}

	got := resolveBuildMetadata("0.5.0", "ldflag-commit", "ldflag-date", info)
	want := buildMetadata{version: "0.5.0", commit: "ldflag-commit", date: "ldflag-date"}
	if got != want {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}

func TestResolveBuildMetadataIgnoresDevelopmentModuleVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	got := resolveBuildMetadata(defaultVersion, unknownValue, unknownValue, info)
	want := buildMetadata{version: defaultVersion, commit: unknownValue, date: unknownValue}
	if got != want {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}
