package telemetry

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wpapp/internal/buildinfo"
)

var (
	maxDiagnosticZIP        = 20 << 20
	maxDiagnosticFile int64 = 2 << 20
	maxDiagnosticRead       = 64 << 20
)

const truncationMarker = "[truncated: older content omitted]\n"

type diagnosticEntry struct {
	name string
	data []byte
}
type diagnosticSource struct {
	path, name string
	json       bool
	modified   time.Time
}
type diagnosticManifest struct {
	File   string `json:"file"`
	Status string `json:"status"`
}

// ExportDiagnostics exports sanitized summaries, then newest log tails. Optional
// snapshots must contain only diagnostic fields, never the material library.
func ExportDiagnostics(logsDir string, configFiles []string, dst string, build buildinfo.Info, private PrivateValues, snapshots ...any) error {
	entries, err := diagnosticEntries(logsDir, configFiles, build, snapshots, newRedactor(private))
	if err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	return writeDiagnostics(f, entries)
}

// Own the destination so both archive finalization and destination Close errors
// reach the caller. Keeping this boundary small also permits portable I/O tests.
func writeDiagnostics(dst io.WriteCloser, entries []diagnosticEntry) error {
	z := zip.NewWriter(dst)
	var writeErr error
	for _, e := range entries {
		var w io.Writer
		w, writeErr = z.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if writeErr == nil {
			_, writeErr = w.Write(e.data)
		}
		if writeErr != nil {
			break
		}
	}
	return errors.Join(writeErr, z.Close(), dst.Close())
}

// Store entries have exactly these local header, descriptor and central-directory
// costs (no timestamps/extras); reserve the end-of-central-directory separately.
func entrySize(e diagnosticEntry) int { return len(e.data) + 2*len(e.name) + 30 + 16 + 46 }

func diagnosticEntries(logsDir string, configs []string, build buildinfo.Info, snapshots []any, redact func(string) string) ([]diagnosticEntry, error) {
	entries := []diagnosticEntry{{"README.txt", []byte(redact(fmt.Sprintf("wallpaperd %s\nexported %s\nPaths and private fields redacted. Logs contain bounded tails. See manifest.json for omissions.\n", build.String(), time.Now().Format(time.RFC3339))))}}
	for i, snapshot := range snapshots {
		b, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		b, err = scrubJSONWith(b, redact)
		if err != nil {
			return nil, err
		}
		name := "assignments.json"
		if i > 0 {
			name = fmt.Sprintf("assignments-%d.json", i)
		}
		entries = append(entries, diagnosticEntry{name, b})
	}
	var sources []diagnosticSource
	for _, p := range configs {
		if filepath.Base(p) == "library.json" {
			continue
		}
		sources = append(sources, diagnosticSource{path: p, name: "config/" + filepath.Base(p), json: true})
	}
	ents, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}
	var logs []diagnosticSource
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(strings.HasSuffix(n, ".log") || strings.HasSuffix(n, ".log.1")) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if info.Size() == 0 && strings.HasPrefix(n, "crash-") && strings.HasSuffix(n, ".log") {
			continue
		}
		logs = append(logs, diagnosticSource{path: filepath.Join(logsDir, n), name: "logs/" + n, modified: info.ModTime()})
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].modified.After(logs[j].modified) })
	sources = append(sources, logs...)
	manifest := make([]diagnosticManifest, len(sources))
	for i, s := range sources {
		manifest[i] = diagnosticManifest{redact(s.name), "omitted: size/read budget or unavailable"}
	}
	reserve, _ := json.MarshalIndent(manifest, "", "  ")
	// Reserve ordinary and ZIP64 end records, even for very many tiny files.
	used := 22 + 76 + entrySize(diagnosticEntry{"manifest.json", reserve})
	for _, e := range entries {
		used += entrySize(e)
	}
	if used > maxDiagnosticZIP {
		return nil, fmt.Errorf("diagnostic summaries exceed ZIP budget")
	}
	readBudget := int64(maxDiagnosticRead)
	for i, s := range sources {
		// Reserve enough for a full bounded file before reading the next source.
		if readBudget < maxDiagnosticFile {
			continue
		}
		b, truncated, n, err := readDiagnostic(s, redact)
		readBudget -= n
		if err != nil {
			if os.IsNotExist(err) {
				manifest[i].Status = "omitted: unavailable"
				continue
			}
			// Invalid/oversized JSON must never fall back to exporting private raw text.
			manifest[i].Status = "omitted: unreadable or invalid JSON"
			continue
		}
		e := diagnosticEntry{redact(s.name), b}
		if used+entrySize(e) > maxDiagnosticZIP {
			continue
		}
		used += entrySize(e)
		entries = append(entries, e)
		manifest[i].Status = "included"
		if truncated {
			manifest[i].Status = "included: truncated tail"
		}
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	entries = append(entries, diagnosticEntry{"manifest.json", b})
	return entries, nil
}

func readDiagnostic(s diagnosticSource, redact func(string) string) ([]byte, bool, int64, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, false, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("not a regular file")
	}
	truncated := st.Size() > maxDiagnosticFile
	if s.json && truncated {
		return nil, false, 0, fmt.Errorf("JSON exceeds read budget")
	}
	if truncated {
		if _, err = f.Seek(-maxDiagnosticFile, io.SeekEnd); err != nil {
			return nil, false, 0, err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, maxDiagnosticFile))
	n := int64(len(b))
	if err != nil {
		return nil, truncated, n, err
	}
	if s.json {
		b, err = scrubJSONWith(b, redact)
		return b, false, n, err
	}
	if truncated {
		// Drop the partial first record rather than risk leaking a cut-off path.
		if end := bytes.IndexByte(b, '\n'); end >= 0 {
			b = b[end+1:]
		} else {
			b = nil
		}
	}
	var out bytes.Buffer
	if truncated {
		out.WriteString(truncationMarker)
	}
	name := filepath.Base(s.path)
	plain := strings.HasPrefix(name, "crash-") || strings.Contains(name, "-stderr") || strings.HasPrefix(name, "mfhost-")
	mpv := strings.HasPrefix(name, "mpv-") && !plain
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if mpv && !bytes.Contains(line, []byte("][w][")) && !bytes.Contains(line, []byte("][e][")) && !bytes.Contains(line, []byte("][f][")) {
			continue
		}
		if !plain && !mpv {
			clean, err := scrubJSONWith(line, redact)
			if err != nil {
				out.WriteString("[omitted: invalid JSON record]\n")
				continue
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, clean); err != nil {
				return nil, truncated, n, err
			}
			out.Write(compact.Bytes())
		} else {
			out.WriteString(redact(string(line)))
		}
		out.WriteByte('\n')
	}
	return out.Bytes(), truncated, n, nil
}
