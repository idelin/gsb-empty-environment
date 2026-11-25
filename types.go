package uffd

import "fmt"

// Sysctl is the value of vm.unprivileged_userfaultfd. Zero disables
// unprivileged userfaultfd; any non-zero value enables the kernel path (the
// gateway still applies its own UFFD_USER_MODE_ONLY rule).
type Sysctl = int

const (
	UserfaultfdDisabled Sysctl = 0
	UserfaultfdEnabled  Sysctl = 1
)

// SysctlFromInt is a convenience for gateways that read the sysctl as an int.
func SysctlFromInt(value int) Sysctl { return value }

// ProcessID identifies the process that owns a mapping or holds a descriptor.
type ProcessID uint64

// Permissions are the explicit additional grants needed by non-MISSING
// registration modes. CAP_SYS_PTRACE does not imply either permission.
type Permissions uint32

const (
	// PermWriteProtect permits UFFDIO_REGISTER_MODE_WP and
	// UFFDIO_WRITEPROTECT.
	PermWriteProtect Permissions = 1 << iota

	// PermMinor permits UFFDIO_REGISTER_MODE_MINOR.
	PermMinor
)

// Has reports whether all bits in want are present.
func (p Permissions) Has(want Permissions) bool { return p&want == want }

// Capabilities describes the credentials the gateway knows about for a
// caller.
type Capabilities struct {
	// CAPSysPtrace allows a userfaultfd without UFFD_USER_MODE_ONLY and
	// allows creation when unprivileged userfaultfd is disabled.
	CAPSysPtrace bool

	// WriteProtect is an explicit extra grant for WP registration and
	// UFFDIO_WRITEPROTECT. It is intentionally separate from CAPSysPtrace.
	WriteProtect bool

	// Minor is an explicit extra grant for minor-fault registration and
	// UFFDIO_CONTINUE.
	Minor bool
}

func (c Capabilities) permissions() Permissions {
	var p Permissions
	if c.WriteProtect {
		p |= PermWriteProtect
	}
	if c.Minor {
		p |= PermMinor
	}
	return p
}

// Caller is the subject of an authorization decision. Mappings is optional on
// individual operations; when a register request does not carry mappings, the
// caller's mapping snapshot is used.
type Caller struct {
	Process  ProcessID
	Caps     Capabilities
	Mappings []Mapping
}

// CreateFlags are the flags supplied to the userfaultfd creation syscall.
//
// The values mirror Linux's relevant flag values so a gateway can forward the
// low bits directly. O_CLOEXEC and O_NONBLOCK do not change authorization;
// they are accepted and retained for descriptor bookkeeping.
type CreateFlags uint32

const (
	UserModeOnly CreateFlags = 1 << 0
	NonBlock     CreateFlags = 1 << 11
	CloseOnExec  CreateFlags = 1 << 19
)

// Names matching the constants used by the kernel and syscall packages.
const (
	UFFD_USER_MODE_ONLY = UserModeOnly
	O_NONBLOCK          = NonBlock
	O_CLOEXEC           = CloseOnExec
)

const knownCreateFlags = UserModeOnly | NonBlock | CloseOnExec

// Has reports whether all bits in want are present.
func (f CreateFlags) Has(want CreateFlags) bool { return f&want == want }

// MappingKind classifies a virtual-memory interval.
type MappingKind uint8

const (
	// AnonymousPrivate is the only mapping kind that may be registered.
	// Being the zero value makes a plainly constructed Mapping default to
	// the safe, common case.
	AnonymousPrivate MappingKind = iota
	AnonymousShared
	FilePrivate
	FileShared
	// Unmapped explicitly describes a hole. It is useful when a gateway
	// reports both mappings and gaps.
	Unmapped
)

// Short aliases used by some callers.
const (
	AnonPrivate = AnonymousPrivate
	AnonShared  = AnonymousShared
	MappingHole = Unmapped
)

// Mapping is a half-open virtual-memory interval [Start, End).
type Mapping struct {
	Start uintptr
	End   uintptr
	Kind  MappingKind
	Owner ProcessID
}

// RegisterMode is a bit set of userfaultfd registration modes.
type RegisterMode uint32

const (
	ModeMissing RegisterMode = 1 << iota
	ModeWriteProtect
	ModeMinor
)

const (
	// ModeWP is a short form of ModeWriteProtect.
	ModeWP = ModeWriteProtect

	// Kernel-style names.
	UFFDIO_REGISTER_MODE_MISSING = ModeMissing
	UFFDIO_REGISTER_MODE_WP      = ModeWriteProtect
	UFFDIO_REGISTER_MODE_MINOR   = ModeMinor
)

const knownRegisterModes = ModeMissing | ModeWriteProtect | ModeMinor

func (m RegisterMode) String() string {
	if m == 0 {
		return "none"
	}
	s := ""
	if m&ModeMissing != 0 {
		s += "missing"
	}
	if m&ModeWriteProtect != 0 {
		if s != "" {
			s += "|"
		}
		s += "wp"
	}
	if m&ModeMinor != 0 {
		if s != "" {
			s += "|"
		}
		s += "minor"
	}
	if m&^knownRegisterModes != 0 {
		if s != "" {
			s += "|"
		}
		s += fmt.Sprintf("unknown(%#x)", uint32(m&^knownRegisterModes))
	}
	return s
}

// APIVersion is UFFD_API, the only API version accepted by UFFDIO_API.
const APIVersion uint32 = 0xAA

// FeatureSet contains UFFD_FEATURE_* bits negotiated by UFFDIO_API.
type FeatureSet uint64

const (
	FeaturePagefaultFlagWP  FeatureSet = 1 << 0
	FeatureEventFork                   = 1 << 1
	FeatureEventRemap                  = 1 << 2
	FeatureEventRemove                 = 1 << 3
	FeatureMissingHugetlbfs            = 1 << 4
	FeatureMissingShmem                = 1 << 5
	FeatureEventUnmap                  = 1 << 6
	FeatureSigbus                      = 1 << 7
	FeatureThreadID                    = 1 << 8
	FeatureMinorHugetlbfs              = 1 << 9
	FeatureMinorShmem                  = 1 << 10
	FeatureExactAddress                = 1 << 11
	FeatureWPHugetlbfsShmem            = 1 << 12
	FeatureWPUnpopulated               = 1 << 13
	FeaturePoison                      = 1 << 14
	FeatureWPAsync                     = 1 << 15
	FeatureMove                        = 1 << 16
)

const (
	// FeatureMinor covers both minor-fault backing types.
	FeatureMinor = FeatureMinorHugetlbfs | FeatureMinorShmem
	// FeatureWP and FeatureEventThread are retained as short spellings.
	FeatureWP          = FeatureWPHugetlbfsShmem
	FeatureEventThread = FeatureThreadID
)

const knownFeatures = FeaturePagefaultFlagWP |
	FeatureEventFork |
	FeatureEventRemap |
	FeatureEventRemove |
	FeatureMissingHugetlbfs |
	FeatureMissingShmem |
	FeatureEventUnmap |
	FeatureSigbus |
	FeatureThreadID |
	FeatureMinorHugetlbfs |
	FeatureMinorShmem |
	FeatureExactAddress |
	FeatureWPHugetlbfsShmem |
	FeatureWPUnpopulated |
	FeaturePoison |
	FeatureWPAsync |
	FeatureMove

// APIRequest is the payload of UFFDIO_API. A zero Version is treated as
// APIVersion for convenience.
type APIRequest struct {
	Version  uint32
	Features FeatureSet
}

// DefaultAPIRequest is a valid UFFDIO_API handshake with no optional
// features.
func DefaultAPIRequest() APIRequest {
	return APIRequest{Version: APIVersion}
}

// IoctlKind identifies a UFFDIO_* operation in an intercepted ioctl.
type IoctlKind uint32

const (
	UFFDIO_API IoctlKind = iota + 1
	UFFDIO_REGISTER
	UFFDIO_UNREGISTER
	UFFDIO_COPY
	UFFDIO_ZEROPAGE
	UFFDIO_WAKE
	UFFDIO_WRITEPROTECT
	UFFDIO_CONTINUE
)

func (k IoctlKind) String() string {
	switch k {
	case UFFDIO_API:
		return "UFFDIO_API"
	case UFFDIO_REGISTER:
		return "UFFDIO_REGISTER"
	case UFFDIO_UNREGISTER:
		return "UFFDIO_UNREGISTER"
	case UFFDIO_COPY:
		return "UFFDIO_COPY"
	case UFFDIO_ZEROPAGE:
		return "UFFDIO_ZEROPAGE"
	case UFFDIO_WAKE:
		return "UFFDIO_WAKE"
	case UFFDIO_WRITEPROTECT:
		return "UFFDIO_WRITEPROTECT"
	case UFFDIO_CONTINUE:
		return "UFFDIO_CONTINUE"
	default:
		return fmt.Sprintf("IoctlKind(%d)", uint32(k))
	}
}

// WriteProtectMode controls UFFDIO_WRITEPROTECT.
type WriteProtectMode uint64

const (
	WriteProtectEnable   WriteProtectMode = 1 << 0
	WriteProtectDontWake WriteProtectMode = 1 << 1
)

// RegisterRequest describes a UFFDIO_REGISTER range.
type RegisterRequest struct {
	Start    uintptr
	Length   uintptr
	End      uintptr
	Mode     RegisterMode
	Mappings []Mapping
}

// UnregisterRequest describes UFFDIO_UNREGISTER.
type UnregisterRequest struct {
	Start  uintptr
	Length uintptr
	End    uintptr
}

// CopyRequest describes UFFDIO_COPY or UFFDIO_ZEROPAGE. The source address is
// irrelevant to authorization; only the destination range is checked.
type CopyRequest struct {
	Start  uintptr
	Length uintptr
	End    uintptr
}

// WakeRequest describes UFFDIO_WAKE.
type WakeRequest struct {
	Start  uintptr
	Length uintptr
	End    uintptr
}

// WriteProtectRequest describes UFFDIO_WRITEPROTECT.
type WriteProtectRequest struct {
	Start   uintptr
	Length  uintptr
	End     uintptr
	Mode    WriteProtectMode
	Protect bool
}

// IoctlRequest is a decoded, intercepted ioctl. The fields used depend on
// Kind.
type IoctlRequest struct {
	Kind     IoctlKind
	Start    uintptr
	Length   uintptr
	End      uintptr
	Mode     RegisterMode
	Mappings []Mapping
	API      APIRequest

	// WriteProtectMode and Protect describe UFFDIO_WRITEPROTECT.
	WriteProtectMode WriteProtectMode
	Protect          bool
}
