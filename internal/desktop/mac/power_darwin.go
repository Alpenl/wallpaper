//go:build darwin

package mac

import (
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"wpapp/internal/desktop"
	"wpapp/internal/platform/macos"
	"wpapp/internal/visibility"
)

var (
	iopsCopyInfo         func() uintptr
	iopsProvidingType    func(uintptr) uintptr
	cfStringGetCString   func(uintptr, *byte, int, uint32) bool
	cfRelease            func(uintptr)
	iokitLoaded, iokitOK bool
)

func loadIOKit() bool {
	if iokitLoaded {
		return iokitOK
	}
	iokitLoaded = true
	io, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return false
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return false
	}
	purego.RegisterLibFunc(&iopsCopyInfo, io, "IOPSCopyPowerSourcesInfo")
	purego.RegisterLibFunc(&iopsProvidingType, io, "IOPSGetProvidingPowerSourceType")
	purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	iokitOK = true
	return true
}

// onBatteryIOKit 用 IOPSGetProvidingPowerSourceType 判断是否电池供电（无子进程，可 1 s 轮询，满足 F-05 ≤ 1 s）。
func onBatteryIOKit() (bool, bool) {
	if !loadIOKit() {
		return false, false
	}
	blob := iopsCopyInfo()
	if blob == 0 {
		return false, false
	}
	defer cfRelease(blob)
	s := iopsProvidingType(blob)
	if s == 0 {
		return false, false
	}
	var buf [64]byte
	if !cfStringGetCString(s, &buf[0], len(buf), 0x08000100) {
		return false, false
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n]) == "Battery Power", true
}

// powerLoop：1 s 采样 AC/电池（IOKit）、低电量模式、thermalState（NSProcessInfo，属性线程安全）。
// 未在真机验证【推断】。
func (h *Host) powerLoop() {
	pi := objc.ID(macos.Class("NSProcessInfo")).Send(macos.Sel("processInfo"))
	var lastSaver, lastSer, lastCrit, lastBatt, first = false, false, false, false, true
	lastThermal := -1
	for {
		low := false
		th := 0
		if pi != 0 {
			low = objc.Send[bool](pi, macos.Sel("isLowPowerModeEnabled"))
			th = int(objc.Send[int64](pi, macos.Sel("thermalState")))
		}
		saver, ser, crit := PowerSignals(low, th)
		emit := func(r visibility.Reason, on bool, last *bool) {
			if first || on != *last {
				*last = on
				h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: r, Active: on, Source: "NSProcessInfo"})
			}
		}
		emit(visibility.BatterySaver, saver, &lastSaver)
		emit(visibility.ThermalSerious, ser, &lastSer)
		emit(visibility.ThermalCritical, crit, &lastCrit)
		if th != lastThermal {
			lastThermal = th
			h.mu.Lock()
			h.thermal = th
			h.lowPower = low
			h.mu.Unlock()
		}
		if b, ok := onBatteryIOKit(); ok && (first || b != lastBatt) {
			lastBatt = b
			h.emit(desktop.Event{Kind: desktop.EventSignal, Reason: visibility.Battery, Active: b, Source: "IOKit"})
		}
		first = false
		select {
		case <-h.stop:
			return
		case <-time.After(time.Second):
		}
	}
}

// Thermal 返回最近一次的 thermalState 与低电量模式（UI 诊断用）。
func (h *Host) Thermal() (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.thermal, h.lowPower
}
