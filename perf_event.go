package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Linux perf_event_attr ABI constants. The fields below are part of the
// userspace ABI in include/uapi/linux/perf_event.h.
const (
	PerfTypeHardware = 0
	PerfTypeSoftware = 1
	PerfTypeRaw      = 4

	PerfSampleAddr        = uint64(1 << 3)
	PerfSampleBranchStack = uint64(1 << 11)
	PerfSamplePhysAddr    = uint64(1 << 19)

	PerfFlagInherit       = uint64(1 << 1)
	PerfFlagExcludeKernel = uint64(1 << 5)
	PerfFlagExcludeHV     = uint64(1 << 6)
	PerfFlagInheritStat   = uint64(1 << 11)
	PerfFlagInheritThread = uint64(1 << 35)

	// PerfEventAttrSize is the size of the version-8 structure, including
	// config3. Older layouts can omit the tail of the structure, but the
	// policy below needs fields through branch_sample_type.
	PerfEventAttrSize = 136
)

// Offsets and masks for the fields used by the policy. They match the Linux
// little-endian perf_event_attr layout.
const (
	perfAttrOffsetType       = 0
	perfAttrOffsetSize       = 4
	perfAttrOffsetSampleType = 24
	perfAttrOffsetReadFormat = 32
	perfAttrOffsetFlags      = 40
	perfAttrOffsetBranchType = 72
)

const (
	// All flag bits through sigtrap (bit 37) are defined. The remaining
	// bits are reserved by the kernel and must not be accepted.
	knownPerfFlags = uint64(1<<38 - 1)

	// Defined PERF_SAMPLE_* bits through PERF_SAMPLE_WEIGHT_STRUCT.
	knownPerfSampleType = uint64(1<<25 - 1)

	// PERF_FORMAT_TOTAL_TIME_ENABLED, TOTAL_TIME_RUNNING, ID, GROUP,
	// and LOST.
	knownPerfReadFormat = uint64(1<<5 - 1)
)

// Errors returned by the policy. Callers can use errors.Is to distinguish the
// reason a request was rejected.
var (
	ErrInvalidPerfEventAttr = errors.New("invalid perf_event_attr")
	ErrNotCurrentThread     = errors.New("perf event must target only the current thread")
	ErrInvalidCPU           = errors.New("invalid perf event cpu")
	ErrInvalidAttrSize      = errors.New("invalid perf_event_attr size")
	ErrUnknownPerfFlag      = errors.New("unknown perf_event_attr flag bits")
	ErrInherit              = errors.New("perf event inheritance is forbidden")
	ErrKernelScope          = errors.New("perf event must exclude kernel events")
	ErrHypervisorScope      = errors.New("perf event must exclude hypervisor events")
	ErrRawEvent             = errors.New("raw perf events are forbidden")
	ErrAddressSampling      = errors.New("perf address sampling is forbidden")
	ErrPhysAddressSampling  = errors.New("perf physical-address sampling is forbidden")
	ErrLBRSampling          = errors.New("perf LBR/branch-stack sampling is forbidden")
)

// PerfEventAttr is the decoded prefix of a Linux perf_event_attr structure.
// Fields that were not present in an older, supported size are left at zero.
type PerfEventAttr struct {
	Type             uint32
	Size             uint32
	Config           uint64
	SamplePeriod     uint64
	SampleType       uint64
	ReadFormat       uint64
	Flags            uint64
	WakeupEvents     uint32
	BpType           uint32
	Config1          uint64
	Config2          uint64
	BranchSampleType uint64
	SampleRegsUser   uint64
	SampleStackUser  uint32
	ClockID          int32
	SampleRegsIntr   uint64
	AuxWatermark     uint32
	SampleMaxStack   uint16
	AuxSampleSize    uint32
	SigData          uint64
	Config3          uint64
}

// ValidatePerfEventAttr validates a raw perf_event_attr and the pid/cpu pair
// that will be passed to perf_event_open. A nil error means the request is safe
// for the sandbox's restricted current-thread user-space counters.
func ValidatePerfEventAttr[T ~int | ~int32 | ~int64](raw []byte, pid, cpu T) error {
	attr, err := ParsePerfEventAttr(raw)
	if err != nil {
		return err
	}
	return attr.Validate(int64(pid), int64(cpu))
}

// Validate applies the event policy to an already decoded attribute.
func (a *PerfEventAttr) Validate(pid, cpu int64) error {
	if a == nil {
		return fmt.Errorf("%w: nil attribute", ErrInvalidPerfEventAttr)
	}

	// pid 0 is the calling thread. Positive pids select another task and
	// negative pids select all tasks; neither is permitted. cpu selects an
	// optional per-CPU filter, but never changes the pid=0 task scope.
	if pid != 0 {
		return fmt.Errorf("%w: pid=%d", ErrNotCurrentThread, pid)
	}
	if cpu < -1 {
		return fmt.Errorf("%w: cpu=%d", ErrInvalidCPU, cpu)
	}

	if !validPerfEventAttrSize(a.Size) {
		return fmt.Errorf("%w: size=%d", ErrInvalidAttrSize, a.Size)
	}

	if a.Flags&^knownPerfFlags != 0 {
		return fmt.Errorf("%w: flags=%#x", ErrUnknownPerfFlag, a.Flags&^knownPerfFlags)
	}
	if a.SampleType&^knownPerfSampleType != 0 {
		return fmt.Errorf("%w: sample_type=%#x", ErrUnknownPerfFlag, a.SampleType&^knownPerfSampleType)
	}
	if a.ReadFormat&^knownPerfReadFormat != 0 {
		return fmt.Errorf("%w: read_format=%#x", ErrUnknownPerfFlag, a.ReadFormat&^knownPerfReadFormat)
	}

	if a.Flags&(PerfFlagInherit|PerfFlagInheritStat|PerfFlagInheritThread) != 0 {
		return fmt.Errorf("%w: flags=%#x", ErrInherit, a.Flags&(PerfFlagInherit|PerfFlagInheritStat|PerfFlagInheritThread))
	}
	if a.Flags&PerfFlagExcludeKernel == 0 {
		return fmt.Errorf("%w: flags=%#x", ErrKernelScope, a.Flags)
	}
	if a.Flags&PerfFlagExcludeHV == 0 {
		return fmt.Errorf("%w: flags=%#x", ErrHypervisorScope, a.Flags)
	}

	if a.Type == PerfTypeRaw {
		return fmt.Errorf("%w: type=%d", ErrRawEvent, a.Type)
	}

	if a.SampleType&PerfSampleAddr != 0 {
		return fmt.Errorf("%w: sample_type=%#x", ErrAddressSampling, a.SampleType)
	}
	if a.SampleType&PerfSamplePhysAddr != 0 {
		return fmt.Errorf("%w: sample_type=%#x", ErrPhysAddressSampling, a.SampleType)
	}
	if a.SampleType&PerfSampleBranchStack != 0 || a.BranchSampleType != 0 {
		return fmt.Errorf("%w: sample_type=%#x branch_sample_type=%#x",
			ErrLBRSampling, a.SampleType, a.BranchSampleType)
	}

	return nil
}

// ParsePerfEventAttr decodes little-endian Linux perf_event_attr bytes. The
// declared size must match the byte slice exactly and may not be a truncated
// legacy layout that omits fields the policy needs.
func ParsePerfEventAttr(raw []byte) (*PerfEventAttr, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("%w: %d bytes", ErrInvalidPerfEventAttr, len(raw))
	}

	size := binary.LittleEndian.Uint32(raw[perfAttrOffsetSize : perfAttrOffsetSize+4])
	if !validPerfEventAttrSize(size) {
		return nil, fmt.Errorf("%w: size=%d", ErrInvalidAttrSize, size)
	}
	if int(size) != len(raw) {
		return nil, fmt.Errorf("%w: declared size=%d, bytes=%d", ErrInvalidAttrSize, size, len(raw))
	}

	attr := &PerfEventAttr{
		Type:         binary.LittleEndian.Uint32(raw[perfAttrOffsetType : perfAttrOffsetType+4]),
		Size:         size,
		Config:       binary.LittleEndian.Uint64(raw[8:16]),
		SamplePeriod: binary.LittleEndian.Uint64(raw[16:24]),
		SampleType:   binary.LittleEndian.Uint64(raw[perfAttrOffsetSampleType : perfAttrOffsetSampleType+8]),
		ReadFormat:   binary.LittleEndian.Uint64(raw[perfAttrOffsetReadFormat : perfAttrOffsetReadFormat+8]),
		Flags:        binary.LittleEndian.Uint64(raw[perfAttrOffsetFlags : perfAttrOffsetFlags+8]),
		WakeupEvents: binary.LittleEndian.Uint32(raw[48:52]),
		BpType:       binary.LittleEndian.Uint32(raw[52:56]),
		Config1:      binary.LittleEndian.Uint64(raw[56:64]),
		Config2:      binary.LittleEndian.Uint64(raw[64:72]),
	}

	attr.BranchSampleType = binary.LittleEndian.Uint64(raw[perfAttrOffsetBranchType : perfAttrOffsetBranchType+8])

	// Read optional fields only after their offsets are covered by the
	// declared size.
	if size >= 88 {
		attr.SampleRegsUser = binary.LittleEndian.Uint64(raw[80:88])
	}
	if size >= 96 {
		attr.SampleStackUser = binary.LittleEndian.Uint32(raw[88:92])
		attr.ClockID = int32(binary.LittleEndian.Uint32(raw[92:96]))
	}
	if size >= 104 {
		attr.SampleRegsIntr = binary.LittleEndian.Uint64(raw[96:104])
	}
	if size >= 112 {
		attr.AuxWatermark = binary.LittleEndian.Uint32(raw[104:108])
		attr.SampleMaxStack = binary.LittleEndian.Uint16(raw[108:110])
	}
	if size >= 120 {
		attr.AuxSampleSize = binary.LittleEndian.Uint32(raw[112:116])
	}
	if size >= 128 {
		attr.SigData = binary.LittleEndian.Uint64(raw[120:128])
	}
	if size >= 136 {
		attr.Config3 = binary.LittleEndian.Uint64(raw[128:136])
	}

	return attr, nil
}

// validPerfEventAttrSize accepts the published perf_event_attr layouts that
// are large enough to contain branch_sample_type (version 2 and later). The
// version-0 and version-1 layouts are rejected because the policy cannot
// inspect that field.
func validPerfEventAttrSize(size uint32) bool {
	switch size {
	case 80, 96, 104, 112, 120, 128, 136:
		return true
	default:
		return false
	}
}

// DecodePerfEventAttr is a value-returning convenience wrapper.
func DecodePerfEventAttr(raw []byte) (PerfEventAttr, error) {
	attr, err := ParsePerfEventAttr(raw)
	if err != nil {
		return PerfEventAttr{}, err
	}
	return *attr, nil
}

// ValidateRawPerfEventAttr is an alias kept for gateways that name the raw
// decoder explicitly.
func ValidateRawPerfEventAttr[T ~int | ~int32 | ~int64](raw []byte, pid, cpu T) error {
	return ValidatePerfEventAttr(raw, pid, cpu)
}

// Validate is the short package-level entry point.
func Validate[T ~int | ~int32 | ~int64](raw []byte, pid, cpu T) error {
	return ValidatePerfEventAttr(raw, pid, cpu)
}

// CheckPerfEventAttr is an alias for callers that use a boolean-style name
// while still returning a policy error.
func CheckPerfEventAttr[T ~int | ~int32 | ~int64](raw []byte, pid, cpu T) error {
	return ValidatePerfEventAttr(raw, pid, cpu)
}
