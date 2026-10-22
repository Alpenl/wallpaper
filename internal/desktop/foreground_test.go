package desktop

import "testing"

func TestExecutableName(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {".", ""}, {"..", ""}, {"/", ""},
		{"/usr/bin/firefox", "firefox"},
		{`C:\Program Files\App\Game.EXE`, "Game.EXE"},
		{"/Applications/My App.app/Contents/MacOS/My App", "My App"},
		{" my app ", " my app "}, {"bad\x00name", ""},
	} {
		if got := ExecutableName(tc.input); got != tc.want {
			t.Errorf("ExecutableName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestForegroundTrackerIdentityAndUnavailable(t *testing.T) {
	var f ForegroundTracker
	var events []Event
	emit := func(e Event) bool { events = append(events, e); return true }
	for _, name := range []string{"", "", "/bin/player", "/opt/player", "editor", "", ""} {
		f.Update(name, "test-probe", emit)
	}
	want := []string{"", "player", "editor", ""}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for i, e := range events {
		if e.Kind != EventForegroundApp || e.Executable != want[i] || e.Display != "" || e.Source != "test-probe" {
			t.Errorf("event %d = %+v", i, e)
		}
	}
}

func TestForegroundTrackerRetriesRejectedEvent(t *testing.T) {
	var f ForegroundTracker
	accept := func(Event) bool { return true }
	reject := func(Event) bool { return false }
	if f.Update("player", "test", reject) || !f.Update("player", "test", accept) {
		t.Fatal("initial event must retry after queue rejection")
	}
	if f.Update("", "test", reject) || !f.Update("", "test", accept) {
		t.Fatal("unavailable event must retry after queue rejection")
	}
	if f.Update("", "test", accept) {
		t.Fatal("same accepted identity must not be emitted twice")
	}
}
