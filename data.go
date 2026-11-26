package seccomp

import (
	"encoding/binary"
	"fmt"
)

// Data is the Go model of struct seccomp_data presented to a seccomp BPF
// program.  Args are full 64-bit values; the BPF program observes each as
// two independent 32-bit words (low word at offset 16+8*i, high word at
// offset 20+8*i on little-endian architectures).
type Data struct {
	NR                 uint32
	Arch               uint32
	InstructionPointer uint64
	Args               [ArgCount]uint64
}

// Bytes renders the exact 64-byte little-endian seccomp_data image.
func (d *Data) Bytes() [SeccompDataSize]byte {
	var b [SeccompDataSize]byte
	binary.LittleEndian.PutUint32(b[OffNR:], d.NR)
	binary.LittleEndian.PutUint32(b[OffArch:], d.Arch)
	binary.LittleEndian.PutUint64(b[OffIP:], d.InstructionPointer)
	for i, arg := range d.Args {
		binary.LittleEndian.PutUint64(b[OffArg0+i*ArgStride:], arg)
	}
	return b
}

// load reads size bytes (1, 2 or 4) at byte offset off.  Out-of-range
// reads return 0, matching the conservative behavior used by the audit
// emulator; Validate rejects statically out-of-range ABS offsets.
func (d *Data) load(off uint32, size uint16) uint32 {
	b := d.Bytes()
	o := int(off)
	if o < 0 || o+int(size) > len(b) {
		return 0
	}
	switch size {
	case SizeW:
		return binary.LittleEndian.Uint32(b[o : o+4])
	case SizeH:
		return uint32(binary.LittleEndian.Uint16(b[o : o+2]))
	case SizeB:
		return uint32(b[o])
	}
	return 0
}

// Tuple is a concrete, reproducible bypass witness: the architecture,
// (possibly x32-bit-tagged) syscall number and six 64-bit arguments.
type Tuple struct {
	Arch Arch
	NR   uint32
	Args [ArgCount]uint64
}

// Data converts the tuple into the seccomp_data image consumed by filters.
func (t Tuple) Data() *Data {
	return &Data{
		NR:   t.NR,
		Arch: t.Arch.AuditArch(),
		Args: t.Args,
	}
}

// String renders the tuple in a compact reproducible form.
func (t Tuple) String() string {
	return fmt.Sprintf("{arch=%s(0x%08x) nr=%d(0x%08x) args=[%#x %#x %#x %#x %#x %#x]}",
		t.Arch.Name(), t.Arch.AuditArch(), t.NR, t.NR,
		t.Args[0], t.Args[1], t.Args[2], t.Args[3], t.Args[4], t.Args[5])
}

// Less defines a total, reproducible order over tuples.  Architecture is
// ordered by its audit constant, then nr numerically, then arguments as
// unsigned 64-bit words in index order.
func (t Tuple) Less(o Tuple) bool {
	if t.Arch.AuditArch() != o.Arch.AuditArch() {
		return t.Arch.AuditArch() < o.Arch.AuditArch()
	}
	if t.NR != o.NR {
		return t.NR < o.NR
	}
	for i := 0; i < ArgCount; i++ {
		if t.Args[i] != o.Args[i] {
			return t.Args[i] < o.Args[i]
		}
	}
	return false
}

// Arch identifies one of the target architecture ABIs bound to a policy.
// ArchX32 shares AuditArchX8664 with ArchX8664 and is distinguished by the
// X32SyscallBit set in nr.
type Arch string

const (
	ArchX8664   Arch = "x86_64"
	ArchI386    Arch = "i386"
	ArchX32     Arch = "x32"
	ArchAARCH64 Arch = "aarch64"
)

// AllArchs is the declared target set [x86_64, i386, x32, aarch64].
var AllArchs = []Arch{ArchX8664, ArchI386, ArchX32, ArchAARCH64}

// ParseArch maps a textual architecture name (as used in host specs) to an
// Arch.  Accepted aliases include amd64/x64, x86/386 and arm64.
func ParseArch(s string) (Arch, error) {
	switch s {
	case "x86_64", "amd64", "x64":
		return ArchX8664, nil
	case "i386", "x86", "386", "i686":
		return ArchI386, nil
	case "x32":
		return ArchX32, nil
	case "aarch64", "arm64":
		return ArchAARCH64, nil
	}
	return "", fmt.Errorf("seccomp: unsupported architecture %q (want x86_64/i386/x32/aarch64)", s)
}

// AuditArch returns the AUDIT_ARCH_* constant reported in seccomp_data.arch.
func (a Arch) AuditArch() uint32 {
	switch a {
	case ArchI386:
		return AuditArchI386
	case ArchAARCH64:
		return AuditArchAARCH64
	case ArchX8664, ArchX32:
		return AuditArchX8664
	}
	return 0
}

// Name returns the canonical architecture name.
func (a Arch) Name() string {
	if _, err := ParseArch(string(a)); err == nil {
		return string(a)
	}
	return fmt.Sprintf("unknown(%q)", string(a))
}

// EncodeNR applies the ABI-specific syscall number encoding.  For x32 the
// __X32_SYSCALL_BIT (bit 30) is OR-ed in; other ABIs return nr unchanged.
// Numbers that already carry the x32 bit are preserved.
func (a Arch) EncodeNR(nr uint32) uint32 {
	if a == ArchX32 {
		return nr | X32SyscallBit
	}
	return nr
}
