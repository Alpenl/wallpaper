package supervisor

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for i, w := range want {
		if Backoff(i) != w {
			t.Fatal(i, Backoff(i))
		}
	}
	if Backoff(-3) != 500*time.Millisecond {
		t.Fatal()
	}
}
