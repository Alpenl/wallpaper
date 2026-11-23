package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Match Windows drive paths only when not preceded by a URL scheme character
// (so https://, file://, socks5:// are never misparsed as drive letters), and
// disallow "://" after the drive colon.
var winDriveRe = regexp.MustCompile(`(?i)(^|[^a-z0-9+.-])([a-z]:(?:\\|/(?:[^/\r\n"'<>]|$))[^\r\n"'<>]*)`)

// Include spaces: plain-text diagnostics have no reliable end-of-path delimiter.
// Require a non-host/path-segment boundary so URL paths like https://host/users/x
// are not corrupted while /home/..., ~/..., and \\server\... are still redacted.
var pathRe = regexp.MustCompile(`(?i)(^|[^a-z0-9._~-])((?:\\\\|~/|/(?:home|users|tmp|var|mnt|media|private|volumes|library|opt|root)/)[^\r\n"'<>]*)`)
var unixPathRe = regexp.MustCompile(`(^|[\s"'=(])(/[^\r\n"'<>]+)`)

type cachedDirRe struct {
	mu  sync.RWMutex
	dir string
	re  *regexp.Regexp
}

func (c *cachedDirRe) get(dir string) *regexp.Regexp {
	c.mu.RLock()
	if c.dir == dir {
		re := c.re
		c.mu.RUnlock()
		return re
	}
	c.mu.RUnlock()
	re := buildDirPattern(dir, runtime.GOOS == "windows")
	c.mu.Lock()
	c.dir = dir
	c.re = re
	c.mu.Unlock()
	return re
}

var (
	ownHomeCache   cachedDirRe
	wpappHomeCache cachedDirRe
)

func buildDirPattern(dir string, caseInsensitive bool) *regexp.Regexp {
	dir = strings.TrimRight(strings.ReplaceAll(dir, `\`, "/"), "/")
	if dir == "" || (len(dir) == 2 && dir[1] == ':') {
		return nil
	}
	parts := strings.Split(dir, "/")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	prefix := ""
	if caseInsensitive || (len(dir) >= 2 && dir[1] == ':') {
		prefix = "(?i)"
	}
	body := strings.Join(parts, `(?:\\{1,2}|/)`)
	return regexp.MustCompile(prefix + `(` + body + `(?:(?:\\{1,2}|/)[^\r\n"'<>]*)?)($|[\s"'<>])`)
}

func homePatterns() []*regexp.Regexp {
	var out []*regexp.Regexp
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if re := ownHomeCache.get(home); re != nil {
			out = append(out, re)
		}
	}
	if wph := os.Getenv("WPAPP_HOME"); wph != "" {
		if re := wpappHomeCache.get(wph); re != nil {
			out = append(out, re)
		}
	}
	return out
}

var pathTokenRe = regexp.MustCompile(`<path:[0-9a-f]{8}>|<redacted>`)

var credentialRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@"'<>]+@`)
var urlQuerySecretRe = regexp.MustCompile(`(?i)([?&](?:[a-z0-9._-]*(?:token|key|secret|pass(?:word|wd)?|auth|sig(?:nature)?|credential|session|jwt|bearer|cookie)[a-z0-9._-]*)=)([^&#\s"'<>]+)`)
var bearerRe = regexp.MustCompile(`(?i)\b(Bearer\s+)([^\s"',;<>]+)`)

func redactURLAndTokens(s string) string {
	if strings.Contains(s, "://") {
		s = credentialRe.ReplaceAllString(s, `${1}<redacted>@`)
	}
	if strings.ContainsAny(s, "?&") && strings.Contains(s, "=") {
		s = urlQuerySecretRe.ReplaceAllString(s, `${1}<redacted>`)
	}
	if strings.Contains(strings.ToLower(s), "bearer") {
		s = bearerRe.ReplaceAllString(s, `${1}<redacted>`)
	}
	return s
}

func replaceSubmatchPath(re *regexp.Regexp, s string, prefixGroup, pathGroup, suffixGroup int) string {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var out strings.Builder
	last := 0
	for _, loc := range locs {
		out.WriteString(s[last:loc[0]])
		if prefixGroup > 0 {
			out.WriteString(s[loc[2*prefixGroup]:loc[2*prefixGroup+1]])
		}
		p := s[loc[2*pathGroup]:loc[2*pathGroup+1]]
		out.WriteString("<path:")
		out.WriteString(HashPath(p))
		out.WriteByte('>')
		if suffixGroup > 0 {
			out.WriteString(s[loc[2*suffixGroup]:loc[2*suffixGroup+1]])
		}
		last = loc[1]
	}
	out.WriteString(s[last:])
	return out.String()
}

func submatchSpans(re *regexp.Regexp, s string, group int) [][]int {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([][]int, 0, len(locs))
	for _, loc := range locs {
		out = append(out, []int{loc[2*group], loc[2*group+1]})
	}
	return out
}

// PrivateValues is an export-only dictionary; it is never serialized.
type PrivateValues struct {
	Paths  []string
	Titles []string
}

type privateMatch struct {
	value, replacement string
	path               bool
}

// newKeyRedactor redacts paths, URLs, and credentials without applying free-text
// Titles so fixed JSON schema keys (e.g. "config", "logs") are never corrupted.
func newKeyRedactor(private PrivateValues) func(string) string {
	base := newRedactor(PrivateValues{Paths: private.Paths})
	return func(s string) string {
		if !strings.ContainsAny(s, `/\:?&`) && !strings.Contains(strings.ToLower(s), "bearer") {
			return s
		}
		return base(s)
	}
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
		// Remove URL credentials, sensitive query values, and Bearer tokens
		// before a known title replacement can hide them.
		s = redactURLAndTokens(s)
		// Replace paths first so a complete known path releases its error suffix
		// from the conservative fallback before titles are considered.
		for _, paths := range []bool{true, false} {
			var unknown [][]int
			if !paths {
				unknown = pathTokenRe.FindAllStringIndex(s, -1)
			}
			if !paths && strings.ContainsAny(s, `/\`) {
				for _, pattern := range homePatterns() {
					unknown = append(unknown, submatchSpans(pattern, s, 1)...)
				}
				unknown = append(unknown, submatchSpans(winDriveRe, s, 2)...)
				unknown = append(unknown, submatchSpans(pathRe, s, 2)...)
				unknown = append(unknown, submatchSpans(unixPathRe, s, 2)...)
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
		// Generic path patterns require a slash. URL/token scrubbing already ran above.
		if strings.ContainsAny(s, `/\`) {
			s = Redact(s)
		}
		return s
	}
}

// Redact scrubs text without first shortening home paths to an unrecognizable form.
func Redact(s string) string {
	s = redactURLAndTokens(s)
	for _, re := range homePatterns() {
		s = replaceSubmatchPath(re, s, 0, 1, 2)
	}
	s = replaceSubmatchPath(winDriveRe, s, 1, 2, 0)
	s = replaceSubmatchPath(pathRe, s, 1, 2, 0)
	return replaceSubmatchPath(unixPathRe, s, 1, 2, 0)
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

func scrubValue(v any, redact, redactKey func(string) string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			nk := redactKey(k)
			if sensitiveKey(k) {
				out[nk] = "<redacted>"
			} else {
				out[nk] = scrubValue(val, redact, redactKey)
			}
		}
		return out
	case []any:
		for i := range x {
			x[i] = scrubValue(x[i], redact, redactKey)
		}
		return x
	case string:
		return redact(x)
	default:
		return v
	}
}

func scrubJSON(b []byte) ([]byte, error) {
	return scrubJSONWithKey(b, Redact, Redact)
}

func scrubJSONWith(b []byte, redact func(string) string) ([]byte, error) {
	return scrubJSONWithKey(b, redact, Redact)
}

func scrubJSONWithKey(b []byte, redact, redactKey func(string) string) ([]byte, error) {
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
	err := enc.Encode(scrubValue(v, redact, redactKey))
	return out.Bytes(), err
}
