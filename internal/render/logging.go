package render

import (
	"os"
	"wpapp/internal/buildinfo"
)

// LogFunc is the lifecycle-only diagnostic sink supplied by the parent.
type LogFunc func(string, ...any)

func (f LogFunc) Log(message string, fields ...any) {
	if f != nil {
		f(message, fields...)
	}
}

// HelperBuild is initialized by the executable before dispatching helper modes.
var HelperBuild = buildinfo.Read("")

type HelperIdentity struct {
	PID   int            `json:"pid"`
	Build buildinfo.Info `json:"build"`
}

func Identity() HelperIdentity { return HelperIdentity{os.Getpid(), HelperBuild} }
