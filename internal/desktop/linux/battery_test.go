//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadBattery(t *testing.T) {
	d := t.TempDir()
	if on, p := ReadBattery(d); on || p != -1 {
		t.Fatal(on, p)
	}
	// Peripheral device battery (e.g. wireless mouse/keyboard) must be ignored.
	mouse := filepath.Join(d, "00-hidpp_battery_0")
	os.MkdirAll(mouse, 0o755)
	os.WriteFile(filepath.Join(mouse, "type"), []byte("Battery\n"), 0o644)
	os.WriteFile(filepath.Join(mouse, "scope"), []byte("Device\n"), 0o644)
	os.WriteFile(filepath.Join(mouse, "status"), []byte("Discharging\n"), 0o644)
	os.WriteFile(filepath.Join(mouse, "capacity"), []byte("12\n"), 0o644)
	if on, p := ReadBattery(d); on || p != -1 {
		t.Fatalf("device-scoped peripheral battery should be ignored, got on=%v pct=%d", on, p)
	}

	b := filepath.Join(d, "BAT0")
	os.MkdirAll(b, 0o755)
	os.WriteFile(filepath.Join(b, "type"), []byte("Battery\n"), 0o644)
	os.WriteFile(filepath.Join(b, "scope"), []byte("System\n"), 0o644)
	os.WriteFile(filepath.Join(b, "status"), []byte("Discharging\n"), 0o644)
	os.WriteFile(filepath.Join(b, "capacity"), []byte("17\n"), 0o644)
	if on, p := ReadBattery(d); !on || p != 17 {
		t.Fatal(on, p)
	}
}
