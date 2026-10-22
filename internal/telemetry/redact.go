package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Include spaces: plain-text diagnostics have no reliable end-of-path delimiter.
// Conservatively redact through the next quote or line boundary.
var pathRe = regexp.MustCompile(`(?i)([a-z]:[\\/]|\\\\|~/|/(?:home|users|tmp|var|mnt|media|private|volumes|library|opt|root)/)[^\r\n"'<>]*`)
var unixPathRe = regexp.MustCompile(`(^|[\s"'=(])(/[^\r\n"'<>]+)`)
var ownHomeRe = func() *regexp.Regexp {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	// Home directories need not live under /Users or /home.
	home = strings.ReplaceAll(home, `\`, "/")
	return regexp.MustCompile(regexp.QuoteMeta(home) + `(?:[\\/][^\r\n"'<>]*|$|[\s"'<>])`)
}()

var pathTokenRe = regexp.MustCompile(`<path:[0-9a-f]{8}>`)

var credentialRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)

// PrivateValues is an export-only dictionary; it is never serialized.
type PrivateValues struct {
	Paths  []string
	Titles []string
}

type privateMatch struct {
	value, replacement string
	path               bool
}

// Compile once per export. Case folding also covers Windows paths in logs
// collected on another OS; over-redacting a case variant is privacy-safe.
func newRedactor(private PrivateValues) func(string) string {
	var matches []privateMatch
	seen := make(map[string]bool)
	add := func(value, replacement string, path bool) {
		if value == "" || seen[strings.ToLower(value)] {
			return
		}
		seen[strings.ToLower(value)] = true
		matches = append(matches, privateMatch{value, replacement, path})
	}
	for _, p := range private.Paths {
		for _, form := range []string{p, strings.ReplaceAll(p, `\`, "/")} {
			add(form, "<path:"+HashPath(p)+">", true)
			escaped, _ := json.Marshal(form)
			add(string(escaped[1:len(escaped)-1]), "<path:"+HashPath(p)+">", true)
		}
	}
	const titleToken = "<redacted>"
	for _, title := range private.Titles {
		if utf8.RuneCountInString(title) < 4 {
			continue
		}
		meaningful := false
		for _, r := range title {
			if r > 127 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				meaningful = true
				break
			}
		}
		if meaningful {
			add(title, titleToken, false)
		}
	}
	if len(matches) == 0 {
		return Redact
	}
	sort.SliceStable(matches, func(i, j int) bool { return len(matches[i].value) > len(matches[j].value) })
	patterns := make([]string, len(matches))
	for i, m := range matches {
		patterns[i] = regexp.QuoteMeta(m.value)
	}
	re := regexp.MustCompile("(?i)" + strings.Join(patterns, "|"))
	return func(s string) string {
		// A title can equal a URL scheme; remove credentials before a known
		// replacement can hide that scheme from credentialRe.
		if strings.Contains(s, "://") {
			s = credentialRe.ReplaceAllString(s, `${1}<redacted>@`)
		}
		// Replace paths first so a complete known path releases its error suffix
		// from the conservative fallback before titles are considered.
		for _, paths := range []bool{true, false} {
			var unknown [][]int
			if !paths {
				unknown = pathTokenRe.FindAllStringIndex(s, -1)
			}
			if !paths && strings.ContainsAny(s, `/\`) {
				for _, pattern := range []*regexp.Regexp{ownHomeRe, pathRe, unixPathRe} {
					if pattern != nil {
						unknown = append(unknown, pattern.FindAllStringIndex(s, -1)...)
					}
				}
			}
			var out strings.Builder
			last := 0
			for _, loc := range re.FindAllStringIndex(s, -1) {
				for _, m := range matches {
					if m.path != paths || !strings.EqualFold(s[loc[0]:loc[1]], m.value) {
						continue
					}
					// A known prefix with an ambiguous tail remains an unknown path.
					if m.path && loc[1] < len(s) && !strings.ContainsRune(":\r\n\"'<>", rune(s[loc[1]])) {
						break
					}
					overlaps := false
					for _, span := range unknown {
						if loc[0] < span[1] && loc[1] > span[0] {
							overlaps = true
							break
						}
					}
					// A title may itself contain a path prefix. Do not let replacing it
					// hide an unknown path from the unchanged generic fallback.
					if overlaps {
						break
					}
					out.WriteString(s[last:loc[0]])
					out.WriteString(m.replacement)
					last = loc[1]
					break
				}
			}
			out.WriteString(s[last:])
			s = out.String()
		}
		// Every generic path/credential pattern requires a slash. Avoid running
		// those regexes on ordinary log lines (notably bounded crash tails).
		if strings.ContainsAny(s, `/\`) {
			s = Redact(s)
		}
		return s
	}
}

// Redact scrubs text without first shortening home paths to an unrecognizable form.
func Redact(s string) string {
	s = credentialRe.ReplaceAllString(s, `${1}<redacted>@`)
	if ownHomeRe != nil {
		s = ownHomeRe.ReplaceAllStringFunc(s, func(p string) string { return "<path:" + HashPath(p) + ">" })
	}
	s = pathRe.ReplaceAllStringFunc(s, func(p string) string { return "<path:" + HashPath(p) + ">" })
	return unixPathRe.ReplaceAllStringFunc(s, func(p string) string {
		start := strings.IndexByte(p, '/')
		return p[:start] + "<path:" + HashPath(p[start:]) + ">"
	})
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(k))
	for _, word := range []string{"password", "passwd", "secret", "token", "credential", "authorization", "apikey"} {
		if strings.Contains(k, word) {
			return true
		}
	}
	switch k {
	case "title", "name", "file", "path", "dir", "snapshot", "entry", "username", "cookie", "cookies":
		return true
	}
	return strings.HasSuffix(k, "path") || strings.HasSuffix(k, "directory")
}

func scrubValue(v any, redact func(string) string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitiveKey(k) {
				out[redact(k)] = "<redacted>"
			} else {
				out[redact(k)] = scrubValue(val, redact)
			}
		}
		return out
	case []any:
		for i := range x {
			x[i] = scrubValue(x[i], redact)
		}
		return x
	case string:
		return redact(x)
	default:
		return v
	}
}

func scrubJSON(b []byte) ([]byte, error) {
	return scrubJSONWith(b, Redact)
}

func scrubJSONWith(b []byte, redact func(string) string) ([]byte, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// Unmarshal also rejects trailing garbage/multiple documents.
	if !json.Valid(b) {
		return nil, fmt.Errorf("invalid JSON document")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(scrubValue(v, redact))
	return out.Bytes(), err
}
