//go:build darwin

package mac

import (
	"github.com/ebitengine/purego/objc"

	"wpapp/internal/platform/macos"
)

// Both helpers run on the AppKit main thread. NSWorkspace supplies process
// identity without Accessibility or Screen Recording permission and without
// reading window titles or localized application names.
func foregroundAvailable() bool {
	ws := macos.Class("NSWorkspace").Send(macos.Sel("sharedWorkspace"))
	appClass := macos.Class("NSRunningApplication")
	return ws != 0 && appClass != 0 &&
		objc.Send[bool](ws, macos.Sel("respondsToSelector:"), macos.Sel("frontmostApplication")) &&
		objc.Send[bool](appClass, macos.Sel("instancesRespondToSelector:"), macos.Sel("executableURL"))
}

func foregroundExecutable() string {
	ws := macos.Class("NSWorkspace").Send(macos.Sel("sharedWorkspace"))
	if ws == 0 {
		return ""
	}
	app := ws.Send(macos.Sel("frontmostApplication"))
	if app == 0 {
		return ""
	}
	url := app.Send(macos.Sel("executableURL"))
	if url == 0 {
		return ""
	}
	return macos.GoString(url.Send(macos.Sel("lastPathComponent")))
}
