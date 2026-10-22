package web

import (
	"encoding/json"
	"time"
	"wpapp/internal/ipc"
)

func (r *Renderer) BlockSignals(blocked bool) error { return r.call("signals-blocked", blocked) }
func (h *hostSession) closeSignals() {
	if h.signals != nil {
		h.signals.Close()
	}
}
func (h *hostSession) signalFrame(now time.Time) {
	if !h.loaded || h.signals == nil || h.paused || h.blocked || !h.limiter.Ready(now, h.fps) {
		return
	}
	spectrum, _ := h.signals.Frame(now, h.fps, h.raster)
	// These are numeric JSON literals, never executable package strings. No
	// page-to-native bridge is installed; page scripts cannot enable acquisition.
	if h.signals.Audio {
		b, e := json.Marshal(spectrum.Bins)
		if e == nil {
			_ = h.view.Script("if(window.wpk&&typeof window.wpk.onAudio==='function')window.wpk.onAudio(new Float32Array(" + string(b) + "));")
		}
	}
	if p := h.signals.InputFrame; p != nil {
		b, e := json.Marshal(p)
		if e == nil {
			_ = h.view.Script("if(window.wpk&&typeof window.wpk.onInput==='function')window.wpk.onInput(" + string(b) + ");")
		}
	}
}

func (h *hostSession) reportSignalStatus(c *ipc.Conn) {
	if h.signals != nil && h.signals.Status != h.reportedStatus {
		h.reportedStatus = h.signals.Status
		_ = c.Emit("signal-status", h.reportedStatus)
	}
}
