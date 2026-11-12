package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Cmd mirrors the fsconfig(2) command space.
type Cmd int

const (
	CmdSetFlag Cmd = iota
	CmdSetString
	CmdSetBinary
	CmdSetPath
	CmdSetFd
	CmdCreate
	CmdReconfigure
)

func (c Cmd) String() string {
	switch c {
	case CmdSetFlag:
		return "SET_FLAG"
	case CmdSetString:
		return "SET_STRING"
	case CmdSetBinary:
		return "SET_BINARY"
	case CmdSetPath:
		return "SET_PATH"
	case CmdSetFd:
		return "SET_FD"
	case CmdCreate:
		return "CMD_CREATE"
	case CmdReconfigure:
		return "CMD_RECONFIGURE"
	}
	return fmt.Sprintf("Cmd(%d)", int(c))
}

func (c Cmd) isSet() bool {
	switch c {
	case CmdSetFlag, CmdSetString, CmdSetBinary, CmdSetPath, CmdSetFd:
		return true
	}
	return false
}

// Command is a single fsconfig(2) invocation to validate.
type Command struct {
	Cmd   Cmd
	Key   string
	Value string // SET_STRING, SET_BINARY, SET_PATH payload
	Fd    int    // SET_FD payload
}

// MountAttr mirrors the fsmount(2) MOUNT_ATTR_* attribute bits.
type MountAttr uint64

const (
	MountAttrRdOnly MountAttr = 1 << iota
	MountAttrNoSuid
	MountAttrNoDev
	MountAttrNoExec
	MountAttrNoAtime
	MountAttrNoDirAtime
	MountAttrRelAtime
	MountAttrStrictAtime
)

// Sentinel errors so callers can distinguish rejection causes.
var (
	ErrUnknownFSType      = errors.New("fsconfig: unknown filesystem type")
	ErrOptionNotAllowed   = errors.New("fsconfig: option not in whitelist")
	ErrCommandNotAllowed  = errors.New("fsconfig: command form not allowed for option")
	ErrProtectionOverride = errors.New("fsconfig: protection option already locked")
	ErrSetAfterCreate     = errors.New("fsconfig: SET_* after CMD_CREATE")
	ErrUnregisteredFD     = errors.New("fsconfig: file descriptor not registered by caller")
	ErrReconfigureOnlyKey = errors.New("fsconfig: key only usable in reconfigure phase")
	ErrBadCommandSequence = errors.New("fsconfig: command not valid in current phase")
	ErrWeakMountAttr      = errors.New("fsconfig: fsmount attributes weaker than declared")
)

// protectionAttr maps a canonical protection key to its MOUNT_ATTR_ bit.
var protectionAttr = map[string]MountAttr{
	"nosuid": MountAttrNoSuid,
	"nodev":  MountAttrNoDev,
	"noexec": MountAttrNoExec,
}

// protectionInverse maps a negating key (suid/dev/exec) to the canonical
// protection key it would undo.
var protectionInverse = map[string]string{
	"suid": "nosuid",
	"dev":  "nodev",
	"exec": "noexec",
}

// OptionSpec describes one whitelisted option key.
type OptionSpec struct {
	// Cmds lists the fsconfig command forms permitted for this key.
	Cmds []Cmd
	// ReconfigureOnly marks keys usable only after CMD_RECONFIGURE,
	// never during the initial create phase.
	ReconfigureOnly bool
}

func (s OptionSpec) allows(c Cmd) bool {
	for _, allowed := range s.Cmds {
		if allowed == c {
			return true
		}
	}
	return false
}

// Registry holds per-filesystem option whitelists.
type Registry struct {
	mu   sync.RWMutex
	byFS map[string]map[string]OptionSpec
}

func NewRegistry() *Registry {
	return &Registry{byFS: make(map[string]map[string]OptionSpec)}
}

// Register installs or replaces the whitelist for a filesystem type.
func (r *Registry) Register(fsType string, opts map[string]OptionSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	norm := make(map[string]OptionSpec, len(opts))
	for key, spec := range opts {
		norm[normalizeKey(key)] = spec
	}
	r.byFS[strings.ToLower(fsType)] = norm
}

func (r *Registry) policy(fsType string) (map[string]OptionSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byFS[strings.ToLower(fsType)]
	return p, ok
}

func flagOnly(keys ...string) map[string]OptionSpec {
	m := make(map[string]OptionSpec, len(keys))
	for _, k := range keys {
		m[k] = OptionSpec{Cmds: []Cmd{CmdSetFlag}}
	}
	return m
}

func merge(dst, src map[string]OptionSpec) {
	for k, v := range src {
		dst[k] = v
	}
}

// DefaultRegistry returns a registry pre-populated with common filesystems.
// Runtimes are expected to extend or replace these policies.
func DefaultRegistry() *Registry {
	r := NewRegistry()

	common := flagOnly(
		"ro", "rw", "sync", "async", "dirsync",
		"relatime", "norelatime", "noatime", "strictatime",
		"nosuid", "suid", "nodev", "dev", "noexec", "exec",
	)

	proc := map[string]OptionSpec{}
	merge(proc, common)
	proc["subset"] = OptionSpec{Cmds: []Cmd{CmdSetString}}
	r.Register("proc", proc)

	sysfs := map[string]OptionSpec{}
	merge(sysfs, common)
	r.Register("sysfs", sysfs)

	tmpfs := map[string]OptionSpec{}
	merge(tmpfs, common)
	for _, k := range []string{"size", "nr_blocks", "nr_inodes", "mode", "uid", "gid", "mpol", "huge"} {
		tmpfs[k] = OptionSpec{Cmds: []Cmd{CmdSetString}}
	}
	r.Register("tmpfs", tmpfs)

	devpts := map[string]OptionSpec{}
	merge(devpts, common)
	for _, k := range []string{"uid", "gid", "mode", "ptmxmode"} {
		devpts[k] = OptionSpec{Cmds: []Cmd{CmdSetString}}
	}
	r.Register("devpts", devpts)

	overlay := map[string]OptionSpec{}
	merge(overlay, common)
	for _, k := range []string{"lowerdir", "upperdir", "workdir", "redirect_dir", "index", "xino", "metacopy"} {
		overlay[k] = OptionSpec{Cmds: []Cmd{CmdSetString}}
	}
	overlay["volatile"] = OptionSpec{Cmds: []Cmd{CmdSetFlag}}
	overlay["userxattr"] = OptionSpec{Cmds: []Cmd{CmdSetFlag}}
	r.Register("overlay", overlay)

	ext4 := map[string]OptionSpec{}
	merge(ext4, common)
	for _, k := range []string{"norecovery", "noload", "errors", "data", "commit"} {
		ext4[k] = OptionSpec{Cmds: []Cmd{CmdSetString}}
	}
	// Example of a runtime policy: "resize" may only be applied to an
	// already-created superblock via CMD_RECONFIGURE.
	ext4["resize"] = OptionSpec{Cmds: []Cmd{CmdSetString}, ReconfigureOnly: true}
	r.Register("ext4", ext4)
	r.Register("ext3", ext4)
	r.Register("ext2", ext4)

	return r
}

// Validator checks fsconfig command sequences against a registry and a set
// of caller-registered file descriptors.
type Validator struct {
	reg *Registry
	mu  sync.RWMutex
	fds map[int]struct{}
}

func NewValidator(reg *Registry) *Validator {
	if reg == nil {
		reg = DefaultRegistry()
	}
	return &Validator{reg: reg, fds: make(map[int]struct{})}
}

// RegisterFD marks a descriptor as owned by the caller and therefore legal
// to reference from FSCONFIG_SET_FD.
func (v *Validator) RegisterFD(fd int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.fds[fd] = struct{}{}
}

// UnregisterFD removes a descriptor from the registered set.
func (v *Validator) UnregisterFD(fd int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.fds, fd)
}

func (v *Validator) fdRegistered(fd int) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.fds[fd]
	return ok
}

// phase is the mount-context state machine position.
type phase int

const (
	phaseConfig phase = iota
	phaseCreated
	phaseReconfigure
)

// Result summarizes a validated sequence.
type Result struct {
	FSType string
	// Declared holds the protection bits (nosuid/nodev/noexec/ro) that the
	// configuration phase committed to. The final fsmount attributes must
	// be at least this strong.
	Declared MountAttr
}

// ValidateMountAttr verifies that the attributes about to be passed to
// fsmount are not weaker than what the configuration phase declared.
func (r *Result) ValidateMountAttr(actual MountAttr) error {
	return ValidateMountAttr(r.Declared, actual)
}

// ValidateMountAttr reports an error unless actual includes every bit set
// in declared.
func ValidateMountAttr(declared, actual MountAttr) error {
	if actual&declared != declared {
		missing := declared &^ actual
		return fmt.Errorf("%w: declared %#x, actual %#x, missing %#x",
			ErrWeakMountAttr, uint64(declared), uint64(actual), uint64(missing))
	}
	return nil
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// Validate runs the full command sequence for fsType through the state
// machine and returns the committed protection bits on success.
func (v *Validator) Validate(fsType string, seq []Command) (*Result, error) {
	policy, ok := v.reg.policy(fsType)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFSType, fsType)
	}

	res := &Result{FSType: strings.ToLower(fsType)}
	ph := phaseConfig
	locked := make(map[string]bool) // canonical protection keys already set

	for i, cmd := range seq {
		switch {
		case cmd.Cmd.isSet():
			if ph == phaseCreated {
				return nil, fmt.Errorf("%w: command %d (%s %q)", ErrSetAfterCreate, i, cmd.Cmd, cmd.Key)
			}
			key := normalizeKey(cmd.Key)
			if key == "" {
				return nil, fmt.Errorf("%w: command %d: empty key", ErrBadCommandSequence, i)
			}
			spec, ok := policy[key]
			if !ok {
				return nil, fmt.Errorf("%w: %q for fs %q", ErrOptionNotAllowed, key, res.FSType)
			}
			if ph == phaseConfig && spec.ReconfigureOnly {
				return nil, fmt.Errorf("%w: %q", ErrReconfigureOnlyKey, key)
			}
			if canon, negates := protectionInverse[key]; negates && locked[canon] {
				return nil, fmt.Errorf("%w: %q would undo locked %q", ErrProtectionOverride, key, canon)
			}
			if attr, isProtection := protectionAttr[key]; isProtection {
				if locked[key] {
					return nil, fmt.Errorf("%w: %q set twice", ErrProtectionOverride, key)
				}
				locked[key] = true
				res.Declared |= attr
			}
			if !spec.allows(cmd.Cmd) {
				return nil, fmt.Errorf("%w: %s %q", ErrCommandNotAllowed, cmd.Cmd, key)
			}
			if key == "ro" {
				res.Declared |= MountAttrRdOnly
			}
			if cmd.Cmd == CmdSetFd && !v.fdRegistered(cmd.Fd) {
				return nil, fmt.Errorf("%w: fd %d", ErrUnregisteredFD, cmd.Fd)
			}
		case cmd.Cmd == CmdCreate:
			if ph != phaseConfig {
				return nil, fmt.Errorf("%w: CMD_CREATE in phase %d", ErrBadCommandSequence, ph)
			}
			ph = phaseCreated
		case cmd.Cmd == CmdReconfigure:
			if ph != phaseConfig {
				return nil, fmt.Errorf("%w: CMD_RECONFIGURE in phase %d", ErrBadCommandSequence, ph)
			}
			ph = phaseReconfigure
		default:
			return nil, fmt.Errorf("%w: command %d: unknown cmd %d", ErrBadCommandSequence, i, int(cmd.Cmd))
		}
	}
	return res, nil
}

// LockedProtections is a helper for callers that want the sorted list of
// protection keys a validated sequence committed to.
func (r *Result) LockedProtections() []string {
	var out []string
	for key, attr := range protectionAttr {
		if r.Declared&attr != 0 {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
