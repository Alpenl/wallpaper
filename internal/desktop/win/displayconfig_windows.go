//go:build windows

package win

import (
	"syscall"
	"unsafe"
)

// DisplayConfig / EnumDisplayDevices 的结构体，布局核对自 wingdi.h（大小在注释里，有 init 断言）。

var (
	pGetDisplayConfigBufferSizes = user32.NewProc("GetDisplayConfigBufferSizes")
	pQueryDisplayConfig          = user32.NewProc("QueryDisplayConfig")
	pDisplayConfigGetDeviceInfo  = user32.NewProc("DisplayConfigGetDeviceInfo")
	pEnumDisplayDevicesW         = user32.NewProc("EnumDisplayDevicesW")
)

const (
	qdcOnlyActivePaths  = 0x2
	dcGetSourceName     = 1
	dcGetTargetName     = 2
	eddGetDeviceIfName  = 0x1
	errInsufficientBuff = 122
)

type luid struct {
	Low  uint32
	High int32
}

// DISPLAYCONFIG_PATH_INFO（72 字节）
type dcPathInfo struct {
	SrcAdapter luid
	SrcID      uint32
	SrcModeIdx uint32
	SrcFlags   uint32
	TgtAdapter luid
	TgtID      uint32
	TgtModeIdx uint32
	OutputTech uint32
	Rotation   uint32
	Scaling    uint32
	RefreshNum uint32
	RefreshDen uint32
	ScanLine   uint32
	TgtAvail   int32
	TgtFlags   uint32
	Flags      uint32
}

// DISPLAYCONFIG_MODE_INFO（64 字节），这里不解析内容。
type dcModeInfo [64]byte

// DISPLAYCONFIG_DEVICE_INFO_HEADER（20 字节）
type dcHeader struct {
	Type    uint32
	Size    uint32
	Adapter luid
	ID      uint32
}

// DISPLAYCONFIG_SOURCE_DEVICE_NAME（84 字节）
type dcSourceName struct {
	H       dcHeader
	GDIName [32]uint16
}

// DISPLAYCONFIG_TARGET_DEVICE_NAME（420 字节）
type dcTargetName struct {
	H            dcHeader
	Flags        uint32
	OutputTech   uint32
	EdidMfg      uint16
	EdidProduct  uint16
	ConnectorIdx uint32
	Friendly     [64]uint16
	Path         [128]uint16
}

// DISPLAY_DEVICEW（840 字节）
type displayDevice struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

func init() {
	for _, c := range []struct {
		got, want uintptr
	}{
		{unsafe.Sizeof(dcPathInfo{}), 72}, {unsafe.Sizeof(dcModeInfo{}), 64}, {unsafe.Sizeof(dcHeader{}), 20},
		{unsafe.Sizeof(dcSourceName{}), 84}, {unsafe.Sizeof(dcTargetName{}), 420}, {unsafe.Sizeof(displayDevice{}), 840},
	} {
		if c.got != c.want {
			panic("win: displayconfig struct size mismatch")
		}
	}
}

// queryDisplayTargets 返回 GDI 源名（\\.\DISPLAYn，大写）→ 第一个活动目标的友好名和设备路径。
// 克隆（镜像）模式下一个源有多个目标，取第一个。失败时返回空表（调用方逐级回退）。
func queryDisplayTargets() map[string]*dcTarget {
	out := map[string]*dcTarget{}
	var paths []dcPathInfo
	var modes []dcModeInfo
	for try := 0; try < 3; try++ {
		var np, nm uint32
		if r, _, _ := pGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 || np == 0 {
			return out
		}
		paths = make([]dcPathInfo, np)
		modes = make([]dcModeInfo, max(nm, 1))
		r, _, _ := pQueryDisplayConfig.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
			uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0)
		if r == errInsufficientBuff {
			continue // 两次调用之间拓扑变了
		}
		if r != 0 {
			return out
		}
		paths = paths[:np]
		break
	}
	for _, p := range paths {
		src := dcSourceName{H: dcHeader{Type: dcGetSourceName, Size: uint32(unsafe.Sizeof(dcSourceName{})), Adapter: p.SrcAdapter, ID: p.SrcID}}
		if r, _, _ := pDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&src))); r != 0 {
			continue
		}
		gdi := upper(syscall.UTF16ToString(src.GDIName[:]))
		if _, ok := out[gdi]; ok {
			continue
		}
		tg := dcTargetName{H: dcHeader{Type: dcGetTargetName, Size: uint32(unsafe.Sizeof(dcTargetName{})), Adapter: p.TgtAdapter, ID: p.TgtID}}
		if r, _, _ := pDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&tg))); r != 0 {
			continue
		}
		out[gdi] = &dcTarget{Friendly: syscall.UTF16ToString(tg.Friendly[:]), Path: syscall.UTF16ToString(tg.Path[:])}
	}
	return out
}

// enumMonitorDevice 返回 GDI 源下第一个显示器的 interface path 和 DeviceString。
func enumMonitorDevice(gdi string) (path, str string) {
	for i := uint32(0); i < 4; i++ {
		dd := displayDevice{Cb: uint32(unsafe.Sizeof(displayDevice{}))}
		if r, _, _ := pEnumDisplayDevicesW.Call(up(u16(gdi)), uintptr(i), uintptr(unsafe.Pointer(&dd)), eddGetDeviceIfName); r == 0 {
			return
		}
		const active = 0x1 // DISPLAY_DEVICE_ACTIVE
		p, s := syscall.UTF16ToString(dd.DeviceID[:]), syscall.UTF16ToString(dd.DeviceString[:])
		if path == "" {
			path, str = p, s
		}
		if dd.StateFlags&active != 0 {
			return p, s
		}
	}
	return
}
