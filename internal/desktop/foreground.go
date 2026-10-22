package desktop

import "strings"

// ExecutableName removes either platform's path separators without changing
// filename case. Hosts must not expose or log foreground application paths.
func ExecutableName(path string) string {
	name := path[strings.LastIndexAny(path, `/\\`)+1:]
	if name == "." || name == ".." || strings.ContainsRune(name, 0) {
		return ""
	}
	return name
}

// ForegroundTracker publishes the first sample and subsequent identity changes.
// Keep one tracker per host probe; Update is not intended for concurrent calls.
// The identity is committed only when emit accepts the event so a temporarily
// full event queue cannot permanently leave an application rule stuck active.
type ForegroundTracker struct {
	initialized bool
	executable  string
}

func (f *ForegroundTracker) Update(executable, source string, emit func(Event) bool) bool {
	name := ExecutableName(executable)
	if f.initialized && f.executable == name {
		return false
	}
	if !emit(Event{Kind: EventForegroundApp, Executable: name, Source: source}) {
		return false
	}
	f.initialized, f.executable = true, name
	return true
}
