package seccomp

import "fmt"

// ArgCond is a bit-mask condition on one 64-bit syscall argument.  The
// condition matches when
//
//	(arg & Mask) == Value
//
// Value must only contain bits present in Mask.  Mask can be ^uint64(0)
// for exact equality.
type ArgCond struct {
	// Index is the argument index in [0,5].
	Index int
	// Mask selects the tested bits.
	Mask uint64
	// Value is the required masked value.
	Value uint64
}

// Matches reports whether the concrete argument satisfies the condition.
func (c ArgCond) Matches(arg uint64) bool {
	return arg&c.Mask == c.Value
}

// Validate checks the condition shape.
func (c ArgCond) Validate() error {
	if c.Index < 0 || c.Index >= ArgCount {
		return fmt.Errorf("seccomp: arg condition index %d out of range [0,%d]", c.Index, ArgCount-1)
	}
	if c.Value & ^c.Mask != 0 {
		return fmt.Errorf("seccomp: arg%d condition value %#x has bits outside mask %#x", c.Index, c.Value, c.Mask)
	}
	return nil
}

// Rule is one host prohibition: on any of Archs, every listed Syscalls
// number (with ABI encoding such as the x32 bit applied) together with
// ALL ArgConds is forbidden and must not reach a lax filter action.
type Rule struct {
	// Name is an optional human readable identifier.
	Name string
	// Archs binds the rule to target ABIs.  An empty slice means all
	// four target architectures.
	Archs []Arch
	// Syscalls are the dangerous syscall numbers in their canonical
	// per-ABI table (e.g. 59 for execve on x86_64/x32, 11 on i386,
	// 221 on aarch64).  The x32 bit is added automatically for ArchX32.
	Syscalls []uint32
	// Args are AND-ed bit-mask conditions.  Empty means all arguments.
	Args []ArgCond
}

// Policy is the host declaration of forbidden syscall behavior.
type Policy struct {
	Rules []Rule
}

// Spec is an alias for Policy for hosts that prefer the term "spec".
type Spec = Policy

// Deny builds a Rule with the given bindings.
func Deny(name string, archs []Arch, syscalls []uint32, args ...ArgCond) Rule {
	return Rule{Name: name, Archs: archs, Syscalls: syscalls, Args: args}
}

// Validate checks every rule.
func (p *Policy) Validate() error {
	if p == nil || len(p.Rules) == 0 {
		return fmt.Errorf("seccomp: policy contains no rules")
	}
	for ri, r := range p.Rules {
		archs := r.Archs
		if len(archs) == 0 {
			archs = AllArchs
		}
		for _, a := range archs {
			if a.AuditArch() == 0 {
				return fmt.Errorf("seccomp: rule %d: unknown arch %q", ri, a)
			}
		}
		if len(r.Syscalls) == 0 {
			return fmt.Errorf("seccomp: rule %d (%q): no syscall numbers", ri, r.Name)
		}
		for _, c := range r.Args {
			if err := c.Validate(); err != nil {
				return fmt.Errorf("seccomp: rule %d (%q): %w", ri, r.Name, err)
			}
		}
	}
	return nil
}

func (r Rule) archs() []Arch {
	if len(r.Archs) == 0 {
		return AllArchs
	}
	return r.Archs
}

// Matches reports whether t belongs to the forbidden region of the rule.
func (r Rule) Matches(t Tuple) bool {
	archOK := false
	for _, a := range r.archs() {
		if a == t.Arch {
			archOK = true
			break
		}
	}
	if !archOK {
		return false
	}
	nrOK := false
	for _, nr := range r.Syscalls {
		if t.NR == t.Arch.EncodeNR(nr) || t.NR == nr {
			nrOK = true
			break
		}
	}
	if !nrOK {
		return false
	}
	for _, c := range r.Args {
		if !c.Matches(t.Args[c.Index]) {
			return false
		}
	}
	return true
}

// MatchesAnyRule returns the first rule matching t, or nil.
func (p *Policy) MatchesAnyRule(t Tuple) *Rule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		if p.Rules[i].Matches(t) {
			return &p.Rules[i]
		}
	}
	return nil
}

// binding is one concrete (arch, nr) seed derived from a policy rule.
type binding struct {
	ruleIndex int
	rule      *Rule
	arch      Arch
	nr        uint32
}

// bindings expands the policy into the small set of concrete
// (architecture, syscall-number) seeds the symbolic explorer starts from.
func (p *Policy) bindings() []binding {
	var out []binding
	for ri := range p.Rules {
		r := &p.Rules[ri]
		for _, a := range r.archs() {
			for _, nr := range r.Syscalls {
				out = append(out, binding{
					ruleIndex: ri,
					rule:      r,
					arch:      a,
					nr:        a.EncodeNR(nr),
				})
			}
		}
	}
	return out
}
