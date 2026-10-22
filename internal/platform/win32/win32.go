//go:build windows

// Package win32 放 Windows 的 syscall 绑定（02 章 2.6：platform/* 只放 FFI，不放逻辑）。
// 例外：desktop/win 与 render/mf 为了复用原型代码，仍各自声明少量 proc（见 README“与 02 章的偏差”）。
package win32

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	pSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	pGetCurrentProcess        = kernel32.NewProc("GetCurrentProcess")
	pIsProcessInJob           = kernel32.NewProc("IsProcessInJob")
	pCreateNamedPipeW         = kernel32.NewProc("CreateNamedPipeW")
	pConnectNamedPipe         = kernel32.NewProc("ConnectNamedPipe")
	pCreateEventW             = kernel32.NewProc("CreateEventW")
	pGetOverlappedResult      = kernel32.NewProc("GetOverlappedResult")
	pWaitNamedPipeW           = kernel32.NewProc("WaitNamedPipeW")
	pProcessIdToSessionId     = kernel32.NewProc("ProcessIdToSessionId")
	pLocalFree                = kernel32.NewProc("LocalFree")
	pRegisterAppRestart       = kernel32.NewProc("RegisterApplicationRestart")
	pConvertSDDL              = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

// U16 转 UTF-16 指针。
func U16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// UP 把 *uint16 转成 uintptr。
func UP(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

// SetupJob 让本进程加入一个 KILL_ON_JOB_CLOSE 作业（02 章 2.2）：之后启动的 mpv / mfhost 子进程自动继承，
// 本进程退出或被杀时它们随之结束。返回的错误只用于记录（例如已在不允许嵌套的作业里）。
func SetupJob() error {
	job, _, err := pCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return fmt.Errorf("CreateJobObject: %v", err)
	}
	var info [144]byte // JOBOBJECT_EXTENDED_LIMIT_INFORMATION（64 位 144 字节，LimitFlags 偏移 16）
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return fmt.Errorf("32-bit not supported")
	}
	*(*uint32)(unsafe.Pointer(&info[16])) = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if r, _, err := pSetInformationJobObject.Call(job, 9, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info))); r == 0 {
		return fmt.Errorf("SetInformationJobObject: %v", err)
	}
	self, _, _ := pGetCurrentProcess.Call()
	if r, _, err := pAssignProcessToJobObject.Call(job, self); r == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	return nil // 作业句柄有意不关闭：关闭即结束全部进程
}

// InJob 报告本进程是否在作业里。
func InJob() bool {
	var r int32
	pIsProcessInJob.Call(^uintptr(0), 0, uintptr(unsafe.Pointer(&r)))
	return r != 0
}

// RegisterRestart 调用 RegisterApplicationRestart（2.5 第 3 条，交给 WER 在崩溃/无响应后重启）。
func RegisterRestart(args string) error {
	r, _, _ := pRegisterAppRestart.Call(UP(U16(args)), 0)
	if r != 0 {
		return fmt.Errorf("RegisterApplicationRestart hr=0x%x", r)
	}
	return nil
}

func currentUserSID() (string, error) {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String()
}

// PipeServer 是单实例命名管道服务端。
type PipeServer struct {
	Name string
	h    syscall.Handle
}

// NewPipeServer 按 02 章 2.3：\\.\pipe\wpd-<会话ID>-<16 字节随机 hex>，FIRST_PIPE_INSTANCE，DACL 只允许当前用户 SID。
func NewPipeServer() (*PipeServer, error) {
	var sess uint32
	pProcessIdToSessionId.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&sess)))
	rb := make([]byte, 16)
	_, _ = rand.Read(rb)
	name := fmt.Sprintf(`\\.\pipe\wpd-%d-%s`, sess, hex.EncodeToString(rb))
	var sa *syscall.SecurityAttributes
	if sid, err := currentUserSID(); err == nil {
		var sd uintptr
		sddl := "D:P(A;;GA;;;" + sid + ")"
		if r, _, _ := pConvertSDDL.Call(UP(U16(sddl)), 1, uintptr(unsafe.Pointer(&sd)), 0); r != 0 {
			defer pLocalFree.Call(sd)
			sa = &syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: sd}
		}
	}
	h, _, err := pCreateNamedPipeW.Call(UP(U16(name)), 0x3|0x00080000|syscall.FILE_FLAG_OVERLAPPED /*DUPLEX|FIRST_PIPE_INSTANCE|OVERLAPPED*/, 0, 1, 64<<10, 64<<10, 0,
		uintptr(unsafe.Pointer(sa)))
	if syscall.Handle(h) == syscall.InvalidHandle {
		return nil, fmt.Errorf("CreateNamedPipe: %v", err)
	}
	return &PipeServer{Name: name, h: syscall.Handle(h)}, nil
}

// Accept 等待客户端连接。连接完成前由本方法管理 OVERLAPPED；完成后交给 Go IOCP。
// Accept and Close must not be called concurrently.
func (p *PipeServer) Accept(timeout time.Duration) (*os.File, error) {
	h := p.h
	connected := false
	defer func() {
		if !connected {
			p.Close()
		}
	}()
	ev, _, err := pCreateEventW.Call(0, 1, 0, 0)
	if ev == 0 {
		return nil, fmt.Errorf("CreateEvent: %v", err)
	}
	defer syscall.CloseHandle(syscall.Handle(ev))
	ov := syscall.Overlapped{HEvent: syscall.Handle(ev)}
	r, _, err := pConnectNamedPipe.Call(uintptr(h), uintptr(unsafe.Pointer(&ov)))
	if r == 0 && err != syscall.Errno(535) /* ERROR_PIPE_CONNECTED */ {
		if err != syscall.ERROR_IO_PENDING {
			return nil, fmt.Errorf("ConnectNamedPipe: %v", err)
		}
		// Round up and avoid INFINITE (0xffffffff) even for very large durations.
		ms := int64(0)
		if timeout > 0 {
			ms = int64((timeout-1)/time.Millisecond) + 1
		}
		if ms > 0xfffffffe {
			ms = 0xfffffffe
		}
		status, waitErr := syscall.WaitForSingleObject(syscall.Handle(ev), uint32(ms))
		if waitErr != nil || status != syscall.WAIT_OBJECT_0 {
			// The kernel must release ov before its memory or event can be freed.
			_ = syscall.CancelIoEx(h, &ov)
			var transferred uint32
			pGetOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&transferred)), 1)
			if status == syscall.WAIT_TIMEOUT {
				return nil, fmt.Errorf("E_TIMEOUT: client did not connect within %v", timeout)
			}
			return nil, fmt.Errorf("WaitForSingleObject: status=%d: %v", status, waitErr)
		}
		var transferred uint32
		if r, _, err := pGetOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&transferred)), 0); r == 0 {
			return nil, fmt.Errorf("ConnectNamedPipe completion: %v", err)
		}
	}
	connected = true
	p.h = syscall.InvalidHandle // ownership transfers to the returned file
	return os.NewFile(uintptr(h), p.Name), nil
}

// Close 关闭未被 Accept 的管道。
func (p *PipeServer) Close() {
	if p.h != syscall.InvalidHandle {
		syscall.CloseHandle(p.h)
		p.h = syscall.InvalidHandle
	}
}

// DialPipe 以客户端身份连接命名管道（mpv 的 --input-ipc-server 由 mpv 创建），最多等 timeout。
func DialPipe(name string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		pWaitNamedPipeW.Call(UP(U16(name)), 100)
		h, err := syscall.CreateFile(U16(name), syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			return os.NewFile(uintptr(h), name), nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("pipe %s: %v", name, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
