//go:build windows

package netsec

import (
	"unsafe"

	"syscall"
)

// JOBOBJECTINFOCLASS values used here.
const (
	jobObjectExtendedLimitInformation = 9
	jobObjectBasicUIRestrictions      = 4
)

// JOB_OBJECT_LIMIT_* flags placed in JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
const (
	jobObjectLimitActiveProcess   = 0x00000008
	jobObjectLimitJobMemory       = 0x00000200
	jobObjectLimitBreakawayOK     = 0x00000400
	jobObjectLimitSilentBreakaway = 0x00001000
	jobObjectLimitKillOnClose     = 0x00002000
)

// Process creation/access flags.
const (
	processCreateSuspended = 0x00000004
	processTerminate       = 0x0001
	waitInfinite           = 0xFFFFFFFF
)

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobBasicLimitInfo struct {
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

type jobExtendedLimitInfo struct {
	BasicLimitInformation jobBasicLimitInfo
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type securityAttributes struct {
	Length             uint32
	SecurityDescriptor uintptr
	InheritHandle      uint32
}

type startupInfo struct {
	Cb            uint32
	Reserved      *uint16
	Desktop       *uint16
	Title         *uint16
	X             uint32
	Y             uint32
	XSize         uint32
	YSize         uint32
	XCountChars   uint32
	YCountChars   uint32
	FillAttribute uint32
	Flags         uint32
	ShowWindow    uint16
	Reserved2     uint16
	Reserved2Ptr  *byte
	StdInput      uintptr
	StdOutput     uintptr
	StdError      uintptr
}

type processInformation struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessId uint32
	ThreadId  uint32
}

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject          = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procCreateProcess            = kernel32.NewProc("CreateProcessW")
	procResumeThread             = kernel32.NewProc("ResumeThread")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procWaitForSingleObject      = kernel32.NewProc("WaitForSingleObject")
	procTerminateProcess         = kernel32.NewProc("TerminateProcess")
)

// winJob is the concrete platformJob: a handle to a kernel job object whose
// limits mirror the platform-neutral Limits stored on Job.
type winJob struct {
	handle syscall.Handle
}

// NewWindowsJob validates the sandbox policy (both breakaways disabled and
// KILL_ON_JOB_CLOSE enabled), creates a kernel job object and applies the
// requested limits to it.
func NewWindowsJob(limits Limits) (*Job, error) {
	if err := ValidateSandboxPolicy(limits); err != nil {
		return nil, err
	}

	handle, _, errno := procCreateJobObject.Call(0, 0)
	if handle == 0 {
		return nil, errno
	}

	job := NewJob(limits)
	job.plat = winJob{handle: syscall.Handle(handle)}
	if err := job.applyLimits(limits); err != nil {
		procCloseHandle.Call(handle)
		return nil, err
	}
	return job, nil
}

func (j *Job) applyLimits(limits Limits) error {
	win := j.plat.(winJob)

	info := jobExtendedLimitInfo{
		JobMemoryLimit: uintptr(limits.MaxJobMemory),
	}
	info.BasicLimitInformation.ActiveProcessLimit = limits.MaxActiveProcesses

	if limits.MaxActiveProcesses != 0 {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitActiveProcess
	}
	if limits.MaxJobMemory != 0 {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitJobMemory
	}
	if limits.BreakawayOK {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitBreakawayOK
	}
	if limits.SilentBreakawayOK {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitSilentBreakaway
	}
	if limits.KillOnJobClose {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitKillOnClose
	}

	r1, _, errno := procSetInformationJobObject.Call(
		uintptr(win.handle),
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r1 == 0 {
		return errno
	}

	if limits.UI != 0 {
		ui := uint32(limits.UI)
		r1, _, errno = procSetInformationJobObject.Call(
			uintptr(win.handle),
			jobObjectBasicUIRestrictions,
			uintptr(unsafe.Pointer(&ui)),
			unsafe.Sizeof(ui),
		)
		if r1 == 0 {
			return errno
		}
	}
	return nil
}

// Close releases the kernel job handle. With KILL_ON_JOB_CLOSE set, closing
// the last handle terminates every remaining member process.
func (j *Job) Close() error {
	j.mu.Lock()
	win, ok := j.plat.(winJob)
	if !ok || win.handle == 0 {
		j.mu.Unlock()
		return nil
	}
	r1, _, errno := procCloseHandle.Call(uintptr(win.handle))
	if r1 == 0 {
		j.mu.Unlock()
		return errno
	}
	win.handle = 0
	j.plat = win
	j.mu.Unlock()
	return nil
}

// Process is a created child process and its primary thread.
type Process struct {
	job     *Job
	process syscall.Handle
	thread  syscall.Handle
	pid     uint32
	exited  chan struct{}
}

// PID returns the child process identifier.
func (p *Process) PID() uint32 { return p.pid }

// Start runs the admission check, creates the requested process (suspended so
// it can be assigned before executing), attaches it to the job and resumes it.
// A failed admission rejects the create up front; a failed system call rolls
// the reservation back. Over-cap requests return ErrProcessLimitExceeded
// without creating anything.
func (j *Job) Start(commandLine string, flags CreateFlags) (*Process, error) {
	breakaway, err := j.AdmitCreate(flags)
	if err != nil {
		return nil, err
	}

	cmdLine, err := syscall.UTF16PtrFromString(commandLine)
	if err != nil {
		j.rollback(breakaway)
		return nil, err
	}

	creationFlags := uint32(flags) | processCreateSuspended
	si := startupInfo{Cb: uint32(unsafe.Sizeof(startupInfo{}))}
	var pi processInformation

	r1, _, errno := procCreateProcess.Call(
		0,
		uintptr(unsafe.Pointer(cmdLine)),
		0,
		0,
		0,
		uintptr(creationFlags),
		0,
		0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if r1 == 0 {
		j.rollback(breakaway)
		return nil, errno
	}

	if !breakaway {
		win := j.plat.(winJob)
		r1, _, errno = procAssignProcessToJobObject.Call(
			uintptr(win.handle),
			uintptr(pi.Process),
		)
		if r1 == 0 {
			procTerminateProcess.Call(uintptr(pi.Process), 1)
			procCloseHandle.Call(uintptr(pi.Thread))
			procCloseHandle.Call(uintptr(pi.Process))
			j.rollback(false)
			return nil, errno
		}
	}

	procResumeThread.Call(uintptr(pi.Thread))

	p := &Process{
		job:     j,
		process: pi.Process,
		thread:  pi.Thread,
		pid:     pi.ProcessId,
		exited:  make(chan struct{}),
	}

	if !breakaway {
		go p.waitForExit()
	} else {
		// Nothing was reserved; the breakaway child manages its own handles.
		procCloseHandle.Call(uintptr(pi.Thread))
	}
	return p, nil
}

func (j *Job) rollback(breakaway bool) {
	if !breakaway {
		j.Release()
	}
}

func (p *Process) waitForExit() {
	procWaitForSingleObject.Call(uintptr(p.process), waitInfinite)
	close(p.exited)
	procCloseHandle.Call(uintptr(p.thread))
	procCloseHandle.Call(uintptr(p.process))
	p.job.Commit()
}

// Wait blocks until the process exits (or returns immediately for breakaways,
// which are not tracked against the job).
func (p *Process) Wait() {
	if p.exited != nil {
		<-p.exited
	}
}

// Kill terminates the process with exit code 1.
func (p *Process) Kill() error {
	r1, _, errno := procTerminateProcess.Call(uintptr(p.process), 1)
	if r1 == 0 {
		return errno
	}
	return nil
}
