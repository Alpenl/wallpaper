package app

import (
	"image"
	"testing"

	"wpapp/internal/config"
	"wpapp/internal/desktop"
)

func (e *env) assignSnap() config.Assignments {
	ch := make(chan config.Assignments)
	e.a.Do(func() {
		a := e.store.Assign
		m, al := map[string]string{}, map[string]string{}
		for k, v := range a.Displays {
			m[k] = v
		}
		for k, v := range a.Aliases {
			al[k] = v
		}
		ch <- config.Assignments{Default: a.Default, Displays: m, Aliases: al}
	})
	return <-ch
}

func (e *env) view() View { e.sync(); return e.a.View() }

func byID(v View, id string) DisplayView {
	for _, d := range v.Displays {
		if d.ID == id {
			return d
		}
	}
	return DisplayView{}
}

// 0.2.0 的 bug：导入一张壁纸会立刻应用到所有显示器。
func TestImportOnlyAddsToLibrary(t *testing.T) {
	e := setup(t, 2, false)
	e.a.Import("good.mp4", "")
	eventually(t, "导入完成", func() bool { return len(e.a.View().Library) == 1 })
	v := e.view()
	for _, d := range v.Displays {
		if d.ItemID != "" || d.Custom {
			t.Fatalf("导入不应改动显示器: %+v", d)
		}
	}
	if a := e.assignSnap(); a.Default != "" || len(a.Displays) != 0 {
		t.Fatalf("导入不应写 assignments: %+v", a)
	}
	if e.cur("A") != nil || e.cur("B") != nil {
		t.Fatal("导入不应启动渲染器")
	}
}

func TestImportApplyToSelectedOnly(t *testing.T) {
	e := setup(t, 2, false)
	e.a.Import("good.mp4", "B")
	eventually(t, "B 播放", func() bool { r := e.cur("B"); return r != nil && r.isLoaded() })
	v := e.view()
	if byID(v, "A").ItemID != "" || byID(v, "B").ItemID != "new" || !byID(v, "B").Custom {
		t.Fatalf("%+v", v.Displays)
	}
	if e.cur("A") != nil {
		t.Fatal("A 不应启动")
	}
}

func TestPerDisplayAssignIndependentAndAll(t *testing.T) {
	e := setup(t, 2, true) // 默认 x，两屏都跟随
	e.a.Do(func() { e.store.Library.Upsert(config.Item{ID: "y", Dir: t.TempDir(), Type: "video", Entry: "a.mp4"}) })
	e.a.Assign("B", "y")
	eventually(t, "B=y", func() bool { return byID(e.view(), "B").ItemID == "y" })
	v := e.view()
	if byID(v, "A").ItemID != "x" || byID(v, "A").Custom || !byID(v, "B").Custom {
		t.Fatalf("A 应仍跟随默认: %+v", v.Displays)
	}
	if a := e.assignSnap(); a.Displays["B"] != "y" || len(a.Displays) != 1 || a.Default != "x" {
		t.Fatalf("应按稳定 ID 持久化: %+v", a)
	}
	// 改默认不影响已单独设置的 B
	e.a.Assign("", "y")
	e.a.Assign("", "x")
	if v := e.view(); byID(v, "B").ItemID != "y" || byID(v, "A").ItemID != "x" {
		t.Fatalf("%+v", v.Displays)
	}
	// 清除单独设置 → 跟随默认
	e.a.Assign("B", "")
	if v := e.view(); byID(v, "B").ItemID != "x" || byID(v, "B").Custom {
		t.Fatalf("%+v", v.Displays)
	}
	// 应用到所有显示器：清掉单独设置
	e.a.Assign("A", "y")
	e.a.AssignAll("y")
	v = e.view()
	if byID(v, "A").ItemID != "y" || byID(v, "B").ItemID != "y" || byID(v, "A").Custom {
		t.Fatalf("%+v", v.Displays)
	}
	if a := e.assignSnap(); a.Default != "y" || len(a.Displays) != 0 {
		t.Fatalf("%+v", a)
	}
}

// 截图里的问题：卡片“2”对应 \\.\DISPLAY1。编号必须按物理排列，且与卡片内容一致。
func TestDisplayOrderingAndNames(t *testing.T) {
	e := setupWith(t, 0, false, nil)
	e.h.mu.Lock()
	e.h.displays = []desktop.Display{
		{ID: "R", Device: `\\.\DISPLAY2`, Name: "DELL U2720Q", Primary: true, Bounds: image.Rect(1920, 0, 4480, 1440)},
		{ID: "L", Device: `\\.\DISPLAY1`, Name: "", Bounds: image.Rect(0, 200, 1920, 1280)},
	}
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "两块屏", func() bool { return len(e.a.View().Displays) == 2 })
	v := e.view()
	if v.Displays[0].ID != "L" || v.Displays[0].Index != 1 || v.Displays[1].ID != "R" || v.Displays[1].Index != 2 {
		t.Fatalf("应按左→右编号: %+v", v.Displays)
	}
	if v.Displays[0].Name != "显示器 1" || v.Displays[1].Name != "DELL U2720Q" {
		t.Fatalf("名字: %q %q", v.Displays[0].Name, v.Displays[1].Name)
	}
	for _, d := range v.Displays {
		if d.Name == d.Device || len(d.Name) > 0 && d.Name[0] == '\\' {
			t.Fatalf("不应把系统内部路径当名字: %+v", d)
		}
	}
	if v.Displays[1].W != 2560 || v.Displays[1].H != 1440 || !v.Displays[1].Primary {
		t.Fatalf("%+v", v.Displays[1])
	}
}

func TestRenameAliasByStableID(t *testing.T) {
	e := setup(t, 2, false) // A 在左，B 在右
	e.a.SetAlias("B", "  右边的屏\n ")
	v := e.view()
	if byID(v, "B").Name != "右边的屏" || byID(v, "B").Alias != "右边的屏" || byID(v, "A").Name != "A" {
		t.Fatalf("别名应只作用于 B（没有错位）: %+v", v.Displays)
	}
	if a := e.assignSnap(); a.Aliases["B"] != "右边的屏" || len(a.Aliases) != 1 {
		t.Fatalf("%+v", a)
	}
	// 调换物理位置：编号变，但别名跟着 ID 走
	e.h.mu.Lock()
	e.h.displays[0].Bounds, e.h.displays[1].Bounds = e.h.displays[1].Bounds, e.h.displays[0].Bounds
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "重排", func() bool { v := e.a.View(); return len(v.Displays) == 2 && v.Displays[0].ID == "B" })
	v = e.view()
	if v.Displays[0].Name != "右边的屏" || v.Displays[0].Index != 1 || v.Displays[1].Name != "A" {
		t.Fatalf("%+v", v.Displays)
	}
	// 恢复默认名称
	e.a.SetAlias("B", "")
	if d := byID(e.view(), "B"); d.Name != "B" || d.Alias != "" {
		t.Fatalf("%+v", d)
	}
	if a := e.assignSnap(); len(a.Aliases) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestLegacyIDMigration(t *testing.T) {
	e := setupWith(t, 0, true, func(d *Deps) {
		d.Store.Assign.Displays = map[string]string{"old-gdi": "x"}
		d.Store.Assign.Aliases = map[string]string{"old-gdi": "客厅"}
		d.Store.Assign.Default = ""
	})
	e.h.mu.Lock()
	e.h.displays = []desktop.Display{{ID: "new", LegacyIDs: []desktop.DisplayID{"old-gdi"}, Bounds: image.Rect(0, 0, 100, 100), Primary: true}}
	e.h.mu.Unlock()
	e.h.ev <- desktop.Event{Kind: desktop.EventDisplaysChanged}
	eventually(t, "迁移后播放", func() bool { r := e.cur("new"); return r != nil && r.isLoaded() })
	a := e.assignSnap()
	if a.Displays["new"] != "x" || a.Aliases["new"] != "客厅" || len(a.Displays) != 1 || len(a.Aliases) != 1 {
		t.Fatalf("%+v", a)
	}
	if d := byID(e.view(), "new"); d.Name != "客厅" {
		t.Fatalf("%+v", d)
	}
}

func TestNextPerDisplay(t *testing.T) {
	e := setup(t, 2, true)
	e.a.Do(func() { e.store.Library.Upsert(config.Item{ID: "y", Dir: t.TempDir(), Type: "video", Entry: "a.mp4"}) })
	e.a.Assign("B", "y")
	e.a.Next()
	v := e.view()
	if byID(v, "A").ItemID != "y" || byID(v, "B").ItemID != "x" {
		t.Fatalf("每块屏各自前进一张: %+v", v.Displays)
	}
}
