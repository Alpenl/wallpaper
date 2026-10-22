//go:build darwin

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"wpapp/internal/ipc"
	"wpapp/internal/platform/macos"
)

var sel = objc.RegisterName

func ns(s string) objc.ID  { return macos.NSString(s) }
func cls(s string) objc.ID { return objc.ID(objc.GetClass(s)) }
func blockCall(b uintptr, args ...uintptr) {
	literal := (*[3]uintptr)(unsafe.Pointer(b))
	purego.SyscallN(literal[2], append([]uintptr{b}, args...)...)
}
func macPump() {
	pool := cls("NSAutoreleasePool").Send(sel("new"))
	defer pool.Send(sel("drain"))
	date := cls("NSDate").Send(sel("dateWithTimeIntervalSinceNow:"), float64(.002))
	cls("NSRunLoop").Send(sel("currentRunLoop")).Send(sel("runMode:beforeDate:"), ns("NSDefaultRunLoopMode"), date)
}
func macAwait(done *bool) error {
	end := time.Now().Add(15 * time.Second)
	for !*done {
		macPump()
		if time.Now().After(end) {
			return fmt.Errorf("WKWebView operation timed out")
		}
	}
	return nil
}

type macView struct {
	ready                  bool
	navigationErr          error
	rulesDir               string
	token                  string
	view, window, delegate objc.ID
	root                   *os.Root
	origin, props          string
	fps                    int
	initial                bool
	entry                  string
}

func (v *macView) Load(x loadRequest) error {
	m, e := validateLoad(x)
	if e != nil {
		return e
	}
	v.token = x.Token
	v.origin = Origin(m.ID, "darwin")
	v.entry = entryURL(x, m, "darwin")
	v.fps = EffectiveFPS(m.Web.FPSCap, x.Item.FPSCap)
	v.props, e = PropertyScript(m.Properties, x.Item.Properties)
	if e != nil {
		return e
	}
	v.root, e = os.OpenRoot(contentDir(x, m))
	if e != nil {
		return e
	}
	methods := []objc.MethodDef{
		{Cmd: sel("webView:navigationAction:didBecomeDownload:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, download objc.ID) {
			download.Send(sel("cancel:"), uintptr(0))
		}},
		{Cmd: sel("webView:navigationResponse:didBecomeDownload:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, download objc.ID) {
			download.Send(sel("cancel:"), uintptr(0))
		}},
		{Cmd: sel("webView:runOpenPanelWithParameters:initiatedByFrame:completionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, decision uintptr) { blockCall(decision, 0) }},
		{Cmd: sel("webView:runJavaScriptAlertPanelWithMessage:initiatedByFrame:completionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, decision uintptr) { blockCall(decision) }},
		{Cmd: sel("webView:runJavaScriptConfirmPanelWithMessage:initiatedByFrame:completionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, decision uintptr) { blockCall(decision, 0) }},
		{Cmd: sel("webView:runJavaScriptTextInputPanelWithPrompt:defaultText:initiatedByFrame:completionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, _ objc.ID, decision uintptr) {
			blockCall(decision, 0)
		}},
		{Cmd: sel("webView:startURLSchemeTask:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, task objc.ID) { v.serve(task) }},
		{Cmd: sel("webView:stopURLSchemeTask:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID) {}},
		{Cmd: sel("webView:decidePolicyForNavigationAction:decisionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, action objc.ID, decision uintptr) {
			target := macos.GoString(action.Send(sel("request")).Send(sel("URL")).Send(sel("absoluteString")))
			allow := AllowNavigation(v.origin, target, false)
			if allow {
				blockCall(decision, 1)
			} else {
				blockCall(decision, 0)
			}
		}},
		{Cmd: sel("webView:decidePolicyForNavigationResponse:decisionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, response objc.ID, decision uintptr) {
			if objc.Send[bool](response, sel("canShowMIMEType")) {
				blockCall(decision, 1)
			} else {
				blockCall(decision, 0)
			}
		}},
		{Cmd: sel("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, _ objc.ID) objc.ID { return 0 }},
		{Cmd: sel("webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, _ uintptr, decision uintptr) {
			blockCall(decision, 0)
		}},
		{Cmd: sel("webView:requestDeviceOrientationAndMotionPermissionForOrigin:initiatedByFrame:decisionHandler:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, _ objc.ID, decision uintptr) { blockCall(decision, 0) }},
		{Cmd: sel("webView:didFailProvisionalNavigation:withError:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, err objc.ID) {
			v.navigationErr = fmt.Errorf("WKWebView navigation failed: %s", macos.GoString(err.Send(sel("localizedDescription"))))
			v.ready = true
		}},
		{Cmd: sel("webView:didFailNavigation:withError:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, err objc.ID) {
			v.navigationErr = fmt.Errorf("WKWebView navigation failed: %s", macos.GoString(err.Send(sel("localizedDescription"))))
			v.ready = true
		}},
		{Cmd: sel("webView:didFinishNavigation:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID) {
			v.ready = true
			_ = v.Script(v.props)
			s := authorizedFPS(v.fps, v.token)
			_ = v.Script(s)
		}},
		{Cmd: sel("webViewWebContentProcessDidTerminate:"), Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) { os.Exit(3) }},
	}
	c, e := objc.RegisterClass("WPKSandboxDelegate", objc.GetClass("NSObject"), nil, nil, methods)
	if e != nil {
		return e
	}
	v.delegate = objc.ID(c).Send(sel("new"))
	config := cls("WKWebViewConfiguration").Send(sel("new"))
	defer config.Send(sel("release"))
	config.Send(sel("setWebsiteDataStore:"), cls("WKWebsiteDataStore").Send(sel("nonPersistentDataStore")))
	config.Send(sel("setURLSchemeHandler:forURLScheme:"), v.delegate, ns("wpk"))
	config.Send(sel("setMediaTypesRequiringUserActionForPlayback:"), ^uintptr(0))
	prefs := config.Send(sel("preferences"))
	prefs.Send(sel("setJavaScriptCanOpenWindowsAutomatically:"), false)
	controller := config.Send(sel("userContentController"))
	// No WKScriptMessageHandler or JavaScript-to-native bridge is registered.
	script := cls("WKUserScript").Send(sel("alloc")).Send(sel("initWithSource:injectionTime:forMainFrameOnly:"), ns(bootstrapScript(v.token, v.fps)), 0, false)
	controller.Send(sel("addUserScript:"), script)
	script.Send(sel("release"))
	if !x.Item.NetworkGranted {
		done := false
		var failure error
		b := objc.NewBlock(func(_ objc.Block, list, err objc.ID) {
			if err != 0 || list == 0 {
				failure = fmt.Errorf("WKContentRuleList compilation failed: %s", macos.GoString(err.Send(sel("localizedDescription"))))
			} else {
				controller.Send(sel("addContentRuleList:"), list)
			}
			done = true
		})
		defer b.Release()
		v.rulesDir, e = os.MkdirTemp("", "wpk-content-rules-*")
		if e != nil {
			return e
		}
		ruleURL := cls("NSURL").Send(sel("fileURLWithPath:"), ns(v.rulesDir))
		store := cls("WKContentRuleListStore").Send(sel("storeWithURL:"), ruleURL)
		store.Send(sel("compileContentRuleListForIdentifier:encodedContentRuleList:completionHandler:"), ns("wpk-deny-network-v1"), ns(`[{"trigger":{"url-filter":"^https?://"},"action":{"type":"block"}},{"trigger":{"url-filter":"^wss?://"},"action":{"type":"block"}}]`), b)
		if e = macAwait(&done); e != nil {
			return e
		}
		if failure != nil {
			return failure
		}
	}
	frame := macos.CGRect{Origin: macos.CGPoint{X: float64(x.Bounds.Min.X), Y: float64(x.Bounds.Min.Y)}, Size: macos.CGSize{W: float64(x.Bounds.Dx()), H: float64(x.Bounds.Dy())}}
	// AppKit uses a bottom-left coordinate origin; desktop bounds are Quartz.
	screens := cls("NSScreen").Send(sel("screens"))
	if objc.Send[uintptr](screens, sel("count")) == 0 {
		return fmt.Errorf("web capability unavailable: no accessible macOS graphical session")
	}
	first := screens.Send(sel("objectAtIndex:"), uintptr(0))
	lib, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	primary := objc.Send[macos.CGRect](first, sel("frame"))
	frame.Origin.Y = primary.Size.H - float64(x.Bounds.Max.Y)
	v.window = macos.MsgInitWindow(cls("NSWindow").Send(sel("alloc")), sel("initWithContentRect:styleMask:backing:defer:"), frame, 0, 2, false, 0)
	v.window.Send(sel("setReleasedWhenClosed:"), false)
	v.window.Send(sel("setLevel:"), int64(macos.CGWindowLevelForKey(2)))
	v.window.Send(sel("setCollectionBehavior:"), uintptr(1|16|64))
	v.window.Send(sel("setIgnoresMouseEvents:"), true)
	var initView func(objc.ID, objc.SEL, macos.CGRect, objc.ID) objc.ID
	purego.RegisterLibFunc(&initView, lib, "objc_msgSend")
	v.view = initView(cls("WKWebView").Send(sel("alloc")), sel("initWithFrame:configuration:"), macos.CGRect{Size: frame.Size}, config)
	if v.view == 0 {
		return fmt.Errorf("WKWebView initialization failed")
	}
	v.view.Send(sel("setNavigationDelegate:"), v.delegate)
	v.view.Send(sel("setUIDelegate:"), v.delegate)
	if objc.Send[bool](v.view, sel("respondsToSelector:"), sel("setInspectable:")) {
		v.view.Send(sel("setInspectable:"), false)
	}
	if m.Web.Transparent {
		v.window.Send(sel("setOpaque:"), false)
		v.window.Send(sel("setBackgroundColor:"), cls("NSColor").Send(sel("clearColor")))
		if !objc.Send[bool](v.view, sel("respondsToSelector:"), sel("setUnderPageBackgroundColor:")) {
			return fmt.Errorf("transparent web wallpapers require macOS 12 or newer")
		}
		v.view.Send(sel("setUnderPageBackgroundColor:"), cls("NSColor").Send(sel("clearColor")))
	}
	v.window.Send(sel("setContentView:"), v.view)
	v.window.Send(sel("orderFrontRegardless"))
	u := cls("NSURL").Send(sel("URLWithString:"), ns(v.entry))
	req := cls("NSURLRequest").Send(sel("requestWithURL:"), u)
	v.view.Send(sel("loadRequest:"), req)
	if e := macAwait(&v.ready); e != nil {
		return e
	}
	if v.navigationErr != nil {
		return v.navigationErr
	}
	return v.Pause(x.Paused)
}
func (v *macView) serve(task objc.ID) {
	u := task.Send(sel("request")).Send(sel("URL"))
	target := macos.GoString(u.Send(sel("absoluteString")))
	parsed, e := url.Parse(target)
	fail := func() {
		err := cls("NSError").Send(sel("errorWithDomain:code:userInfo:"), ns("WPKSandbox"), int64(403), objc.ID(0))
		task.Send(sel("didFailWithError:"), err)
	}
	if e != nil || !sameOrigin(v.origin, target) {
		fail()
		return
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if e = checkResourcePath(name); e != nil {
		fail()
		return
	}
	f, e := v.root.Open(name)
	if e != nil {
		fail()
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 256<<20 {
		fail()
		return
	}
	b, e := io.ReadAll(io.LimitReader(f, (256<<20)+1))
	if e != nil || len(b) > 256<<20 {
		fail()
		return
	}
	typ := mime.TypeByExtension(path.Ext(name))
	if mediaType, _, err := mime.ParseMediaType(typ); err == nil {
		typ = mediaType
	}
	if typ == "" {
		typ = "application/octet-stream"
	}
	response := cls("NSURLResponse").Send(sel("alloc")).Send(sel("initWithURL:MIMEType:expectedContentLength:textEncodingName:"), u, ns(typ), int64(len(b)), ns("utf-8"))
	defer response.Send(sel("release"))
	var data objc.ID
	if len(b) > 0 {
		data = cls("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&b[0]), uintptr(len(b)))
	} else {
		data = cls("NSData").Send(sel("data"))
	}
	task.Send(sel("didReceiveResponse:"), response)
	task.Send(sel("didReceiveData:"), data)
	task.Send(sel("didFinish"))
	runtime.KeepAlive(b)
}
func (v *macView) Script(s string) error {
	v.view.Send(sel("evaluateJavaScript:completionHandler:"), ns(s), uintptr(0))
	return nil
}
func (v *macView) Pause(paused bool) error {
	v.view.Send(sel("setHidden:"), paused)
	if !paused {
		if err := v.Script(v.props); err != nil {
			return err
		}
		return v.Script(authorizedFPS(v.fps, v.token))
	}
	return nil
}
func (v *macView) Close() {
	defer func() {
		if v.rulesDir != "" {
			_ = os.RemoveAll(v.rulesDir)
		}
	}()
	if v.view != 0 {
		v.view.Send(sel("stopLoading"))
		v.view.Send(sel("setNavigationDelegate:"), objc.ID(0))
		v.view.Send(sel("setUIDelegate:"), objc.ID(0))
	}
	if v.window != 0 {
		v.window.Send(sel("close"))
		v.window.Send(sel("release"))
	}
	if v.view != 0 {
		v.view.Send(sel("release"))
	}
	if v.delegate != 0 {
		v.delegate.Send(sel("release"))
	}
	if v.root != nil {
		v.root.Close()
	}
}
func runHost(p pipeIO) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Load only the frameworks needed by this host. Desktop discovery and
	// AVFoundation initialization belong to the parent, not this process.
	for _, framework := range []string{"AppKit", "WebKit", "QuartzCore"} {
		if _, e := purego.Dlopen("/System/Library/Frameworks/"+framework+".framework/"+framework, purego.RTLD_NOW|purego.RTLD_GLOBAL); e != nil {
			return e
		}
	}
	cg, e := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW)
	if e != nil {
		return e
	}
	purego.RegisterLibFunc(&macos.CGWindowLevelForKey, cg, "CGWindowLevelForKey")
	lib, e := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	if e != nil {
		return e
	}
	purego.RegisterLibFunc(&macos.MsgInitWindow, lib, "objc_msgSend")
	if _, e := purego.Dlopen("/System/Library/Frameworks/WebKit.framework/WebKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); e != nil {
		return e
	}
	app := cls("NSApplication").Send(sel("sharedApplication"))
	app.Send(sel("setActivationPolicy:"), 2)
	app.Send(sel("finishLaunching"))
	v := &macView{}
	defer v.Close()
	h := &hostSession{view: v}
	jobs := make(chan func())
	c := ipc.New(p, func(m string, b json.RawMessage) (any, *ipc.Error) {
		var result any
		var err *ipc.Error
		done := make(chan struct{})
		jobs <- func() { result, err = h.handle(m, b); close(done) }
		<-done
		return result, err
	}, nil)
	defer c.Close()
	for {
		select {
		case <-c.Done():
			return nil
		case job := <-jobs:
			job()
		default:
			macPump()
		}
	}
}

func (v *macView) Properties(s string) error { v.props = s; return v.Script(s) }
func (v *macView) FPS(fps int) error {
	v.fps = fps
	return v.Script(authorizedFPS(fps, v.token))
}
