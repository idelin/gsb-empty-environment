package seccomp

import (
	"fmt"
	"math"
)

// Arch identifies a target architecture by its AUDIT_ARCH_* value.
type Arch uint32

// Known architectures (little-endian only, see package doc).
const (
	ArchX86     Arch = 0x40000003 // AUDIT_ARCH_I386
	ArchX86_64  Arch = 0xC000003E // AUDIT_ARCH_X86_64
	ArchARM     Arch = 0x40000028 // AUDIT_ARCH_ARM
	ArchAArch64 Arch = 0xC00000B7 // AUDIT_ARCH_AARCH64
)

// known reports whether a is a supported architecture. Unknown
// architectures are always rejected.
func (a Arch) known() bool {
	switch a {
	case ArchX86, ArchX86_64, ArchARM, ArchAArch64:
		return true
	}
	return false
}

// Constraint is a predicate on one 64-bit syscall argument. All
// constraints of a rule must hold for the rule to match.
type Constraint interface{ constraint() }

// MaskedEqual matches when (args[Arg] & Mask) == Value.
type MaskedEqual struct {
	Arg   int
	Mask  uint64
	Value uint64
}

func (MaskedEqual) constraint() {}

// InRange matches when Min <= args[Arg] <= Max (unsigned).
type InRange struct {
	Arg      int
	Min, Max uint64
}

func (InRange) constraint() {}

// Rule matches a syscall number plus optional argument constraints.
type Rule struct {
	NR          int
	Constraints []Constraint
}

// Policy is a per-architecture syscall allow-list. Syscalls not matched by
// any rule are rejected, as is any context whose architecture differs from
// the policy's.
type Policy struct {
	Arch  Arch
	Rules []Rule
}

// Validate checks the policy for structural errors. Unknown architectures
// are always rejected.
func (p *Policy) Validate() error {
	if !p.Arch.known() {
		return fmt.Errorf("seccomp: unknown architecture %#08x", uint32(p.Arch))
	}
	for i, r := range p.Rules {
		if r.NR < 0 || r.NR > math.MaxInt32 {
			return fmt.Errorf("seccomp: rule %d: syscall number %d out of range", i, r.NR)
		}
		for j, c := range r.Constraints {
			switch c := c.(type) {
			case MaskedEqual:
				if err := checkArg(c.Arg); err != nil {
					return fmt.Errorf("seccomp: rule %d constraint %d: %w", i, j, err)
				}
				if c.Value&^c.Mask != 0 {
					return fmt.Errorf("seccomp: rule %d constraint %d: value %#x has bits outside mask %#x",
						i, j, c.Value, c.Mask)
				}
			case InRange:
				if err := checkArg(c.Arg); err != nil {
					return fmt.Errorf("seccomp: rule %d constraint %d: %w", i, j, err)
				}
				if c.Min > c.Max {
					return fmt.Errorf("seccomp: rule %d constraint %d: empty range [%#x, %#x]",
						i, j, c.Min, c.Max)
				}
			default:
				return fmt.Errorf("seccomp: rule %d constraint %d: unknown constraint type %T", i, j, c)
			}
		}
	}
	return nil
}

func checkArg(arg int) error {
	if arg < 0 || arg >= numArgs {
		return fmt.Errorf("argument index %d out of range [0, %d)", arg, numArgs)
	}
	return nil
}

// Allows is the reference semantics for a policy, written independently of
// the compiler. Tests compare compiled programs against it item by item.
func (p *Policy) Allows(ctx *Context) bool {
	if ctx.Arch != uint32(p.Arch) {
		return false
	}
	for _, r := range p.Rules {
		if int32(r.NR) != ctx.NR {
			continue
		}
		if r.match(ctx) {
			return true
		}
	}
	return false
}

func (r *Rule) match(ctx *Context) bool {
	for _, c := range r.Constraints {
		switch c := c.(type) {
		case MaskedEqual:
			if ctx.Args[c.Arg]&c.Mask != c.Value {
				return false
			}
		case InRange:
			if ctx.Args[c.Arg] < c.Min || ctx.Args[c.Arg] > c.Max {
				return false
			}
		}
	}
	return true
}
