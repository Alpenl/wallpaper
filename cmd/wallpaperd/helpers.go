package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"wpapp/internal/config"
	"wpapp/internal/importer"
	"wpapp/internal/media"
	"wpapp/internal/render/shader"
	"wpapp/internal/telemetry"
	"wpapp/internal/wpk"
)

const (
	maxShaderHelperBytes  = 1 << 20
	maxHelperStdoutBytes  = 1 << 20
	maxPaletteStdoutBytes = 64 << 10
	maxHelperStderrBytes  = 16 << 10
	helperTimeout         = 15 * time.Second
)

func dispatchHelper(f *flags, stdout, stderr io.Writer) (int, bool) {
	if f.helper == "" {
		return 0, false
	}
	switch f.helper {
	case "shader-preview":
		if err := shader.PreviewMain(f.in, f.out); err != nil {
			fmt.Fprintln(stderr, err)
			return 2, true
		}
		return 0, true
	case "shader-validate":
		return runShaderValidateHelper(f.in, stdout, stderr), true
	case "palette":
		return runPaletteHelper(f.in, stdout, stderr), true
	case "sceneprobe":
		return runSceneProbeHelper(f.in), true
	case "probe":
		return runProbeHelper(f.in, stdout, stderr), true
	case "transcode":
		return runTranscodeHelper(f), true
	default:
		fmt.Fprintln(stderr, "unknown helper operation")
		return 2, true
	}
}

func runProbeHelper(in string, stdout, stderr io.Writer) int {
	debug.SetMemoryLimit(128 << 20)
	info, err := media.ProbeFile(in)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	if err := json.NewEncoder(stdout).Encode(info); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	return 0
}

func runShaderValidateHelper(in string, stdout, stderr io.Writer) int {
	if in == "" {
		fmt.Fprintln(stderr, "shader-validate: input path is required")
		return 2
	}
	st, err := os.Stat(in)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	if !st.Mode().IsRegular() {
		fmt.Fprintln(stderr, "shader line 1: source must be a regular file")
		return 3
	}
	if st.Size() > maxShaderHelperBytes {
		fmt.Fprintln(stderr, "shader line 1: source exceeds 1 MiB limit")
		return 3
	}
	f, err := os.Open(in)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	b, readErr := io.ReadAll(io.LimitReader(f, maxShaderHelperBytes+1))
	_ = f.Close()
	if readErr != nil {
		fmt.Fprintln(stderr, readErr)
		return 3
	}
	if len(b) > maxShaderHelperBytes {
		fmt.Fprintln(stderr, "shader line 1: source exceeds 1 MiB limit")
		return 3
	}
	defs := findStagedShaderProperties(in)
	for _, target := range []shader.Target{shader.HLSL, shader.MSL} {
		if _, err := shader.TranslateProperties(string(b), target, defs); err != nil {
			fmt.Fprintln(stderr, err)
			return 3
		}
	}
	if defs == nil {
		defs = []wpk.PropertyDef{}
	}
	if err := json.NewEncoder(stdout).Encode(defs); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	return 0
}

// findStagedShaderProperties checks whether srcPath is the entry file of a
// staged shader WPK directory so manifest-declared uniform properties are
// included during isolated validation.
func findStagedShaderProperties(srcPath string) []wpk.PropertyDef {
	absIn, err := filepath.Abs(srcPath)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(absIn)
	for i := 0; i < 3; i++ {
		if m, err := wpk.LoadDir(dir); err == nil && m.Type == "shader" {
			entry := filepath.Clean(filepath.Join(dir, filepath.FromSlash(m.Entry)))
			if entry == absIn {
				return m.Properties
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func runPaletteHelper(in string, stdout, stderr io.Writer) int {
	if in == "" {
		fmt.Fprintln(stderr, "palette: input path is required")
		return 2
	}
	colors := importer.PreviewColors(in)
	if colors == nil {
		colors = []string{}
	}
	if err := json.NewEncoder(stdout).Encode(colors); err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	return 0
}

// runBoundedHelper runs a short-lived helper subprocess with strict stdout and
// stderr size budgets; overflowing either stream kills the child immediately.
func runBoundedHelper(cmd *exec.Cmd, maxStdout, maxStderr int) ([]byte, []byte, error) {
	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	out := &probeOutput{limit: maxStdout, kill: kill}
	errBuf := &probeOutput{limit: maxStderr, kill: kill}
	cmd.Stdout = out
	cmd.Stderr = errBuf
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if out.overflow || errBuf.overflow {
		return nil, errBuf.buffer.Bytes(), errors.New("helper output exceeded budget")
	}
	return out.buffer.Bytes(), errBuf.buffer.Bytes(), err
}

func (e *env) validateShader(srcPath string) ([]wpk.PropertyDef, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.exe, "-helper", "shader-validate", "-in", srcPath)
	hideConsole(cmd)
	out, stderr, err := runBoundedHelper(cmd, maxHelperStdoutBytes, maxHelperStderrBytes)
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("shader-validate helper: %w", err)
	}
	var defs []wpk.PropertyDef
	if err := json.Unmarshal(out, &defs); err != nil {
		return nil, fmt.Errorf("shader-validate decode: %w", err)
	}
	return defs, nil
}

func (e *env) previewColors(path string) []string {
	if path == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.exe, "-helper", "palette", "-in", path)
	hideConsole(cmd)
	out, _, err := runBoundedHelper(cmd, maxPaletteStdoutBytes, maxHelperStderrBytes)
	if err != nil {
		return nil
	}
	var colors []string
	if err := json.Unmarshal(out, &colors); err != nil || len(colors) == 0 {
		return nil
	}
	return colors
}

func readJSONReadOnly(path string, v any) bool {
	for _, candidate := range []string{path, path + ".bak"} {
		b, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, v); err == nil {
			return true
		}
	}
	return false
}

// exportDiagnosticsReadOnly exports a redacted diagnostics archive without
// creating a desktop host, running migrations, or mutating state.json.
func exportDiagnosticsReadOnly(paths config.Paths) (string, error) {
	dst := filepath.Join(filepath.Dir(paths.Logs), "diagnostics-"+time.Now().Format("20060102-150405")+".zip")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return dst, err
	}
	cfgs := []string{
		filepath.Join(paths.Config, "config.json"),
		filepath.Join(paths.Config, "state.json"),
	}
	statusPath := filepath.Join(paths.Logs, "logger-status.json")
	if _, err := os.Stat(statusPath); err == nil {
		cfgs = append(cfgs, statusPath)
	}
	cfg := config.Defaults()
	readJSONReadOnly(filepath.Join(paths.Config, "config.json"), &cfg)
	var lib config.Library
	readJSONReadOnly(filepath.Join(paths.Config, "library.json"), &lib)
	var assign config.Assignments
	readJSONReadOnly(filepath.Join(paths.Config, "assignments.json"), &assign)

	home, _ := os.UserHomeDir()
	private := telemetry.PrivateValues{
		Paths: []string{home, paths.Config, paths.Library, paths.Logs, paths.Cache},
	}
	if cfg.Render.MpvPath != "" {
		private.Paths = append(private.Paths, cfg.Render.MpvPath)
	}
	types := make(map[string]string, len(lib.Items))
	for _, item := range lib.Items {
		types[item.ID] = item.Type
		private.Paths = append(private.Paths, item.Dir, filepath.Join(item.Dir, item.Entry))
		private.Titles = append(private.Titles, item.Title)
	}

	assignments := make([]map[string]any, 0, len(assign.Displays))
	displayIDs := make([]string, 0, len(assign.Displays))
	for id := range assign.Displays {
		displayIDs = append(displayIDs, id)
	}
	sort.Strings(displayIDs)
	for _, id := range displayIDs {
		itemID := assign.Displays[id]
		kind := types[itemID]
		if kind == "" {
			kind = "unknown"
		}
		assignments = append(assignments, map[string]any{
			"display": id,
			"type":    kind,
			"backend": "unknown",
			"options": assign.OptionsFor(id),
		})
	}
	return dst, telemetry.ExportDiagnostics(paths.Logs, cfgs, dst, build, private, assignments)
}
