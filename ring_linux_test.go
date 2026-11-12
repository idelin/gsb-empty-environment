//go:build linux

package netsec

import (
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"
)

const (
	ioUringOffSQRing      = 0
	ioUringOffCQRing      = 0x8000000
	ioUringOffSQEs        = 0x10000000
	ioUringEnterGetEvents = 1 << 0
)

// kernelSQE mirrors struct io_uring_sqe (64 bytes).
type kernelSQE struct {
	opcode      uint8
	flags       uint8
	ioprio      uint16
	fd          int32
	offset      uint64
	addr        uint64
	len         uint32
	rwFlags     uint32
	userData    uint64
	bufIndex    uint16
	personality uint16
	spliceFdIn  int32
	pad1        uint64
	pad2        [2]uint64
}

// kernelCQE mirrors struct io_uring_cqe (16 bytes).
type kernelCQE struct {
	userData uint64
	res      int32
	flags    uint32
}

type ringViews struct {
	params *kernelParams
	sqHead *uint32
	sqTail *uint32
	sqMask *uint32
	sqArr  []uint32
	sqes   []kernelSQE
	cqHead *uint32
	cqTail *uint32
	cqMask *uint32
	cqes   []kernelCQE
}

func mmapRing(fd int, p *kernelParams) (*ringViews, error) {
	sqLen := int(p.sqOff.array) + int(p.sqEntries)*4
	sq, err := syscall.Mmap(fd, int64(ioUringOffSQRing), sqLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		return nil, err
	}
	cqLen := int(p.cqOff.cqes) + int(p.cqEntries)*16
	cq, err := syscall.Mmap(fd, int64(ioUringOffCQRing), cqLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		_ = syscall.Munmap(sq)
		return nil, err
	}
	sqeLen := int(p.sqEntries) * int(unsafe.Sizeof(kernelSQE{}))
	sqeMem, err := syscall.Mmap(fd, int64(ioUringOffSQEs), sqeLen,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		_ = syscall.Munmap(sq)
		_ = syscall.Munmap(cq)
		return nil, err
	}

	return &ringViews{
		params: p,
		sqHead: (*uint32)(unsafe.Pointer(&sq[p.sqOff.head])),
		sqTail: (*uint32)(unsafe.Pointer(&sq[p.sqOff.tail])),
		sqMask: (*uint32)(unsafe.Pointer(&sq[p.sqOff.ringMask])),
		sqArr:  unsafe.Slice((*uint32)(unsafe.Pointer(&sq[p.sqOff.array])), p.sqEntries),
		sqes:   unsafe.Slice((*kernelSQE)(unsafe.Pointer(unsafe.SliceData(sqeMem))), p.sqEntries),
		cqHead: (*uint32)(unsafe.Pointer(&cq[p.cqOff.head])),
		cqTail: (*uint32)(unsafe.Pointer(&cq[p.cqOff.tail])),
		cqMask: (*uint32)(unsafe.Pointer(&cq[p.cqOff.ringMask])),
		cqes:   unsafe.Slice((*kernelCQE)(unsafe.Pointer(&cq[p.cqOff.cqes])), p.cqEntries),
	}, nil
}

func (v *ringViews) submit(t *testing.T, l *linuxRing, opcode uint8, flags uint8) int32 {
	t.Helper()
	head := atomicLoad(v.sqHead)
	tail := atomicLoad(v.sqTail)
	index := tail & *v.sqMask
	sqe := &v.sqes[index]
	*sqe = kernelSQE{
		opcode:   opcode,
		flags:    flags,
		fd:       -1,
		userData: uint64(tail) + 1,
	}
	v.sqArr[index] = index
	atomicStore(v.sqTail, tail+1)

	_, _, errno := syscall.Syscall6(
		sysIoUringEnter,
		uintptr(l.fd),
		uintptr(tail-head+1),
		1,
		ioUringEnterGetEvents,
		0,
		0,
	)
	if errno != 0 {
		t.Fatalf("io_uring_enter: %v", errno)
	}

	cqHead := atomicLoad(v.cqHead)
	cqTail := atomicLoad(v.cqTail)
	if cqTail == cqHead {
		t.Fatal("expected one completion, CQ ring is empty")
	}
	cqe := v.cqes[cqHead&*v.cqMask]
	atomicStore(v.cqHead, cqHead+1)
	return cqe.res
}

func linuxRingOf(t *testing.T, r *Ring) *linuxRing {
	t.Helper()
	l, ok := r.b.(*linuxRing)
	if !ok {
		t.Fatalf("backend is %T, want *linuxRing", r.b)
	}
	return l
}

func skipWithoutIOUring(t *testing.T) {
	t.Helper()
	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Skipf("io_uring unavailable: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func atomicLoad(p *uint32) uint32 {
	return (*atomic.Uint32)(unsafe.Pointer(p)).Load()
}

func atomicStore(p *uint32, v uint32) {
	(*atomic.Uint32)(unsafe.Pointer(p)).Store(v)
}

func linuxTestConfig() Config {
	return Config{
		AllowedRegisterOpcodes: []RegisterOpcode{RegisterProbe},
		AllowedSQEOpcodes:      []SQEOpcode{OpNop},
		AllowedSQEFlags:        SQEFlagIODrain,
		RequiredSQEFlags:       SQEFlagIODrain,
	}
}

// A restricted ring must be created disabled and cannot be enabled before
// restrictions are registered (kernel returns -EBADFD for SQEs while disabled).
func TestLinuxNewDisabledRing(t *testing.T) {
	skipWithoutIOUring(t)

	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Fatalf("NewDisabledRing: %v", err)
	}
	defer r.Close()
	if r.State() != StateCreated {
		t.Fatalf("state = %d, want StateCreated", r.State())
	}

	l := linuxRingOf(t, r)
	p := &kernelParams{}
	if err := l.rawRegister(uint32(RegisterProbe), unsafe.Pointer(p), 0); !errors.Is(err, syscall.EBADFD) {
		t.Fatalf("register on a still-disabled ring: got %v, want EBADFD", err)
	}
}

// Scenario 1 at the kernel boundary: IORING_REGISTER_RESTRICTIONS on a ring
// that is not disabled fails with -EBADFD.
func TestLinuxRegisterRestrictionsOnEnabledRing(t *testing.T) {
	skipWithoutIOUring(t)

	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Fatalf("NewDisabledRing: %v", err)
	}
	defer r.Close()
	l := linuxRingOf(t, r)

	if err := l.enableRings(); err != nil {
		t.Fatalf("enableRings: %v", err)
	}
	pol, err := linuxTestConfig().build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	err = l.registerRestrictions(pol.entries())
	if !errors.Is(err, syscall.EBADFD) {
		t.Fatalf("restrictions on enabled ring: got %v, want EBADFD", err)
	}
}

// Scenario 2 at the kernel boundary: the second restriction registration fails
// with -EEXIST; the package also rejects it up front.
func TestLinuxDoubleRestrictionRegistration(t *testing.T) {
	skipWithoutIOUring(t)

	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Fatalf("NewDisabledRing: %v", err)
	}
	defer r.Close()
	cfg := linuxTestConfig()

	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("first ApplyRestrictions: %v", err)
	}
	assertErrorIs(t, r.ApplyRestrictions(cfg), ErrRestrictionsAlreadyApplied)

	l := linuxRingOf(t, r)
	pol, err := cfg.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := l.registerRestrictions(pol.entries()); !errors.Is(err, syscall.EEXIST) {
		t.Fatalf("second kernel registration: got %v, want EEXIST", err)
	}
}

func TestLinuxSQEAndRegisterRestrictions(t *testing.T) {
	skipWithoutIOUring(t)

	if got := unsafe.Sizeof(kernelSQE{}); got != 64 {
		t.Fatalf("kernelSQE size = %d, want 64", got)
	}
	if got := unsafe.Sizeof(kernelCQE{}); got != 16 {
		t.Fatalf("kernelCQE size = %d, want 16", got)
	}
	if got := unsafe.Sizeof(kernelRestriction{}); got != 24 {
		t.Fatalf("kernelRestriction size = %d, want 24", got)
	}
	if got := unsafe.Sizeof(kernelParams{}); got != 104 {
		t.Fatalf("kernelParams size = %d, want 104", got)
	}

	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Fatalf("NewDisabledRing: %v", err)
	}
	defer r.Close()
	cfg := linuxTestConfig()

	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	l := linuxRingOf(t, r)
	views, err := mmapRing(l.fd, &l.params)
	if err != nil {
		t.Fatalf("mmapRing: %v", err)
	}

	// Whitelisted opcode with all required flags: NOP completes successfully.
	if res := views.submit(t, l, uint8(OpNop), uint8(SQEFlagIODrain)); res != 0 {
		t.Fatalf("allowed NOP: cqe.res = %d, want 0", res)
	}

	// Scenario 3: missing required flag -> -EACCES.
	if res := views.submit(t, l, uint8(OpNop), 0); res != -int32(syscall.EACCES) {
		t.Fatalf("NOP without required flag: cqe.res = %d, want -EACCES", res)
	}

	// Scenario 4: flag outside the allowed mask -> -EACCES.
	if res := views.submit(t, l, uint8(OpNop),
		uint8(SQEFlagIODrain|SQEFlagIOLink)); res != -int32(syscall.EACCES) {
		t.Fatalf("NOP with extraneous flag: cqe.res = %d, want -EACCES", res)
	}

	// Non-whitelisted opcode -> -EACCES.
	if res := views.submit(t, l, uint8(OpReadv), uint8(SQEFlagIODrain)); res != -int32(syscall.EACCES) {
		t.Fatalf("unwhitelisted READV: cqe.res = %d, want -EACCES", res)
	}

	// Scenario 5: IORING_REGISTER_FILES is not in the register whitelist, so
	// the kernel refuses the new file table with -EACCES.
	if err := r.RegisterFiles(nil); !errors.Is(err, ErrRegisterOpcodeNotAllowed) {
		t.Fatalf("package-level RegisterFiles: got %v, want ErrRegisterOpcodeNotAllowed", err)
	}
	if err := l.registerFiles(nil); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("kernel RegisterFiles: got %v, want EACCES", err)
	}
}

func TestLinuxRegisterFilesWhitelisted(t *testing.T) {
	skipWithoutIOUring(t)

	r, err := NewDisabledRing(8, 0)
	if err != nil {
		t.Fatalf("NewDisabledRing: %v", err)
	}
	defer r.Close()

	cfg := linuxTestConfig()
	cfg.AllowedRegisterOpcodes = []RegisterOpcode{RegisterProbe, RegisterFiles}
	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// A sparse file table (negative slots) exercises the whitelisted path
	// without needing real open file descriptors.
	fds := []int{-1, -1}
	if err := r.RegisterFiles(fds); err != nil {
		t.Fatalf("whitelisted RegisterFiles: %v", err)
	}

	// A register opcode outside the whitelist is still rejected.
	l := linuxRingOf(t, r)
	if err := l.rawRegister(uint32(RegisterBuffers), nil, 0); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("RegisterBuffers: got %v, want EACCES", err)
	}
}
