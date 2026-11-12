//go:build linux

package netsec

import (
	"syscall"
	"unsafe"
)

const (
	sysIoUringSetup    = 425
	sysIoUringEnter    = 426
	sysIoUringRegister = 427
)

// kernelRestriction mirrors struct io_uring_restriction (24 bytes).
type kernelRestriction struct {
	opType uint16
	op     uint8
	_      uint8
	flags  uint64
	_      uint64
}

// kernelParams mirrors struct io_uring_params (104 bytes).
type kernelParams struct {
	sqEntries    uint32
	cqEntries    uint32
	flags        uint32
	sqThreadCPU  uint32
	sqThreadIdle uint32
	features     uint32
	wqFd         uint32
	resv         [3]uint32
	sqOff        sqRingOffsets
	cqOff        cqRingOffsets
}

type sqRingOffsets struct {
	head        uint32
	tail        uint32
	ringMask    uint32
	ringEntries uint32
	flags       uint32
	dropped     uint32
	array       uint32
	resv1       uint32
	resv2       uint64
}

type cqRingOffsets struct {
	head        uint32
	tail        uint32
	ringMask    uint32
	ringEntries uint32
	overflow    uint32
	cqes        uint32
	flags       uint32
	resv1       uint32
	resv2       uint64
}

// linuxRing drives one io_uring file descriptor through raw system calls.
type linuxRing struct {
	fd     int
	params kernelParams
}

// NewDisabledRing creates an io_uring ring with IORING_SETUP_R_DISABLED set.
// No SQE can be submitted and no ordinary registration is allowed until a
// restriction policy has been applied and the ring is enabled.
func NewDisabledRing(entries uint, extraFlags SetupFlag) (*Ring, error) {
	l := &linuxRing{params: kernelParams{
		flags: uint32(SetupRDisabled) | uint32(extraFlags),
	}}
	r0, _, errno := syscall.Syscall(
		sysIoUringSetup,
		uintptr(entries),
		uintptr(unsafe.Pointer(&l.params)),
		0,
	)
	if errno != 0 {
		return nil, errno
	}
	l.fd = int(r0)
	return newRing(l), nil
}

func (l *linuxRing) rawRegister(op uint32, arg unsafe.Pointer, nArgs uint32) error {
	_, _, errno := syscall.Syscall6(
		sysIoUringRegister,
		uintptr(l.fd),
		uintptr(op),
		uintptr(arg),
		uintptr(nArgs),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func (l *linuxRing) registerRestrictions(entries []restrictionEntry) error {
	restrictions := make([]kernelRestriction, len(entries))
	for i, e := range entries {
		restrictions[i] = kernelRestriction{
			opType: uint16(e.opType),
			op:     uint8(e.op),
			flags:  e.flags,
		}
	}
	return l.rawRegister(
		uint32(RegisterRestrictions),
		unsafe.Pointer(unsafe.SliceData(restrictions)),
		uint32(len(restrictions)),
	)
}

func (l *linuxRing) enableRings() error {
	return l.rawRegister(uint32(RegisterEnableRings), nil, 0)
}

func (l *linuxRing) registerFiles(fds []int) error {
	return l.rawRegister(
		uint32(RegisterFiles),
		unsafe.Pointer(unsafe.SliceData(fds)),
		uint32(len(fds)),
	)
}

func (l *linuxRing) close() error {
	return syscall.Close(l.fd)
}
