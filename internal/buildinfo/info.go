// Package buildinfo provides the executable identity shared by all surfaces.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

type Info struct {
	Version    string
	Commit     string
	Modified   bool
	CommitTime string
	GoVersion  string
}

func Read(version string) Info {
	b, _ := debug.ReadBuildInfo()
	return fromBuild(version, b)
}

func fromBuild(version string, b *debug.BuildInfo) Info {
	if version == "" {
		version = "dev"
	}
	i := Info{Version: version, Commit: "unknown", CommitTime: "unknown", GoVersion: runtime.Version()}
	if b == nil {
		return i
	}
	if b.GoVersion != "" {
		i.GoVersion = b.GoVersion
	}
	for _, s := range b.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				i.Commit = s.Value
				if len(i.Commit) > 12 {
					i.Commit = i.Commit[:12]
				}
			}
		case "vcs.modified":
			i.Modified = s.Value == "true"
		case "vcs.time":
			if s.Value != "" {
				i.CommitTime = s.Value
			}
		}
	}
	return i
}

func (i Info) Revision() string {
	if i.Modified {
		return i.Commit + "+dirty"
	}
	return i.Commit
}

func (i Info) String() string {
	return fmt.Sprintf("%s commit=%s commit_time=%s go=%s %s/%s", i.Version, i.Revision(), i.CommitTime, i.GoVersion, runtime.GOOS, runtime.GOARCH)
}
