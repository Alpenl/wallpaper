package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
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

var credentialRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)

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

func scrubValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitiveKey(k) {
				out[Redact(k)] = "<redacted>"
			} else {
				out[Redact(k)] = scrubValue(val)
			}
		}
		return out
	case []any:
		for i := range x {
			x[i] = scrubValue(x[i])
		}
		return x
	case string:
		return Redact(x)
	default:
		return v
	}
}

func scrubJSON(b []byte) ([]byte, error) {
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
	err := enc.Encode(scrubValue(v))
	return out.Bytes(), err
}
