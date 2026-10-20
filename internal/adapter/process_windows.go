//go:build windows

package adapter

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Start suspended so no descendant can escape before job assignment. Closing
// the owned job kills every descendant, including children that inherited pipes.
func configureProcess(cmd *exec.Cmd) (func() error, func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, errors.New("create process job failed")
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, nil, errors.New("configure process job failed")
	}
	const createSuspended = 0x00000004
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | createSuspended}
	cmd.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	attach := func() error {
		const processSuspendResume = 0x0800
		handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|processSuspendResume, false, uint32(cmd.Process.Pid))
		if err != nil {
			return errors.New("open child process failed")
		}
		defer windows.CloseHandle(handle)
		if err := windows.AssignProcessToJobObject(job, handle); err != nil {
			return errors.New("assign child process job failed")
		}
		status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(handle))
		if status != 0 {
			return errors.New("resume child process failed")
		}
		return nil
	}
	return attach, func() { _ = windows.CloseHandle(job) }, nil
}
