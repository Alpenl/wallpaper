//go:build windows

// Job lifetime and the temporary mpv named-pipe transport.
package win

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	pipeKernel32 = syscall.NewLazyDLL("kernel32.dll")

	pCreateJobObjectW         = pipeKernel32.NewProc("CreateJobObjectW")
	pSetInformationJobObject  = pipeKernel32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObject = pipeKernel32.NewProc("AssignProcessToJobObject")
	pGetCurrentProcess        = pipeKernel32.NewProc("GetCurrentProcess")
	pWaitNamedPipeW           = pipeKernel32.NewProc("WaitNamedPipeW")
)

// U16 转 UTF-16 指针。
func U16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// UP 把 *uint16 转成 uintptr。
func UP(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

// SetupJob 让本进程加入一个 KILL_ON_JOB_CLOSE 作业（02 章 2.2）：之后启动的 mpv / mfhost 子进程自动继承，
// 本进程退出或被杀时它们随之结束。失败必须阻止启动。
func SetupJob() error {
	job, _, err := pCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return fmt.Errorf("CreateJobObject: %v", err)
	}
	var info [144]byte // JOBOBJECT_EXTENDED_LIMIT_INFORMATION（64 位 144 字节，LimitFlags 偏移 16）
	if unsafe.Sizeof(uintptr(0)) == 4 {
		syscall.CloseHandle(syscall.Handle(job))
		return fmt.Errorf("32-bit not supported")
	}
	*(*uint32)(unsafe.Pointer(&info[16])) = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if r, _, err := pSetInformationJobObject.Call(job, 9, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info))); r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return fmt.Errorf("SetInformationJobObject: %v", err)
	}
	self, _, _ := pGetCurrentProcess.Call()
	if r, _, err := pAssignProcessToJobObject.Call(job, self); r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	return nil // 作业句柄有意不关闭：关闭即结束全部进程
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
