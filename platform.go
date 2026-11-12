package taskpkg

import (
	"fmt"
	"strings"
)

// MaxPlatformConditions is the fixed upper bound on boolean platform
// conditions. Bit indices therefore range over 0..11.
const MaxPlatformConditions = 12

// PlatformConfig is a bitmask of boolean platform conditions. Bit i represents
// condition i (for example linux, amd64, cgo). The zero value enables nothing.
type PlatformConfig uint16

// Bit returns whether condition bit i is enabled.
func (p PlatformConfig) Bit(i int) bool {
	if i < 0 || i >= MaxPlatformConditions {
		return false
	}
	return p&(1<<uint(i)) != 0
}

// WithBit returns a copy with condition i set to on.
func (p PlatformConfig) WithBit(i int, on bool) PlatformConfig {
	if i < 0 || i >= MaxPlatformConditions {
		return p
	}
	if on {
		return p | (1 << uint(i))
	}
	return p &^ (1 << uint(i))
}

// Configs returns all 2^bits distinct configurations in ascending mask order.
// Each configuration is enumerated independently for later resolution runs.
func Configs(bits int) ([]PlatformConfig, error) {
	if bits < 0 || bits > MaxPlatformConditions {
		return nil, fmt.Errorf("taskpkg: platform condition count %d exceeds limit %d",
			bits, MaxPlatformConditions)
	}
	count := 1 << uint(bits)
	out := make([]PlatformConfig, 0, count)
	for mask := 0; mask < count; mask++ {
		out = append(out, PlatformConfig(mask))
	}
	return out, nil
}

// Condition is a boolean expression over platform condition bits.
type Condition interface {
	Eval(cfg PlatformConfig) bool
	validate() error
}

// Bool is condition bit index i.
type Bool int

// Eval implements Condition.
func (b Bool) Eval(cfg PlatformConfig) bool { return cfg.Bit(int(b)) }

func (b Bool) validate() error {
	if b < 0 || int(b) >= MaxPlatformConditions {
		return fmt.Errorf("taskpkg: condition bit %d out of range [0,%d]", b, MaxPlatformConditions-1)
	}
	return nil
}

// Not negates Inner.
type Not struct{ Inner Condition }

// Eval implements Condition.
func (n Not) Eval(cfg PlatformConfig) bool { return !n.Inner.Eval(cfg) }

func (n Not) validate() error {
	if n.Inner == nil {
		return fmt.Errorf("taskpkg: Not has nil inner condition")
	}
	return n.Inner.validate()
}

// And requires every child to hold. An empty And always holds.
type And []Condition

// Eval implements Condition.
func (a And) Eval(cfg PlatformConfig) bool {
	for _, c := range a {
		if !c.Eval(cfg) {
			return false
		}
	}
	return true
}

func (a And) validate() error {
	for _, c := range a {
		if c == nil {
			return fmt.Errorf("taskpkg: And contains a nil condition")
		}
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Or requires at least one child to hold. An empty Or never holds.
type Or []Condition

// Eval implements Condition.
func (o Or) Eval(cfg PlatformConfig) bool {
	for _, c := range o {
		if c.Eval(cfg) {
			return true
		}
	}
	return false
}

func (o Or) validate() error {
	for _, c := range o {
		if c == nil {
			return fmt.Errorf("taskpkg: Or contains a nil condition")
		}
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Always is a condition that is satisfied under every configuration.
type Always struct{}

// Eval implements Condition.
func (Always) Eval(PlatformConfig) bool { return true }
func (Always) validate() error          { return nil }

// Never is a condition that is satisfied under no configuration.
type Never struct{}

// Eval implements Condition.
func (Never) Eval(PlatformConfig) bool { return false }
func (Never) validate() error          { return nil }

// String renders a mask as a set of set-bit indices, e.g. "bits{0,2}".
func (p PlatformConfig) String() string {
	var set []string
	for i := 0; i < MaxPlatformConditions; i++ {
		if p.Bit(i) {
			set = append(set, fmt.Sprintf("%d", i))
		}
	}
	return "bits{" + strings.Join(set, ",") + "}"
}
