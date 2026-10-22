//go:build linux

package linux

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jezek/xgb/xproto"

	"wpapp/internal/desktop"
)

type foregroundAtoms struct {
	active, pid xproto.Atom
}

func (h *Host) initForeground() {
	// XWayland cannot report native Wayland applications. Advertising support
	// there would leave rules applied to the wrong application when focus moves.
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return
	}
	if _, err := os.Readlink("/proc/self/exe"); err != nil {
		return
	}
	h.foregroundAtoms = foregroundAtoms{active: h.atom("_NET_ACTIVE_WINDOW"), pid: h.atom("_NET_WM_PID")}
	supported := h.atom("_NET_SUPPORTED")
	if h.foregroundAtoms.active == 0 || h.foregroundAtoms.pid == 0 || supported == 0 {
		return
	}
	r, err := xproto.GetProperty(h.c, false, h.scr.Root, supported, xproto.AtomAtom, 0, 4096).Reply()
	if err != nil || r == nil || r.Type != xproto.AtomAtom || r.Format != 32 {
		return
	}
	if !containsAtom(r.Value, h.foregroundAtoms.active) {
		return
	}
	h.foregroundHostname = hostname
	h.foregroundSupported = true
}

func containsAtom(data []byte, atom xproto.Atom) bool {
	for len(data) >= 4 {
		if xproto.Atom(binary.LittleEndian.Uint32(data[:4])) == atom {
			return true
		}
		data = data[4:]
	}
	return false
}

func (h *Host) foregroundExecutable() string {
	window := xproto.Window(h.cardinal(h.scr.Root, h.foregroundAtoms.active, xproto.AtomWindow))
	if window == 0 {
		return ""
	}
	pid := h.cardinal(window, h.foregroundAtoms.pid, xproto.AtomCardinal)
	if pid == 0 {
		return ""
	}
	// X clients can live on other machines. Never interpret a remote client's
	// PID as a local /proc PID; EWMH requires WM_CLIENT_MACHINE alongside PID.
	r, err := xproto.GetProperty(h.c, false, window, xproto.AtomWmClientMachine, xproto.AtomString, 0, 256).Reply()
	if err != nil || r == nil || r.Type != xproto.AtomString || r.Format != 8 || r.BytesAfter != 0 ||
		!sameMachine(string(r.Value), h.foregroundHostname) {
		return ""
	}
	return processExecutable("/proc", pid)
}

func (h *Host) cardinal(window xproto.Window, property, typ xproto.Atom) uint32 {
	r, err := xproto.GetProperty(h.c, false, window, property, typ, 0, 1).Reply()
	if err != nil || r == nil || r.Type != typ || r.Format != 32 || r.ValueLen != 1 || len(r.Value) != 4 || r.BytesAfter != 0 {
		return 0
	}
	return binary.LittleEndian.Uint32(r.Value)
}

func sameMachine(client, local string) bool {
	client = strings.TrimSuffix(strings.TrimRight(client, "\x00"), ".")
	local = strings.TrimSuffix(local, ".")
	return client != "" && local != "" && strings.EqualFold(client, local)
}

func processExecutable(procDir string, pid uint32) string {
	if pid == 0 {
		return ""
	}
	path, err := os.Readlink(filepath.Join(procDir, strconv.FormatUint(uint64(pid), 10), "exe"))
	if err != nil {
		return ""
	}
	// The kernel suffix describes an unlinked executable, not its filename.
	return desktop.ExecutableName(strings.TrimSuffix(path, " (deleted)"))
}
