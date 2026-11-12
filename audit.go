package netsec

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// OPath is the Linux O_PATH flag as reported in /proc/self/fdinfo flags.
const OPath = 0o10000000

// Decision is the audit verdict for a single descriptor.
type Decision int

const (
	// DecisionKeep leaves the descriptor open: it is whitelisted or an
	// expected standard stream.
	DecisionKeep Decision = iota
	// DecisionClose closes the descriptor: it is benign but not
	// whitelisted, so the helper must not inherit it.
	DecisionClose
	// DecisionReject refuses startup: the descriptor is dangerous or
	// violates policy (replaced standard stream, O_PATH, socket, pidfd,
	// out-of-range number).
	DecisionReject
)

func (d Decision) String() string {
	switch d {
	case DecisionKeep:
		return "keep"
	case DecisionClose:
		return "close"
	case DecisionReject:
		return "reject"
	}
	return "unknown"
}

// StatInfo carries the fstat fields the audit needs.
type StatInfo struct {
	Mode os.FileMode
	Dev  uint64
	Rdev uint64
}

// Proc abstracts every descriptor operation so callers inject either the
// real /proc-backed implementation or a test double.
type Proc interface {
	// ListFDs enumerates open descriptor numbers (e.g. /proc/self/fd).
	ListFDs() ([]int, error)
	// Readlink returns the /proc/self/fd/<fd> link target.
	Readlink(fd int) (string, error)
	// Flags returns the open file status flags (from fdinfo or fcntl).
	Flags(fd int) (uint64, error)
	// Stat fstats the descriptor.
	Stat(fd int) (StatInfo, error)
	// Close closes the descriptor.
	Close(fd int) error
}

// Policy configures the audit.
type Policy struct {
	// StdRdev is the expected device number (st_rdev) of the character
	// device backing fds 0, 1 and 2, e.g. /dev/null or the controlling
	// terminal. Any other object on a standard stream rejects startup.
	StdRdev uint64
	// KeepTargets lists exact /proc link targets that may stay open,
	// e.g. "pipe:[4242]" for a legitimate log pipe.
	KeepTargets []string
	// MaxFD is the exclusive upper bound for plausible descriptor
	// numbers; anything >= MaxFD (or negative) is rejected as
	// out-of-range. Zero selects the default.
	MaxFD int
	// FallbackMax is the inclusive ceiling for the blind-close fallback
	// used when enumeration fails. Zero selects the default.
	FallbackMax int
}

const (
	defaultMaxFD       = 1 << 20
	defaultFallbackMax = 1024
)

func (p Policy) withDefaults() Policy {
	if p.MaxFD <= 0 {
		p.MaxFD = defaultMaxFD
	}
	if p.FallbackMax <= 0 {
		p.FallbackMax = defaultFallbackMax
	}
	return p
}

// Entry records the verdict for one descriptor.
type Entry struct {
	FD       int
	Target   string
	Decision Decision
	Reason   string
}

// Report is the outcome of an Audit run.
type Report struct {
	Entries []Entry
	// Fallback is true when enumeration failed and the blind-close
	// fallback ran instead of per-descriptor classification.
	Fallback bool
}

// Rejected returns the entries that triggered rejection.
func (r *Report) Rejected() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Decision == DecisionReject {
			out = append(out, e)
		}
	}
	return out
}

// RejectError is returned by Audit when any descriptor is rejected.
type RejectError struct {
	Entries []Entry
}

func (e *RejectError) Error() string {
	parts := make([]string, 0, len(e.Entries))
	for _, ent := range e.Entries {
		parts = append(parts, fmt.Sprintf("fd %d: %s", ent.FD, ent.Reason))
	}
	return "descriptor audit rejected: " + strings.Join(parts, "; ")
}

// Audit inspects every inherited descriptor against pol, closing benign
// strays and rejecting dangerous ones. It returns a *RejectError if any
// descriptor fails policy; the Report is always populated.
func Audit(p Proc, pol Policy) (*Report, error) {
	pol = pol.withDefaults()
	rep := &Report{}

	fds, err := p.ListFDs()
	if err != nil {
		// Enumeration failed: conservatively blind-close everything
		// above stderr, then still validate the standard streams.
		rep.Fallback = true
		for fd := 3; fd <= pol.FallbackMax; fd++ {
			p.Close(fd) // EBADF expected for gaps; ignored
		}
		for fd := 0; fd < 3; fd++ {
			rep.Entries = append(rep.Entries, auditStd(p, pol, fd))
		}
		return rep, rep.err()
	}

	sort.Ints(fds)
	keep := make(map[string]bool, len(pol.KeepTargets))
	for _, t := range pol.KeepTargets {
		keep[t] = true
	}
	for _, fd := range fds {
		ent := classify(p, pol, keep, fd)
		rep.Entries = append(rep.Entries, ent)
		if ent.Decision == DecisionClose {
			p.Close(fd)
		}
	}
	return rep, rep.err()
}

func (r *Report) err() error {
	if rej := r.Rejected(); len(rej) > 0 {
		return &RejectError{Entries: rej}
	}
	return nil
}

func classify(p Proc, pol Policy, keep map[string]bool, fd int) Entry {
	if fd < 0 || fd >= pol.MaxFD {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "descriptor number out of range"}
	}
	if fd < 3 {
		return auditStd(p, pol, fd)
	}

	flags, err := p.Flags(fd)
	if err != nil {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "cannot read flags"}
	}
	if flags&OPath != 0 {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "O_PATH descriptor"}
	}

	target, err := p.Readlink(fd)
	if err != nil {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "cannot read /proc link"}
	}
	if strings.HasPrefix(target, "socket:[") {
		return Entry{FD: fd, Target: target, Decision: DecisionReject, Reason: "unix socket"}
	}
	if strings.HasPrefix(target, "anon_inode:[pidfd]") {
		return Entry{FD: fd, Target: target, Decision: DecisionReject, Reason: "pidfd"}
	}

	st, err := p.Stat(fd)
	if err != nil {
		return Entry{FD: fd, Target: target, Decision: DecisionReject, Reason: "cannot stat"}
	}
	switch {
	case st.Mode&os.ModeSocket != 0:
		return Entry{FD: fd, Target: target, Decision: DecisionReject, Reason: "socket"}
	case st.Mode&os.ModeDir != 0:
		return Entry{FD: fd, Target: target, Decision: DecisionReject, Reason: "directory"}
	}

	if keep[target] {
		return Entry{FD: fd, Target: target, Decision: DecisionKeep, Reason: "whitelisted target"}
	}
	return Entry{FD: fd, Target: target, Decision: DecisionClose, Reason: "not whitelisted"}
}

func auditStd(p Proc, pol Policy, fd int) Entry {
	st, err := p.Stat(fd)
	if err != nil {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "standard stream not stat-able"}
	}
	if st.Mode&os.ModeCharDevice == 0 || st.Rdev != pol.StdRdev {
		return Entry{FD: fd, Decision: DecisionReject, Reason: "standard stream replaced"}
	}
	return Entry{FD: fd, Decision: DecisionKeep, Reason: "expected device"}
}
