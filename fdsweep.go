package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// File type bits, mirroring Linux stat(2) st_mode.
const (
	ModeTypeMask    uint32 = 0o170000
	ModeFIFO        uint32 = 0o010000
	ModeCharDevice  uint32 = 0o020000
	ModeDirectory   uint32 = 0o040000
	ModeBlockDevice uint32 = 0o060000
	ModeRegular     uint32 = 0o100000
	ModeSymlink     uint32 = 0o120000
	ModeSocket      uint32 = 0o140000
)

// FlagOPath is the Linux O_PATH status flag as reported by
// /proc/self/fdinfo. Descriptors opened with O_PATH bypass normal
// read/write checks and must never survive into a privileged helper.
const FlagOPath = 0o10000000

// AbsoluteMaxFD bounds descriptor sweeps when Policy.MaxFD is unset.
const AbsoluteMaxFD = 1 << 20

// ErrRejected is returned by Sweep when any descriptor violates the policy.
var ErrRejected = errors.New("netsec: descriptor policy violated")

// Decision is the verdict for a single descriptor.
type Decision int

const (
	// DecisionKeep leaves the descriptor open.
	DecisionKeep Decision = iota
	// DecisionClose closes the descriptor before the helper proceeds.
	DecisionClose
	// DecisionReject aborts startup; the descriptor is dangerous or a
	// standard stream was replaced.
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
	default:
		return "unknown"
	}
}

// FileStat carries the stat(2) fields the policy needs.
type FileStat struct {
	Mode uint32 // permission and file-type bits
	Rdev uint64 // device number of the node the descriptor refers to
}

// Type returns the file-type bits of Mode.
func (s FileStat) Type() uint32 { return s.Mode & ModeTypeMask }

// FDInfo is a snapshot of one descriptor gathered through Inspector.
type FDInfo struct {
	FD     int
	Target string   // readlink of /proc/self/fd/N
	Flags  int      // status flags from /proc/self/fdinfo/N
	Stat   FileStat // fstat of the descriptor
}

// Inspector abstracts every probe the sweep needs so callers (and tests)
// can inject procfs, a sandbox, or a fake.
type Inspector interface {
	// Enum lists all open descriptor numbers of the process.
	Enum() ([]int, error)
	// Readlink resolves /proc/self/fd/fd.
	Readlink(fd int) (string, error)
	// Flags returns the descriptor status flags (O_PATH, O_APPEND, ...).
	Flags(fd int) (int, error)
	// Fstat stats the open descriptor itself.
	Fstat(fd int) (FileStat, error)
}

// Closer closes a descriptor. Injected so tests can observe closes.
type Closer interface {
	Close(fd int) error
}

// StdExpectation describes the device a standard stream must be attached
// to. The zero value matches nothing, so unconfigured streams are
// rejected.
type StdExpectation struct {
	ModeType uint32 // expected file-type bits, e.g. ModeCharDevice
	Rdev     uint64 // expected device number, e.g. of /dev/null
}

// AllowRule whitelists an inherited descriptor above fd 2. Both set
// fields must match; the zero rule matches everything and should be
// avoided outside tests.
type AllowRule struct {
	TargetPrefix string // readlink target prefix, e.g. "pipe:[" or "/var/log/"
	ModeType     uint32 // required file-type bits, 0 means any
}

func (r AllowRule) matches(info FDInfo) bool {
	if r.ModeType != 0 && info.Stat.Type() != r.ModeType {
		return false
	}
	return strings.HasPrefix(info.Target, r.TargetPrefix)
}

// Policy decides the fate of every inherited descriptor.
type Policy struct {
	// Std holds the expected device for fd 0, 1 and 2.
	Std [3]StdExpectation
	// MaxFD is the exclusive upper bound of plausible descriptors;
	// anything at or above it is out of range and rejected. Zero uses
	// AbsoluteMaxFD.
	MaxFD int
	// Allow whitelists descriptors above fd 2. Dangerous descriptors
	// (O_PATH directories, sockets, pidfds) are rejected before the
	// whitelist is consulted.
	Allow []AllowRule
}

func (p Policy) maxFD() int {
	if p.MaxFD > 0 {
		return p.MaxFD
	}
	return AbsoluteMaxFD
}

// Decide classifies one descriptor. It is pure: all probing happens in
// Inspector before Decide is called.
func (p Policy) Decide(info FDInfo) Decision {
	if info.FD < 0 || info.FD >= p.maxFD() {
		return DecisionReject
	}
	if dangerous(info) {
		return DecisionReject
	}
	if info.FD >= 0 && info.FD <= 2 {
		exp := p.Std[info.FD]
		if info.Stat.Type() != exp.ModeType || info.Stat.Rdev != exp.Rdev {
			return DecisionReject
		}
		return DecisionKeep
	}
	for _, rule := range p.Allow {
		if rule.matches(info) {
			return DecisionKeep
		}
	}
	return DecisionClose
}

// dangerous reports descriptors that are never acceptable in a
// privileged helper, regardless of the whitelist.
func dangerous(info FDInfo) bool {
	return isOPathDir(info) || isSocket(info) || isPidfd(info)
}

func isOPathDir(info FDInfo) bool {
	return info.Flags&FlagOPath != 0 && info.Stat.Type() == ModeDirectory
}

// isSocket treats every S_IFSOCK descriptor as dangerous: the socket
// family (unix vs inet) cannot be determined from the descriptor alone,
// and unix sockets are trivially abused to reach privileged daemons.
func isSocket(info FDInfo) bool {
	return info.Stat.Type() == ModeSocket || strings.HasPrefix(info.Target, "socket:[")
}

func isPidfd(info FDInfo) bool {
	return info.Target == "anon_inode:[pidfd]"
}

// explain produces the audit-trail reason for a decision.
func (p Policy) explain(info FDInfo, d Decision) string {
	switch d {
	case DecisionKeep:
		if info.FD <= 2 {
			return "standard stream matches expected device"
		}
		return "matched whitelist"
	case DecisionClose:
		return "not whitelisted"
	case DecisionReject:
		return p.rejectReason(info)
	default:
		return "unknown decision"
	}
}

func (p Policy) rejectReason(info FDInfo) string {
	switch {
	case info.FD < 0 || info.FD >= p.maxFD():
		return fmt.Sprintf("descriptor %d out of range [0,%d)", info.FD, p.maxFD())
	case isOPathDir(info):
		return "O_PATH directory"
	case isSocket(info):
		return "socket descriptor"
	case isPidfd(info):
		return "pidfd descriptor"
	case info.FD <= 2:
		return "standard stream replaced: unexpected device"
	default:
		return "rejected by policy"
	}
}

// Entry records the verdict for one descriptor.
type Entry struct {
	FD       int
	Info     FDInfo
	Decision Decision
	Reason   string
	Err      error // inspect or close failure, if any
}

// Report is the audit trail of one Sweep.
type Report struct {
	Entries  []Entry
	Fallback bool  // enumeration failed; range [3,MaxFD) was closed blindly
	EnumErr  error // the enumeration error that triggered the fallback
}

func (r *Report) add(e Entry) { r.Entries = append(r.Entries, e) }

// Rejected reports whether any entry was rejected.
func (r Report) Rejected() bool {
	for _, e := range r.Entries {
		if e.Decision == DecisionReject {
			return true
		}
	}
	return false
}

// Failed reports whether the sweep could not enforce the policy: a
// rejection, or a close that did not succeed.
func (r Report) Failed() bool {
	for _, e := range r.Entries {
		if e.Decision == DecisionReject {
			return true
		}
		if e.Decision == DecisionClose && e.Err != nil {
			return true
		}
	}
	return false
}

func inspectFD(insp Inspector, fd int) (FDInfo, error) {
	info := FDInfo{FD: fd}
	var err error
	if info.Target, err = insp.Readlink(fd); err != nil {
		return info, err
	}
	if info.Flags, err = insp.Flags(fd); err != nil {
		return info, err
	}
	if info.Stat, err = insp.Fstat(fd); err != nil {
		return info, err
	}
	return info, nil
}

// Sweep audits every inherited descriptor and enforces the policy.
// Standard streams are always verified first. If enumeration fails the
// sweep falls back to blindly closing [3, MaxFD): an uninspectable
// descriptor can never be kept. It returns ErrRejected when the policy
// could not be fully enforced; the Report always describes what happened.
func Sweep(insp Inspector, closer Closer, pol Policy) (Report, error) {
	rep := Report{}

	for fd := 0; fd <= 2; fd++ {
		info, err := inspectFD(insp, fd)
		if err != nil {
			rep.add(Entry{FD: fd, Info: info, Decision: DecisionReject,
				Reason: "standard stream not inspectable", Err: err})
			continue
		}
		d := pol.Decide(info)
		rep.add(Entry{FD: fd, Info: info, Decision: d, Reason: pol.explain(info, d)})
	}

	fds, err := insp.Enum()
	if err != nil {
		rep.Fallback = true
		rep.EnumErr = err
		for fd := 3; fd < pol.maxFD(); fd++ {
			if cerr := closer.Close(fd); cerr == nil {
				rep.add(Entry{FD: fd, Decision: DecisionClose,
					Reason: "enumeration failed; closed blindly"})
			}
		}
	} else {
		for _, fd := range fds {
			if fd >= 0 && fd <= 2 {
				continue // standard streams already handled
			}
			info, ierr := inspectFD(insp, fd)
			if ierr != nil {
				// Uninspectable: Decide on the bare number, which
				// rejects out-of-range fds and closes the rest.
				d := pol.Decide(FDInfo{FD: fd})
				e := Entry{FD: fd, Decision: d,
					Reason: "inspect failed: " + ierr.Error(), Err: ierr}
				if d == DecisionClose {
					e.Err = closer.Close(fd)
				}
				rep.add(e)
				continue
			}
			d := pol.Decide(info)
			e := Entry{FD: fd, Info: info, Decision: d, Reason: pol.explain(info, d)}
			if d == DecisionClose {
				e.Err = closer.Close(fd)
			}
			rep.add(e)
		}
	}

	if rep.Failed() {
		return rep, ErrRejected
	}
	return rep, nil
}
