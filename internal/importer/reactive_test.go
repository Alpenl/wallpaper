package importer

import (
	"context"
	"testing"
	"wpapp/internal/wpk"
)

func TestSignalImportConsent(t *testing.T) {
	seed, e := Import(context.Background(), "../../examples/web-orbit", opts(t, "windows"))
	if e != nil {
		t.Fatal(e)
	}
	m, e := wpk.LoadDir(seed.Item.Dir)
	if e != nil {
		t.Fatal(e)
	}
	m.Permissions = []string{"audio", "input"}
	if e = wpk.WriteManifest(seed.Item.Dir, m); e != nil {
		t.Fatal(e)
	}
	o := opts(t, "windows")
	r, e := Import(context.Background(), seed.Item.Dir, o)
	if e != nil {
		t.Fatal(e)
	}
	if r.Item.AudioGranted || r.Item.InputGranted || len(r.Item.Permissions) != 2 {
		t.Fatal("default deny", r.Item)
	}
	called := map[string]int{}
	o.ConsentSignal = func(_, p string) bool { called[p]++; return p == "audio" }
	r, e = Import(context.Background(), seed.Item.Dir, o)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Item.AudioGranted || r.Item.InputGranted || called["audio"] != 1 || called["input"] != 1 {
		t.Fatal("independent consent", r.Item, called)
	}
	o.ConsentSignal = nil
	r, e = Import(context.Background(), seed.Item.Dir, o)
	if e != nil {
		t.Fatal(e)
	}
	if r.Item.AudioGranted || r.Item.InputGranted {
		t.Fatal("replacement retained grants")
	}
}
