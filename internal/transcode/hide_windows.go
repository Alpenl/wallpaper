//go:build windows

package transcode

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32DLL               = syscall.NewLazyDLL("kernel32.dll")
	pCreateJobObjectW         = kernel32DLL.NewProc("CreateJobObjectW")
	pSetInformationJobObject  = kernel32DLL.NewProc("SetInformationJobObject")
	pAssignProcessToJobObject = kernel32DLL.NewProc("AssignProcessToJobObject")
	pTerminateJobObject       = kernel32DLL.NewProc("TerminateJobObject")
)

const (
	jobObjectExtendedLimitInfoClass = 9
	jobObjectLimitKillOnJobClose    = 0x00002000
	processSetQuota                 = 0x0100
	processTerminate                = 0x0001
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func createKillOnCloseJob() syscall.Handle {
	job, _, _ := pCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return 0
	}
	var info jobObjectExtendedLimitInformation
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, _ := pSetInformationJobObject.Call(
		job,
		jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return 0
	}
	return syscall.Handle(job)
}

func assignProcessToJob(job syscall.Handle, pid int) bool {
	if job == 0 || pid <= 0 {
		return false
	}
	proc, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(proc)
	r, _, _ := pAssignProcessToJobObject.Call(uintptr(job), uintptr(proc))
	return r != 0
}

// runCommand binds the spawned subprocess to a KILL_ON_JOB_CLOSE Job Object so
// cancelling a Scoop/WinGet shim kills the entire process tree including
// grandchild ffmpeg.exe processes immediately.
func runCommand(ctx context.Context, stdout, stderr io.Writer, path string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(path, args...)
	hide(cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second

	job := createKillOnCloseJob()
	if err := cmd.Start(); err != nil {
		if job != 0 {
			_ = syscall.CloseHandle(job)
		}
		return err
	}
	assigned := assignProcessToJob(job, cmd.Process.Pid)

	var closeOnce sync.Once
	killTree := func() {
		closeOnce.Do(func() {
			if job != 0 {
				if assigned {
					_, _, _ = pTerminateJobObject.Call(uintptr(job), 1)
				}
				_ = syscall.CloseHandle(job)
			}
			if !assigned && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}

	done := make(chan struct{})
	watchDone := make(chan struct{})
	// Goroutine owned by runCommand; exits on ctx cancellation or done close, joined below.
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			killTree()
		case <-done:
		}
	}()

	err := cmd.Wait()
	close(done)
	<-watchDone
	killTree()
	return err
}
