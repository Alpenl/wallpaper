package telemetry

import (
	"strings"
	"testing"
)

func TestKnownPrivateValues(t *testing.T) {
	home := "/home/Jane Doe"
	dir := home + "/My Library/My Clip"
	file := dir + "/video.mp4"
	win := `C:\Users\Jane Doe\AppData\Local\WallpaperApp\library\x\a.mp4`
	redact := newRedactor(PrivateValues{Paths: []string{home, home + "/My Library", dir, file, win}, Titles: []string{"My Clip", "Hi", "1234", "!?.,", "123 !?", `C:\Users\Other Person`, "/home/Other Person"}})
	for _, tc := range []struct{ raw, want string }{
		{"[e][ffmpeg] " + file + ": Invalid data found", "[e][ffmpeg] <path:" + HashPath(file) + ">: Invalid data found"},
		{win + ": error", "<path:" + HashPath(win) + ">: error"},
		{strings.ToLower(win) + ": error", "<path:" + HashPath(win) + ">: error"},
		{strings.ReplaceAll(win, `\`, "/") + ": error", "<path:" + HashPath(win) + ">: error"},
		{strings.ReplaceAll(win, `\`, `\\`) + ": error", "<path:" + HashPath(win) + ">: error"},
		{"playing My Clip; Hi 1234 !?., 123 !?", "playing <redacted>; Hi 1234 !?., 123 !?"},
	} {
		if got := redact(tc.raw); got != tc.want {
			t.Errorf("redact(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	for _, raw := range []string{
		"open /unknown/Private Movies/secret.mp4: sensitive tail",
		"open " + home + "/Unknown Movies/secret.mp4: sensitive tail",
		"open " + dir + " extra/secret.mp4: sensitive tail",
		"open /unknown/My Clip/secret.mp4: sensitive tail",
		`open C:\Users\Other Person\secret.mp4: sensitive tail`,
		"open:/home/Other Person/secret.mp4: sensitive tail",
	} {
		got := redact(raw)
		if strings.Contains(got, "secret") || strings.Contains(got, "sensitive") || strings.Contains(got, "My Clip") {
			t.Errorf("unknown path leaked: %q", got)
		}
	}
}

func TestKnownTitleDoesNotRewritePathToken(t *testing.T) {
	p := "/home/Jane Doe/video.mp4"
	got := newRedactor(PrivateValues{Paths: []string{p}, Titles: []string{"path"}})(p + ": path failed")
	if want := "<path:" + HashPath(p) + ">: <redacted> failed"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestKnownTitleCannotHideURLCredentials(t *testing.T) {
	got := newRedactor(PrivateValues{Titles: []string{"https"}})("https://private-user:private-pass@example.com")
	if strings.Contains(got, "private-") || strings.Contains(got, "https") {
		t.Fatalf("credentials/title leaked: %q", got)
	}
}
