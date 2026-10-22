package shader

import (
	"fmt"
	"strings"
)

type macro struct {
	params   []string
	body     []token
	function bool
}

// preprocess is bounded and intentionally rejects includes, conditional
// directives, token pasting and stringification instead of invoking a compiler
// preprocessor with filesystem access to untrusted package directives.
func preprocess(src string) (string, error) {
	var err error
	src, err = stripComments(src)
	if err != nil {
		return "", err
	}
	macros := map[string]macro{}
	var out strings.Builder
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			d := strings.TrimSpace(trimmed[1:])
			if !strings.HasPrefix(d, "define ") && !strings.HasPrefix(d, "define\t") {
				return "", fmt.Errorf("shader line %d: unsupported preprocessor directive", i+1)
			}
			d = strings.TrimSpace(d[6:])
			ts, e := lex(d)
			if e != nil {
				return "", fmt.Errorf("shader line %d: invalid macro", i+1)
			}
			if len(ts) < 3 {
				return "", fmt.Errorf("shader line %d: empty macro", i+1)
			}
			name := ts[0].text
			m := macro{}
			start := 1
			if strings.HasPrefix(d, name+"(") {
				m.function = true
				start = 2
				for ts[start].text != ")" {
					if ts[start].text == "" {
						return "", fmt.Errorf("shader line %d: unclosed macro", i+1)
					}
					m.params = append(m.params, ts[start].text)
					start++
					if ts[start].text == "," {
						start++
					}
				}
				start++
			}
			m.body = ts[start : len(ts)-1]
			macros[name] = m
			out.WriteByte('\n')
			continue
		}
		ts, e := lex(line)
		if e != nil {
			return "", fmt.Errorf("shader line %d: %w", i+1, e)
		}
		ts = ts[:len(ts)-1]
		for depth := 0; depth < 32; depth++ {
			changed := false
			var next []token
			for j := 0; j < len(ts); j++ {
				m, ok := macros[ts[j].text]
				if !ok {
					next = append(next, ts[j])
					continue
				}
				changed = true
				args := map[string][]token{}
				if m.function {
					if j+1 >= len(ts) || ts[j+1].text != "(" {
						return "", fmt.Errorf("shader line %d: macro needs arguments", i+1)
					}
					j += 2
					level := 0
					start := j
					var aa [][]token
					for ; j < len(ts); j++ {
						s := ts[j].text
						if s == ")" && level == 0 {
							aa = append(aa, ts[start:j])
							break
						}
						if s == "," && level == 0 {
							aa = append(aa, ts[start:j])
							start = j + 1
						}
						if s == "(" {
							level++
						}
						if s == ")" {
							level--
						}
					}
					if len(m.params) == 0 && len(aa) == 1 && len(aa[0]) == 0 {
						aa = nil
					}
					if j == len(ts) || len(aa) != len(m.params) {
						return "", fmt.Errorf("shader line %d: invalid macro arguments", i+1)
					}
					for k, n := range m.params {
						args[n] = aa[k]
					}
				}
				for _, t := range m.body {
					if a, ok := args[t.text]; ok {
						next = append(next, a...)
					} else {
						next = append(next, t)
					}
				}
			}
			ts = next
			if len(ts) > 100000 {
				return "", fmt.Errorf("shader line %d: macro expansion limit", i+1)
			}
			if !changed {
				break
			}
			if depth == 31 {
				return "", fmt.Errorf("shader line %d: recursive macro", i+1)
			}
		}
		for _, t := range ts {
			if out.Len()+len(t.text) > 2<<20 {
				return "", fmt.Errorf("shader line %d: expanded source exceeds 2 MiB", i+1)
			}
			out.WriteString(t.text)
			out.WriteByte(' ')
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func stripComments(src string) (string, error) {
	b := []byte(src)
	line := 1
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			line++
		}
		if i+1 >= len(b) || b[i] != '/' {
			continue
		}
		if b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
			if i < len(b) {
				line++
			}
			continue
		}
		if b[i+1] == '*' {
			start := line
			b[i] = ' '
			b[i+1] = ' '
			i += 2
			closed := false
			for i+1 < len(b) {
				if b[i] == '*' && b[i+1] == '/' {
					b[i] = ' '
					b[i+1] = ' '
					i++
					closed = true
					break
				}
				if b[i] == '\n' {
					line++
				} else {
					b[i] = ' '
				}
				i++
			}
			if !closed {
				return "", fmt.Errorf("shader line %d: unclosed comment", start)
			}
		}
	}
	return string(b), nil
}
