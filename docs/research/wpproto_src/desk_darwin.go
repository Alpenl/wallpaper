package main

import (
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const kCGDesktopWindowLevel = -2147483623 // kCGMinimumWindowLevel(INT32_MIN)+kCGDesktopWindowLevelKey(2)... verify at runtime via CGWindowLevelForKey

func attachDesktop(h uintptr) {
	cg, _ := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL)
	var levelForKey func(int32) int32
	purego.RegisterLibFunc(&levelForKey, cg, "CGWindowLevelForKey")
	lvl := levelForKey(2) // kCGDesktopWindowLevelKey
	win := objc.ID(h)
	win.Send(objc.RegisterName("setLevel:"), int(lvl))
	win.Send(objc.RegisterName("setCollectionBehavior:"), uint(1|16|64)) // CanJoinAllSpaces|Stationary|IgnoresCycle
	win.Send(objc.RegisterName("setIgnoresMouseEvents:"), true)

	// AVFoundation via purego, no cgo
	purego.Dlopen("/System/Library/Frameworks/AVFoundation.framework/AVFoundation", purego.RTLD_GLOBAL)
	nsurl := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("fileURLWithPath:"),
		objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "/tmp/wall.mp4\x00"))
	player := objc.ID(objc.GetClass("AVPlayer")).Send(objc.RegisterName("playerWithURL:"), nsurl)
	layer := objc.ID(objc.GetClass("AVPlayerLayer")).Send(objc.RegisterName("playerLayerWithPlayer:"), player)
	view := win.Send(objc.RegisterName("contentView"))
	view.Send(objc.RegisterName("setWantsLayer:"), true)
	view.Send(objc.RegisterName("layer")).Send(objc.RegisterName("addSublayer:"), layer)
	player.Send(objc.RegisterName("play"))
}
