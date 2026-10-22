package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildIdentity(t *testing.T) {
	i := fromBuild("1.2.3", &debug.BuildInfo{GoVersion: "go1.27.3", Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-10-22T00:00:00Z"},
	}})
	if i.Version != "1.2.3" || i.Commit != "0123456789ab" || !i.Modified || i.CommitTime != "2026-10-22T00:00:00Z" || i.GoVersion != "go1.27.3" {
		t.Fatal(i)
	}
	for _, part := range []string{"1.2.3", "commit=0123456789ab+dirty", "commit_time=2026-10-22T00:00:00Z", "go=go1.27.3"} {
		if !strings.Contains(i.String(), part) {
			t.Fatal(i.String())
		}
	}
}

func TestUnknownBuildIdentity(t *testing.T) {
	i := fromBuild("", nil)
	if i.Version != "dev" || i.Commit != "unknown" || i.CommitTime != "unknown" || i.Modified || i.GoVersion == "" {
		t.Fatal(i)
	}
	if got := Read("dev"); got.Version != "dev" || got.GoVersion == "" {
		t.Fatal(got)
	}
}
