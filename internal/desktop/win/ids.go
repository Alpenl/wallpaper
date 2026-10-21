package win

// 显示器身份解析（与平台无关的纯逻辑，便于在任何平台上单元测试）。
//
// 稳定 ID 的来源优先级：
//  1. DisplayConfig（QueryDisplayConfig + DISPLAYCONFIG_DEVICE_INFO_GET_TARGET_NAME）的 monitorDevicePath；
//  2. EnumDisplayDevices(显示器, EDD_GET_DEVICE_INTERFACE_NAME) 的 DeviceID；
//  3. IDesktopWallpaper::GetMonitorDevicePathAt（按矩形匹配）；
//  4. 最后才用 GDI 名 \\.\DISPLAYn（不稳定，只作兜底）。
// 前三者是同一种 device interface path（\\?\DISPLAY#<EDID 厂商型号>#<实例>#{e6f07b5f-…}），比较时不区分大小写。
//
// 友好名优先级：monitorFriendlyDeviceName（EDID 里的型号名）→ 显示器 DeviceString → 空（UI 显示“显示器 N”）。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"wpapp/internal/desktop"
)

// dcTarget 是 DisplayConfig 里一个活动路径的目标信息，按源的 GDI 名索引。
type dcTarget struct {
	Friendly string // monitorFriendlyDeviceName
	Path     string // monitorDevicePath
}

// monIdentity 是解析一块显示器身份所需的全部原始输入。
type monIdentity struct {
	GDI       string // \\.\DISPLAYn
	DC        *dcTarget
	EDDPath   string // EnumDisplayDevices 的 DeviceID（interface name）
	EDDString string // EnumDisplayDevices 的 DeviceString
	WallPath  string // IDesktopWallpaper 的 device path
}

func hashID(prefix, s string) desktop.DisplayID {
	sum := sha256.Sum256([]byte(s))
	return desktop.DisplayID(prefix + hex.EncodeToString(sum[:6]))
}

func normPath(p string) string { return strings.ToLower(strings.TrimSpace(p)) }

// isInterfacePath 判断是不是 device interface path（排除空串和 GDI 名）。
func isInterfacePath(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, `\\?\`) && len(p) > 8
}

// resolve 返回稳定 ID、友好名（可能为空）和旧版 ID（用于迁移）。
func (m monIdentity) resolve() (id desktop.DisplayID, name string, legacy []desktop.DisplayID) {
	key := ""
	for _, p := range []string{dcPath(m.DC), m.EDDPath, m.WallPath} {
		if isInterfacePath(p) {
			key = normPath(p)
			break
		}
	}
	if key != "" {
		id = hashID("w-", key)
	} else {
		id = hashID("w-gdi-", strings.ToUpper(strings.TrimSpace(m.GDI)))
	}
	if m.DC != nil {
		name = desktop.CleanName(m.DC.Friendly)
	}
	if name == "" {
		name = desktop.CleanName(m.EDDString)
	}
	// 0.2.0 及之前：hash(IDesktopWallpaper 原始路径)，拿不到时 hash(GDI 名)，前缀 "w-"。
	if m.WallPath != "" {
		legacy = append(legacy, hashID("w-", m.WallPath))
	}
	if m.GDI != "" {
		legacy = append(legacy, hashID("w-", m.GDI))
	}
	var out []desktop.DisplayID
	for _, l := range legacy {
		if l != id {
			out = append(out, l)
		}
	}
	return id, name, out
}

func dcPath(t *dcTarget) string {
	if t == nil {
		return ""
	}
	return t.Path
}
