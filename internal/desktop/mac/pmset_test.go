package mac

import "testing"

func TestParsePmset(t *testing.T) {
	on, p := ParsePmset("Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t57%; discharging; 3:12 remaining present: true\n")
	if !on || p != 57 {
		t.Fatal(on, p)
	}
	on, p = ParsePmset("Now drawing from 'AC Power'\n")
	if on || p != -1 {
		t.Fatal(on, p)
	}
}

func TestCovered(t *testing.T) {
	d := Rect{0, 0, 1000, 500}
	if c, f := Covered(d, nil); c || f {
		t.Fatal()
	}
	if c, f := Covered(d, []Rect{{0, 0, 1000, 500}}); !c || !f {
		t.Fatal()
	}
	if c, f := Covered(d, []Rect{{0, 0, 500, 500}, {500, 0, 480, 500}}); !c || f {
		t.Fatal("98% 应判遮挡")
	}
	if c, _ := Covered(d, []Rect{{0, 0, 500, 500}, {2000, 0, 100, 100}}); c {
		t.Fatal()
	}
}

func TestCoveredNotchFullscreen(t *testing.T) {
	// MacBook Pro 14" logical bounds with ~38pt notch/menu-bar safe area.
	disp := Rect{0, 0, 1512, 982}
	if c, f := Covered(disp, []Rect{{0, 38, 1512, 944}}); !c || !f {
		t.Fatalf("native fullscreen below notch should be detected: covered=%v fullscreen=%v", c, f)
	}
	// Maximized window with visible Dock (does not reach bottom edge) is not fullscreen.
	if _, f := Covered(disp, []Rect{{0, 38, 1512, 870}}); f {
		t.Fatal("window stopping above Dock must not be treated as fullscreen")
	}
	// Custom larger top inset (e.g. 56pt).
	if c, f := CoveredWithTopInset(disp, 56, []Rect{{0, 56, 1512, 926}}); !c || !f {
		t.Fatalf("custom top inset fullscreen failed: covered=%v fullscreen=%v", c, f)
	}
	if got := topInsetFromFrames(0, 982, 70, 874); got != 50 {
		t.Fatalf("topInsetFromFrames(38pt) = %g, want 50", got)
	}
	if got := topInsetFromFrames(0, 982, 70, 852); got != 60 {
		t.Fatalf("topInsetFromFrames(60pt) = %g, want 60", got)
	}
}

func TestDebounce3(t *testing.T) {
	var d Debounce3
	flips := 0
	for _, v := range []bool{true, true, false, true, true, true, false} {
		if d.Push(v) {
			flips++
		}
	}
	if flips != 1 || !d.State {
		t.Fatal(flips, d.State)
	}
}

func TestPowerSignals(t *testing.T) {
	for _, c := range []struct {
		low           bool
		th            int
		sv, ser, crit bool
	}{{false, 0, false, false, false}, {true, 1, true, false, false}, {false, 2, false, true, false}, {false, 3, false, false, true}} {
		sv, ser, crit := PowerSignals(c.low, c.th)
		if sv != c.sv || ser != c.ser || crit != c.crit {
			t.Error(c)
		}
	}
	if ThermalName(2) != "严重" || ThermalName(9) != "未知" {
		t.Fatal()
	}
}
