package netsec

import "sync"

// CreateFlags mirrors the dwCreationFlags values relevant to job membership.
type CreateFlags uint32

const (
	// CreateBreakawayFromJob is CREATE_BREAKAWAY_FROM_JOB. The child leaves
	// the job explicitly; creation fails if the job does not allow breakaway.
	CreateBreakawayFromJob CreateFlags = 0x01000000
	// CreateSilentBreakawayFromJob is CREATE_SILENT_BREAKAWAY_FROM_JOB. The
	// child is never associated with the job.
	CreateSilentBreakawayFromJob CreateFlags = 0x00020000
	// CreateSuspended is CREATE_SUSPENDED, used to assign a process to a job
	// before its primary thread starts.
	CreateSuspended CreateFlags = 0x00000004
)

// platformJob holds the OS-specific kernel job handle. It is an empty
// winJob on Windows and an empty struct on other platforms.
type platformJob interface{}

// Job is the host-side admission object for one Windows job object. It owns
// the effective Limits and tracks the number of active member processes so a
// create request can be rejected before the process cap is exceeded. The
// platform field holds the concrete kernel handle (see job_windows.go).
type Job struct {
	mu     sync.Mutex
	limits Limits
	active uint32
	plat   platformJob
}

// NewJob builds an in-memory job with the given effective limits. On Windows
// use NewWindowsJob to additionally create and configure the kernel job
// object; that constructor enforces ValidateSandboxPolicy.
func NewJob(limits Limits) *Job {
	return &Job{limits: limits}
}

// Limits returns a copy of the currently effective limits.
func (j *Job) Limits() Limits {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.limits
}

// ActiveProcesses returns the number of member processes currently accounted
// for on the job.
func (j *Job) ActiveProcesses() uint32 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.active
}

// AdmitCreate applies the job policy to a prospective CreateProcess call.
//
// Breakaway flags are checked first against the limit set: a breakaway is only
// permitted when the matching JOB_OBJECT_LIMIT_*_BREAKAWAY_OK bit is set.
// A process that actually breaks away is not a job member, so it neither
// consumes nor is bounded by MaxActiveProcesses. For normal (and suspended,
// job-bound) creates the active counter is reserved and
// ErrProcessLimitExceeded is returned when the cap would be exceeded. Existing
// members are left untouched on rejection rather than being terminated.
//
// On success the reservation must be finalized with Commit (the process was
// created and assigned) or rolled back with Release (CreateProcess failed).
func (j *Job) AdmitCreate(flags CreateFlags) (breakaway bool, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if flags&CreateSilentBreakawayFromJob != 0 && !j.limits.SilentBreakawayOK {
		return false, ErrSilentBreakawayDenied
	}
	if flags&CreateBreakawayFromJob != 0 && !j.limits.BreakawayOK {
		return false, ErrBreakawayDenied
	}

	breakaway = flags&(CreateBreakawayFromJob|CreateSilentBreakawayFromJob) != 0
	if breakaway {
		// The child escapes the job: it does not count against the member cap.
		return true, nil
	}

	if cap := j.limits.MaxActiveProcesses; cap != 0 && j.active >= cap {
		return false, ErrProcessLimitExceeded
	}
	j.active++
	return false, nil
}

// Release rolls back a reservation made by AdmitCreate when the job-bound
// process could not actually be created. Breakaway reservations are no-ops.
func (j *Job) Release() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.active > 0 {
		j.active--
	}
}

// Commit records that a job-bound process has exited, freeing one slot. It is
// normally invoked from the process exit waiter; breakaway processes never
// consumed a slot and therefore do not call Commit.
func (j *Job) Commit() {
	j.Release()
}
