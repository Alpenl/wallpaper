//go:build windows

package web

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"wpapp/internal/ipc"
	"wpapp/internal/wpk"
)

var ole = syscall.NewLazyDLL("ole32.dll")
var user = syscall.NewLazyDLL("user32.dll")

var (
	iidIUnknown                   = syscall.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidEnvCompletedHandler        = syscall.GUID{Data1: 0x4e8a3389, Data2: 0xc9d8, Data3: 0x4bd2, Data4: [8]byte{0xb6, 0xb5, 0x12, 0x4f, 0xee, 0x6c, 0xc1, 0x4d}}
	iidControllerCompletedHandler = syscall.GUID{Data1: 0x6c4819f3, Data2: 0xc9b7, Data3: 0x4260, Data4: [8]byte{0x81, 0x27, 0xc9, 0xf5, 0xbd, 0xe7, 0xf6, 0x8c}}
	iidNavigationStartingHandler  = syscall.GUID{Data1: 0x9adbe429, Data2: 0xf36d, Data3: 0x432b, Data4: [8]byte{0x9d, 0xdc, 0xf8, 0x88, 0x1f, 0xbd, 0x76, 0xe3}}
	iidNewWindowRequestedHandler  = syscall.GUID{Data1: 0xd4c185fe, Data2: 0xc81c, Data3: 0x4989, Data4: [8]byte{0x97, 0xaf, 0x2d, 0x3f, 0xa7, 0xab, 0x56, 0x51}}
	iidPermissionRequestedHandler = syscall.GUID{Data1: 0x15e1c6a3, Data2: 0xc72a, Data3: 0x4df3, Data4: [8]byte{0x91, 0xd7, 0xd0, 0x97, 0xfb, 0xec, 0x6b, 0xfd}}
	iidDownloadStartingHandler    = syscall.GUID{Data1: 0xefedc989, Data2: 0xc396, Data3: 0x41ca, Data4: [8]byte{0x83, 0xf7, 0x07, 0xf8, 0x45, 0xa5, 0x57, 0x24}}
	iidWebResourceRequested       = syscall.GUID{Data1: 0xab00b74c, Data2: 0x15f1, Data3: 0x4646, Data4: [8]byte{0x80, 0xe8, 0xe7, 0x63, 0x41, 0xd2, 0x5d, 0x71}}
	iidNavigationCompletedHandler = syscall.GUID{Data1: 0xd33a35bf, Data2: 0x1c49, Data3: 0x4f98, Data4: [8]byte{0x93, 0xab, 0x00, 0x6e, 0x05, 0x33, 0xfe, 0x1c}}
	iidAddScriptCompletedHandler  = syscall.GUID{Data1: 0xb99369f3, Data2: 0x9b11, Data3: 0x47b5, Data4: [8]byte{0xbc, 0x6f, 0x8e, 0x78, 0x95, 0xfc, 0xea, 0x17}}
	iidTrySuspendCompletedHandler = syscall.GUID{Data1: 0x00f206a7, Data2: 0x9d17, Data3: 0x4605, Data4: [8]byte{0x91, 0xf6, 0x4e, 0x8e, 0x4d, 0xe1, 0x92, 0xe3}}
	iidCapturePreviewCompleted    = syscall.GUID{Data1: 0x697e05e9, Data2: 0x3d8f, Data3: 0x45fa, Data4: [8]byte{0x96, 0xf4, 0x8f, 0xfe, 0x1e, 0xde, 0xda, 0xf5}}
	iidProcessFailedHandler       = syscall.GUID{Data1: 0x79e0aea4, Data2: 0x990b, Data3: 0x42d9, Data4: [8]byte{0xaa, 0x1d, 0x0f, 0xcc, 0x2e, 0x5b, 0xc7, 0xf1}}
)

var nativeRoots []any

// rawPtr reinterprets a native (non-Go) address as unsafe.Pointer for COM/Win32 FFI.
func rawPtr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

func ptr[T any](v *T) uintptr {
	nativeRoots = append(nativeRoots, v)
	return uintptr(unsafe.Pointer(v))
}
func wide(s string) uintptr { p, _ := syscall.UTF16PtrFromString(s); return ptr(p) }
func com(o uintptr, index int, args ...uintptr) uintptr {
	v := *(*uintptr)(rawPtr(o))
	fn := *(*uintptr)(unsafe.Add(rawPtr(v), uintptr(index)*unsafe.Sizeof(v)))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{o}, args...)...)
	return r
}
func hr(v uintptr) error {
	if int32(v) < 0 {
		return fmt.Errorf("WebView2 HRESULT 0x%08x", uint32(v))
	}
	return nil
}
func query(o uintptr, id string) (uintptr, error) {
	var g syscall.GUID
	v, _, _ := ole.NewProc("CLSIDFromString").Call(wide(id), ptr(&g))
	if e := hr(v); e != nil {
		return 0, e
	}
	var out uintptr
	e := hr(com(o, 0, ptr(&g), ptr(&out)))
	return out, e
}
func release(o uintptr) {
	if o != 0 {
		com(o, 2)
	}
}
func comString(o uintptr, index int) string {
	var p *uint16
	if hr(com(o, index, ptr(&p))) != nil || p == nil {
		return ""
	}
	defer ole.NewProc("CoTaskMemFree").Call(ptr(p))
	return syscall.UTF16ToString(unsafe.Slice(p, utf16Len(p)))
}
func utf16Len(p *uint16) int {
	for n := 0; n < 32768; n++ {
		if *(*uint16)(unsafe.Add(unsafe.Pointer(p), uintptr(n*2))) == 0 {
			return n
		}
	}
	return 0
}

// COM owns event callbacks through AddRef/Release. One shared trampoline keeps
// repeated pause/snapshot operations from exhausting syscall callback slots.
type callback struct {
	vtable *[4]uintptr
	fn     func(uintptr, uintptr) uintptr
	iid    syscall.GUID
	refs   int
}

var callbackMu sync.Mutex
var callbacks = map[uintptr]*callback{}

func callbackRef(self uintptr, delta int) uintptr {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	c := callbacks[self]
	if c == nil {
		return 0
	}
	c.refs += delta
	if c.refs <= 0 {
		delete(callbacks, self)
		return 0
	}
	return uintptr(c.refs)
}

var callbackTable = [4]uintptr{
	syscall.NewCallback(func(self, iid, out uintptr) uintptr {
		if out == 0 {
			return 0x80004003 // E_POINTER
		}
		if iid == 0 {
			*(*uintptr)(rawPtr(out)) = 0
			return 0x80004003
		}
		callbackMu.Lock()
		c := callbacks[self]
		callbackMu.Unlock()
		g := *(*syscall.GUID)(rawPtr(iid))
		if c == nil || (g != iidIUnknown && g != c.iid) {
			*(*uintptr)(rawPtr(out)) = 0
			return 0x80004002 // E_NOINTERFACE
		}
		*(*uintptr)(rawPtr(out)) = self
		callbackRef(self, 1)
		return 0
	}),
	syscall.NewCallback(func(self uintptr) uintptr { return callbackRef(self, 1) }),
	syscall.NewCallback(func(self uintptr) uintptr { return callbackRef(self, -1) }),
	syscall.NewCallback(func(self, a, b uintptr) uintptr {
		callbackMu.Lock()
		c := callbacks[self]
		callbackMu.Unlock()
		if c == nil {
			return 0x80004005
		}
		return c.fn(a, b)
	}),
}

func handler(iid syscall.GUID, fn func(uintptr, uintptr) uintptr) uintptr {
	c := &callback{vtable: &callbackTable, fn: fn, iid: iid, refs: 1}
	p := ptr(c)
	callbackMu.Lock()
	callbacks[p] = c
	callbackMu.Unlock()
	return p
}

type winMessage struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

func pump() {
	var m winMessage
	for {
		r, _, _ := user.NewProc("PeekMessageW").Call(ptr(&m), 0, 0, 0, 1)
		if r == 0 {
			break
		}
		user.NewProc("TranslateMessage").Call(ptr(&m))
		user.NewProc("DispatchMessageW").Call(ptr(&m))
	}
}
func await(done *bool) error {
	end := time.Now().Add(15 * time.Second)
	for !*done {
		pump()
		if *done {
			return nil
		}
		rem := time.Until(end)
		if rem <= 0 {
			return fmt.Errorf("WebView2 operation timed out")
		}
		waitMs := uint32(min(rem.Milliseconds(), 50))
		if waitMs == 0 {
			waitMs = 1
		}
		procMsgWaitObjects.Call(0, 0, 0, uintptr(waitMs), 0x04FF) // QS_ALLINPUT
	}
	return nil
}

type winView struct {
	token                                  string
	parent, snapshotWindow, snapshotBitmap uintptr
	paused                                 bool
	env, ctl, wv, wv3, wv4                 uintptr
	userData                               *userDataDir
	origin                                 string
	network                                bool
	allowedHosts                           []string
	props                                  string
	fps                                    int
	ready                                  bool
	navigationErr                          error
	processFailed                          error
	onFatal                                func()
}

func (w *winView) Load(x loadRequest) error {
	m, e := validateLoad(x)
	if e != nil {
		return e
	}
	if x.Native == 0 {
		return fmt.Errorf("web: missing parent HWND")
	}
	w.parent = x.Native
	w.token = x.Token
	w.origin = Origin(m.ID, "windows")
	w.network = x.Item.NetworkGranted
	w.allowedHosts = normalizeAllowedHosts(m.Web.NetworkAllow)
	w.fps = EffectiveFPS(m.Web.FPSCap, x.Item.FPSCap)
	w.props, e = PropertyScript(m.Properties, x.Item.Properties)
	if e != nil {
		return e
	}
	w.userData, e = prepareUserDataDir(x.CacheDir)
	if e != nil {
		return e
	}
	// Keep temporary COM references until Load returns, in the original LIFO order.
	l := &winLoadState{}
	defer l.close()
	if e = w.createEnvironment(l); e != nil {
		return e
	}
	if e = w.createController(l, x.Native); e != nil {
		return e
	}
	if e = w.configureSecurity(l, x, m); e != nil {
		return e
	}
	if e = w.configureRequests(l); e != nil {
		return e
	}
	if e = w.configureNavigation(l, m.Web.Transparent); e != nil {
		return e
	}
	return w.navigateInitial(l, x, m)
}

// winLoadState retains async callback state and temporary COM references for Load.
type winLoadState struct {
	done     bool
	asyncErr error
	refs     []uintptr
}

func (l *winLoadState) close() {
	for i := len(l.refs) - 1; i >= 0; i-- {
		release(l.refs[i])
	}
}

func (w *winView) createEnvironment(l *winLoadState) error {
	create, e := runtimeCreate()
	if e != nil {
		return e
	}
	opts := newEnvOptions(browserArgs(w.network))
	l.refs = append(l.refs, opts)
	h := handler(iidEnvCompletedHandler, func(result, env uintptr) uintptr {
		l.asyncErr = hr(result)
		if l.asyncErr == nil {
			w.env = env
			com(env, 1)
		}
		l.done = true
		return 0
	})
	l.refs = append(l.refs, h)
	v := create(w.userData.path, opts, h)
	if e = hr(v); e != nil {
		return e
	}
	if e = await(&l.done); e != nil {
		return e
	}
	if l.asyncErr != nil {
		return l.asyncErr
	}
	return nil
}

func (w *winView) createController(l *winLoadState, parent uintptr) error {
	var e error
	l.done = false
	h := handler(iidControllerCompletedHandler, func(result, ctl uintptr) uintptr {
		l.asyncErr = hr(result)
		if l.asyncErr == nil {
			w.ctl = ctl
			com(ctl, 1)
		}
		l.done = true
		return 0
	})
	l.refs = append(l.refs, h)
	if e = hr(com(w.env, 3, parent, h)); e != nil {
		return e
	}
	if e = await(&l.done); e != nil {
		return e
	}
	if l.asyncErr != nil {
		return l.asyncErr
	}
	if e = hr(com(w.ctl, 25, ptr(&w.wv))); e != nil {
		return e
	}
	w.wv3, e = query(w.wv, "{A0D6DF20-3B92-416D-AA0C-437A9C727857}")
	if e != nil {
		return e
	}
	w.wv4, e = query(w.wv, "{20D02D59-6DF2-42DC-BD06-F98A694B1302}")
	if e != nil {
		return e
	}
	return nil
}

func (w *winView) configureSecurity(l *winLoadState, x loadRequest, m *wpk.Manifest) error {
	var e error
	// Security configuration is fail-closed: every required COM call is checked.
	var settings uintptr
	if e = hr(com(w.wv, 3, ptr(&settings))); e != nil {
		return e
	}
	l.refs = append(l.refs, settings)
	for _, index := range []int{6, 8, 10, 12, 14, 16, 18} {
		if e = hr(com(settings, index, 0)); e != nil {
			return e
		}
	}
	// ICoreWebView2Settings::AreHostObjectsAllowed is index 16.
	if e = hr(com(w.wv3, 71, wide(m.ID+".wpk.invalid"), wide(contentDir(x, m)), 2)); e != nil {
		return e
	}
	// ICoreWebView2::add_NavigationStarting (index 7)
	if e = addWebHandler(w.wv, 7, iidNavigationStartingHandler, func(_, args uintptr) uintptr {
		if !AllowNavigation(w.origin, comString(args, 3), false) {
			return com(args, 8, 1)
		}
		return 0
	}); e != nil {
		return e
	}
	// ICoreWebView2::add_FrameNavigationStarting (index 17)
	if e = addWebHandler(w.wv, 17, iidNavigationStartingHandler, func(_, args uintptr) uintptr {
		if !AllowNavigation(w.origin, comString(args, 3), false) {
			return com(args, 8, 1)
		}
		return 0
	}); e != nil {
		return e
	}
	// ICoreWebView2::add_NewWindowRequested (index 44)
	if e = addWebHandler(w.wv, 44, iidNewWindowRequestedHandler, func(_, args uintptr) uintptr { return com(args, 6, 1) }); e != nil {
		return e
	}
	// ICoreWebView2::add_PermissionRequested (index 23)
	if e = addWebHandler(w.wv, 23, iidPermissionRequestedHandler, func(_, args uintptr) uintptr { return com(args, 7, 2) }); e != nil {
		return e
	}
	// ICoreWebView2::add_ProcessFailed (index 25)
	if e = addWebHandler(w.wv, 25, iidProcessFailedHandler, func(_, args uintptr) uintptr {
		var kind int32
		_ = com(args, 3, ptr(&kind))
		if w.processFailed == nil {
			w.processFailed = fmt.Errorf("WebView2 process failed (kind=%d)", kind)
		}
		if w.onFatal != nil {
			w.onFatal()
		}
		return 0
	}); e != nil {
		return e
	}
	// ICoreWebView2_4::add_DownloadStarting (index 75)
	if e = addWebHandler(w.wv4, 75, iidDownloadStartingHandler, func(_, args uintptr) uintptr { return com(args, 5, 1) }); e != nil {
		return e
	}
	return nil
}

func addWebHandler(obj uintptr, index int, iid syscall.GUID, fn func(uintptr, uintptr) uintptr) error {
	var token int64
	h := handler(iid, fn)
	defer release(h)
	return hr(com(obj, index, h, ptr(&token)))
}

func (w *winView) configureRequests(l *winLoadState) error {
	filter, e := query(w.wv, "{DB75DFC7-A857-4632-A398-6969DDE26C0A}")
	if e != nil {
		return fmt.Errorf("WebView2 Runtime is too old for worker request isolation: %w", e)
	}
	l.refs = append(l.refs, filter)
	if e = hr(com(filter, 123, wide("*"), 0, 0xffffffff)); e != nil {
		return e
	}
	if e = addWebHandler(w.wv, 55, iidWebResourceRequested, func(_, args uintptr) uintptr {
		var req uintptr
		if hr(com(args, 3, ptr(&req))) != nil {
			return 0x80004005
		}
		defer release(req)
		if AllowRequest(w.origin, comString(req, 3), w.network, w.allowedHosts) {
			return 0
		}
		var response uintptr
		v := com(w.env, 4, 0, 403, wide("Forbidden"), wide("Content-Type: text/plain\r\n"), ptr(&response))
		if hr(v) != nil {
			return v
		}
		defer release(response)
		return com(args, 5, response)
	}); e != nil {
		return e
	}
	return nil
}

func (w *winView) configureNavigation(l *winLoadState, transparent bool) error {
	var e error
	if e = addWebHandler(w.wv, 15, iidNavigationCompletedHandler, func(_, args uintptr) uintptr {
		var success int32
		if hr(com(args, 3, ptr(&success))) != nil || success == 0 {
			w.navigationErr = fmt.Errorf("WebView2 navigation failed")
		}
		w.ready = true
		_ = w.Script(w.props)
		s := authorizedFPS(w.fps, w.token)
		_ = w.Script(s)
		return 0
	}); e != nil {
		return e
	}
	// Deny audio natively, including WebAudio, via ICoreWebView2_8.
	audio, e := query(w.wv, "{E9632730-6E1E-43AB-B7B8-7B2C9E62E094}")
	if e != nil {
		return e
	}
	l.refs = append(l.refs, audio)
	if e = hr(com(audio, 84, 1)); e != nil {
		return e
	}
	if transparent {
		ctl2, e := query(w.ctl, "{C979903E-D4CA-4228-92EB-47EE3FA96EAB}")
		if e != nil {
			return e
		}
		e = hr(com(ctl2, 27, 0))
		release(ctl2)
		if e != nil {
			return e
		}
	}
	return nil
}

func (w *winView) navigateInitial(l *winLoadState, x loadRequest, m *wpk.Manifest) error {
	var e error
	l.done = false
	l.asyncErr = nil
	h := handler(iidAddScriptCompletedHandler, func(result, _ uintptr) uintptr { l.asyncErr = hr(result); l.done = true; return 0 })
	l.refs = append(l.refs, h)
	if e = hr(com(w.wv, 27, wide(bootstrapScript(w.token, w.fps, w.network)), h)); e != nil {
		return e
	}
	if e = await(&l.done); e != nil {
		return e
	}
	if l.asyncErr != nil {
		return l.asyncErr
	}
	bounds := struct{ L, T, R, B int32 }{0, 0, int32(x.Bounds.Dx()), int32(x.Bounds.Dy())}
	if e = hr(com(w.ctl, 6, ptr(&bounds))); e != nil {
		return e
	}
	if e = hr(com(w.wv, 5, wide(entryURL(x, m, "windows")))); e != nil {
		return e
	}
	if e = await(&w.ready); e != nil {
		return e
	}
	if w.navigationErr != nil {
		return w.navigationErr
	}
	return w.Pause(x.Paused)
}

func (w *winView) Script(s string) error { return hr(com(w.wv, 29, wide(s), 0)) }
func (w *winView) Pause(paused bool) error {
	if w.paused == paused {
		return nil
	}
	if err := pauseNative(w, paused); err != nil {
		return err
	}
	w.paused = paused
	if !paused {
		w.clearSnapshot()
		if err := w.Script(w.props); err != nil {
			return err
		}
		return w.Script(authorizedFPS(w.fps, w.token))
	}
	return nil
}
func (w *winView) Capture() error { return w.capture() }
func (w *winView) Visible(on bool) error {
	value := uintptr(0)
	if on {
		value = 1
	}
	return hr(com(w.ctl, 4, value))
}
func (w *winView) Resume() error { return hr(com(w.wv3, 69)) }
func (w *winView) Suspend() error {
	done := false
	var err error
	// TrySuspend returning FALSE (ok == 0) is a benign soft decline; do not fail.
	h := handler(iidTrySuspendCompletedHandler, func(result, _ uintptr) uintptr {
		err = hr(result)
		done = true
		return 0
	})
	defer release(h)
	if e := hr(com(w.wv3, 68, h)); e != nil {
		return e
	}
	if e := await(&done); e != nil {
		return e
	}
	return err
}
func (w *winView) Close() {
	w.clearSnapshot()
	if w.ctl != 0 {
		com(w.ctl, 24)
	}
	for _, o := range []uintptr{w.wv4, w.wv3, w.wv, w.ctl, w.env} {
		release(o)
	}
	w.wv4, w.wv3, w.wv, w.ctl, w.env = 0, 0, 0, 0, 0
	if w.userData != nil {
		w.userData.Close()
		w.userData = nil
	}
}

func (h *hostSession) activeSignals() bool {
	return h.loaded && !h.paused && !h.blocked && h.signals != nil && (h.signals.Audio || h.signals.Input)
}

func (h *hostSession) signalWaitTimeoutMs() uintptr {
	if !h.activeSignals() {
		return 0xFFFFFFFF // INFINITE
	}
	fps := h.fps
	if fps <= 0 {
		fps = 30
	}
	ms := 1000 / fps
	if ms < 4 {
		ms = 4
	}
	if ms > 100 {
		ms = 100
	}
	return uintptr(ms)
}

func runHost(p pipeIO) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Match desktop.Bounds physical pixels before creating any native window.
	dpi := user.NewProc("SetThreadDpiAwarenessContext")
	if e := dpi.Find(); e != nil {
		return fmt.Errorf("per-monitor DPI awareness unavailable: %w", e)
	}
	previous, _, dpiErr := dpi.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	if previous == 0 {
		return fmt.Errorf("set renderer DPI awareness: %w", dpiErr)
	}
	defer dpi.Call(previous)

	v, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if e := hr(v); e != nil {
		return e
	}
	defer ole.NewProc("CoUninitialize").Call()

	wakeEvent, _, wakeErr := procCreateEventW.Call(0, 0, 0, 0)
	if wakeEvent == 0 {
		return fmt.Errorf("web: create wake event: %w", wakeErr)
	}
	defer procCloseHandle.Call(wakeEvent)
	wake := func() { procSetEvent.Call(wakeEvent) }

	w := &winView{onFatal: wake}
	defer w.Close()
	h := &hostSession{view: w}
	defer h.closeSignals()

	hostDone := make(chan struct{})
	defer close(hostDone)

	jobs := make(chan func(), 16)
	c := ipc.New(p, func(m string, b json.RawMessage) (any, *ipc.Error) {
		var result any
		var e *ipc.Error
		done := make(chan struct{})
		select {
		case jobs <- func() { result, e = h.handle(m, b); close(done) }:
			wake()
		case <-hostDone:
			return nil, &ipc.Error{Code: "E_STATE", Msg: "web host shutting down", Fatal: true}
		}
		select {
		case <-done:
			return result, e
		case <-hostDone:
			return nil, &ipc.Error{Code: "E_STATE", Msg: "web host shutting down", Fatal: true}
		}
	}, nil)
	defer c.Close()

	// runHost owns this watcher; it exits when IPC closes or runHost returns.
	go func() {
		select {
		case <-c.Done():
			wake()
		case <-hostDone:
		}
	}()

	for {
		// Drain pending control jobs before waiting.
		for draining := true; draining; {
			select {
			case <-c.Done():
				return nil
			case job := <-jobs:
				job()
				nativeRoots = nil
			default:
				draining = false
			}
		}
		if w.processFailed != nil {
			return w.processFailed
		}
		pump()
		if w.processFailed != nil {
			return w.processFailed
		}
		h.signalFrame(time.Now())
		h.reportSignalStatus(c)
		nativeRoots = nil

		select {
		case <-c.Done():
			return nil
		default:
		}
		if len(jobs) > 0 {
			continue
		}
		waitHandle := wakeEvent
		procMsgWaitObjects.Call(1, uintptr(unsafe.Pointer(&waitHandle)), 0, h.signalWaitTimeoutMs(), 0x04FF) // QS_ALLINPUT
	}
}

func (v *winView) Properties(s string) error { v.props = s; return v.Script(s) }
func (v *winView) FPS(fps int) error {
	v.fps = fps
	return v.Script(authorizedFPS(fps, v.token))
}
