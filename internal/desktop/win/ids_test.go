package win

import (
	"testing"

	"wpapp/internal/desktop"
)

const pathA = `\\?\DISPLAY#DEL40A3#5&2a1b3c4&0&UID4352#{e6f07b5f-ee97-4a90-b076-33f57bf4eaa7}`
const pathB = `\\?\DISPLAY#DEL40A3#5&2a1b3c4&0&UID4353#{e6f07b5f-ee97-4a90-b076-33f57bf4eaa7}`

func TestResolvePrefersDevicePathOverGDI(t *testing.T) {
	// 同一块屏：重启后 GDI 名从 DISPLAY1 变成 DISPLAY2，三个来源大小写不同，ID 必须不变。
	a, _, _ := monIdentity{GDI: `\\.\DISPLAY1`, DC: &dcTarget{Friendly: "DELL U2720Q", Path: pathA}}.resolve()
	b, _, _ := monIdentity{GDI: `\\.\DISPLAY2`, EDDPath: toUpperASCII(pathA)}.resolve()
	c, _, _ := monIdentity{GDI: `\\.\DISPLAY3`, WallPath: pathA}.resolve()
	if a != b || b != c {
		t.Fatalf("同一设备路径应得到同一 ID: %s %s %s", a, b, c)
	}
	// 两块同型号的屏（实例号不同）ID 不同
	d, _, _ := monIdentity{GDI: `\\.\DISPLAY1`, DC: &dcTarget{Path: pathB}}.resolve()
	if d == a {
		t.Fatal("不同实例应不同 ID")
	}
}

func TestResolveFallbacks(t *testing.T) {
	id1, n1, _ := monIdentity{GDI: `\\.\DISPLAY1`}.resolve()
	id2, _, _ := monIdentity{GDI: `\\.\display1`}.resolve()
	if id1 != id2 || n1 != "" || id1[:6] != "w-gdi-" {
		t.Fatalf("GDI 兜底: %s %s %q", id1, id2, n1)
	}
	// DisplayConfig 路径为空（虚拟显示器 / 驱动不给）时退到 EnumDisplayDevices
	id3, _, _ := monIdentity{GDI: `\\.\DISPLAY1`, DC: &dcTarget{Friendly: "X"}, EDDPath: pathA}.resolve()
	id4, _, _ := monIdentity{GDI: `\\.\DISPLAY9`, WallPath: pathA}.resolve()
	if id3 != id4 {
		t.Fatal("应回退到 EDD 路径")
	}
	// 非 interface path 的 DeviceID（老驱动返回 MONITOR\DEL40A3\{...}\0001）不当作稳定键
	id5, _, _ := monIdentity{GDI: `\\.\DISPLAY1`, EDDPath: `MONITOR\DEL40A3\{4d36e96e}\0001`}.resolve()
	if id5 != id1 {
		t.Fatal("非 interface path 应回退到 GDI")
	}
}

func TestResolveNames(t *testing.T) {
	cases := []struct {
		m    monIdentity
		want string
	}{
		{monIdentity{DC: &dcTarget{Friendly: "DELL U2720Q"}, EDDString: "Generic PnP Monitor"}, "DELL U2720Q"},
		{monIdentity{DC: &dcTarget{Friendly: ""}, EDDString: "LG HDR 4K"}, "LG HDR 4K"},
		{monIdentity{EDDString: "Generic PnP Monitor"}, ""},
		{monIdentity{EDDString: "通用即插即用监视器"}, ""},
		{monIdentity{GDI: `\\.\DISPLAY1`}, ""},
	}
	for _, c := range cases {
		if _, n, _ := c.m.resolve(); n != c.want {
			t.Errorf("%+v: got %q want %q", c.m, n, c.want)
		}
	}
}

func TestResolveLegacyIDs(t *testing.T) {
	// 0.2.0 的 ID = "w-" + hash(IDesktopWallpaper 原始路径)，没有时 hash(GDI 名)
	id, _, legacy := monIdentity{GDI: `\\.\DISPLAY1`, DC: &dcTarget{Path: pathA}, WallPath: pathA}.resolve()
	want := map[desktop.DisplayID]bool{hashID("w-", pathA): true, hashID("w-", `\\.\DISPLAY1`): true}
	if len(legacy) != 2 {
		t.Fatal(legacy)
	}
	for _, l := range legacy {
		if !want[l] || l == id {
			t.Fatal(legacy)
		}
	}
}

func toUpperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
