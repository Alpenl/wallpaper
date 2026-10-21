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
