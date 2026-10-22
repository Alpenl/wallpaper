//go:build darwin

package macos

import "testing"

// 验证本机框架加载及 UUID 符号绑定，不调用需要主线程或显示设备的 API。
func TestLoadDisplayUUID(t *testing.T) {
	previousMain := Main
	Main = func(fn func()) { fn() }
	t.Cleanup(func() { Main = previousMain })
	if err := Load(); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if CGDisplayCreateUUID == nil {
		t.Fatal("CGDisplayCreateUUID is nil after Load()")
	}
}
