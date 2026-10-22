//go:build darwin

package shader

import (
	"github.com/ebitengine/purego"
	"testing"
)

func TestNativeIntegerBridgeAllocations(t *testing.T) {
	if _, e := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); e != nil {
		t.Skip(e)
	}
	if e := initBridge(); e != nil {
		t.Fatal(e)
	}
	object := cls("NSObject").Send(sel("new"))
	defer object.Send(sel("release"))
	if n := testing.AllocsPerRun(100, func() { message(object, "retain"); message(object, "release") }); n != 0 {
		t.Fatalf("native integer bridge allocates: %v", n)
	}
}
