package seccomp

// syscallNumbers maps the security-relevant named syscalls across the target
// ABIs.  x32 entries are the base table number; Arch.EncodeNR adds bit 30.
var syscallNumbers = map[string]map[Arch]uint32{
	"execve": {
		ArchX8664:   SysExecveX8664,
		ArchX32:     SysExecveX8664,
		ArchI386:    SysExecveI386,
		ArchAARCH64: SysExecveAARCH64,
	},
	"ptrace": {
		ArchX8664:   SysPtraceX8664,
		ArchX32:     SysPtraceX8664,
		ArchI386:    SysPtraceI386,
		ArchAARCH64: SysPtraceAARCH64,
	},
	"bpf": {
		ArchX8664:   SysBpfX8664,
		ArchX32:     SysBpfX8664,
		ArchI386:    SysBpfI386,
		ArchAARCH64: SysBpfAARCH64,
	},
}

// ResolveSyscall looks up a canonical syscall number for the named call on
// the given ABI.  The x32 bit is NOT added here; use Arch.EncodeNR.
func ResolveSyscall(name string, arch Arch) (uint32, bool) {
	byArch, ok := syscallNumbers[name]
	if !ok {
		return 0, false
	}
	nr, ok := byArch[arch]
	return nr, ok
}

// MustResolveSyscall panics on an unknown name/arch.
func MustResolveSyscall(name string, arch Arch) uint32 {
	nr, ok := ResolveSyscall(name, arch)
	if !ok {
		panic("seccomp: no syscall table entry for " + name + "/" + string(arch))
	}
	return nr
}

// DenyNamed builds one Rule per (name, architecture) pair using the correct
// per-ABI syscall numbers.  Expand with ... inside NewPolicy.
func DenyNamed(name string, archs []Arch, args ...ArgCond) []Rule {
	if len(archs) == 0 {
		archs = AllArchs
	}
	out := make([]Rule, 0, len(archs))
	for _, a := range archs {
		nr, ok := ResolveSyscall(name, a)
		if !ok {
			continue
		}
		out = append(out, Rule{
			Name:     name + "-" + string(a),
			Archs:    []Arch{a},
			Syscalls: []uint32{nr},
			Args:     args,
		})
	}
	return out
}
