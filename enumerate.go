package policycheck

import (
	"fmt"
	"sort"
)

// ExhaustiveConfig 配置有限但完整的小位宽参考空间。
//
// ArgBits 会枚举全部六个参数槽的 2^ArgBits 个值；NrBits 枚举全部
// 调用号。也可用 Numbers 为每个架构指定权威合法调用号集合。
type ExhaustiveConfig struct {
	ArgBits       uint              `json:"arg_bits,omitempty"`
	NrBits        uint              `json:"nr_bits,omitempty"`
	Architectures []Arch            `json:"architectures,omitempty"`
	Numbers       map[Arch][]uint64 `json:"numbers,omitempty"`
	MaxInputs     uint64            `json:"max_inputs,omitempty"`
}

// VerifyExhaustive 仅运行有限空间全枚举参考，返回的差异区域是两两
// 不相交的具体输入点。cfg 为 nil 时使用 2 位参数和 2 位调用号。
func VerifyExhaustive(policy *Policy, dag *DAG, registry Registry, cfg *ExhaustiveConfig) (*Report, error) {
	if cfg == nil {
		cfg = DefaultExhaustiveConfig()
	}
	return Verify(policy, dag, &Options{
		Registry:     registry,
		Exhaustive:   cfg,
		SkipSymbolic: true,
	})
}

// DefaultExhaustiveConfig 返回双架构、2 位参数、2 位调用号的全枚举。
func DefaultExhaustiveConfig() *ExhaustiveConfig {
	return &ExhaustiveConfig{ArgBits: 2, NrBits: 2, Architectures: []Arch{ArchX86_64, ArchIA32}}
}

func (c *ExhaustiveConfig) defaults() *ExhaustiveConfig {
	out := &ExhaustiveConfig{}
	if c != nil {
		*out = *c
	}
	if out.ArgBits == 0 {
		out.ArgBits = 2
	}
	if out.NrBits == 0 && len(out.Numbers) == 0 {
		out.NrBits = 2
	}
	if len(out.Architectures) == 0 {
		out.Architectures = []Arch{ArchX86_64, ArchIA32}
	}
	return out
}

func (c *ExhaustiveConfig) argValues() []uint64 {
	bits := c.ArgBits
	if bits >= 63 {
		panic("policycheck: exhaustive ArgBits must be small (<63)")
	}
	n := uint64(1) << bits
	out := make([]uint64, n)
	for i := uint64(0); i < n; i++ {
		out[i] = i
	}
	return out
}

func (c *ExhaustiveConfig) nrValues(arch Arch) []uint64 {
	if c.Numbers != nil {
		if values, ok := c.Numbers[arch]; ok {
			return append([]uint64(nil), values...)
		}
	}
	if c.NrBits >= 63 {
		panic("policycheck: exhaustive NrBits must be small (<63)")
	}
	n := uint64(1) << c.NrBits
	out := make([]uint64, n)
	for i := uint64(0); i < n; i++ {
		out[i] = i
	}
	return out
}

func verifyExhaustive(policy *Policy, dag *DAG, opts *Options, report *Report, recordMismatches bool) error {
	cfg := opts.Exhaustive.defaults()
	argValues := cfg.argValues()
	argSpace := uint64(1) << (cfg.ArgBits * 6)

	var archNRCount uint64
	for _, arch := range cfg.Architectures {
		archNRCount += uint64(len(cfg.nrValues(arch)))
	}
	total := argSpace * archNRCount
	if cfg.MaxInputs > 0 && total > cfg.MaxInputs {
		return fmt.Errorf("policycheck: exhaustive space has %d inputs, exceeds MaxInputs=%d", total, cfg.MaxInputs)
	}
	if cfg.MaxInputs == 0 && total > 20_000_000 {
		return fmt.Errorf("policycheck: exhaustive space has %d inputs; set Exhaustive.MaxInputs to allow", total)
	}

	initialMismatches := len(report.Mismatches)
	maxMismatch := maxMismatches(opts)

	for _, arch := range cfg.Architectures {
		if _, ok := NormalizeArch(string(arch)); !ok {
			return fmt.Errorf("policycheck: unsupported exhaustive architecture %q", arch)
		}
		for _, nr := range cfg.nrValues(arch) {
			err := enumerateArgs(argValues, [6]uint64{}, 0, func(args [6]uint64) (bool, error) {
				in := Input{Arch: arch, Nr: nr, Args: args}
				ref, err := policy.Evaluate(in, opts.Registry)
				if err != nil {
					return false, err
				}
				got, _, err := dag.Evaluate(in, opts.Registry)
				if err != nil {
					return false, err
				}
				transparent := opts.Strict
				if opts.Transparent != nil {
					transparent, err = opts.Transparent.ContainsInput(in, opts.Registry)
					if err != nil {
						return false, err
					}
				}
				report.CheckedInputs++
				violations := compareDecisions(ref, got, transparent, opts.Strict)
				if len(violations) == 0 {
					return true, nil
				}
				if !recordMismatches {
					return true, nil
				}

				nrCopy := nr
				witness := in
				region := Region{
					Arch:       arch,
					Nr:         &nrCopy,
					ExactPoint: true,
					Witness:    &witness,
					Literals: []string{
						fmt.Sprintf("arch=%s", arch),
						fmt.Sprintf("nr=%d", nr),
						fmt.Sprintf("args=%v", args),
					},
					Satisfiable:  true,
					WitnessFound: true,
				}
				report.Mismatches = append(report.Mismatches, Mismatch{
					Violations: violations,
					Region:     region,
					Reference:  ref,
					Candidate:  got,
				})
				if len(report.Mismatches)-initialMismatches >= maxMismatch {
					return false, nil
				}
				return true, nil
			})
			if err == errStopEnumeration {
				err = nil
			}
			if err != nil {
				return err
			}
			if len(report.Mismatches)-initialMismatches >= maxMismatch {
				report.ExhaustiveComplete = false
				return nil
			}
		}
	}
	report.ExhaustiveComplete = true
	return nil
}

func enumerateArgs(values []uint64, current [6]uint64, pos int, visit func([6]uint64) (bool, error)) error {
	if pos == 6 {
		cont, err := visit(current)
		if err != nil {
			return err
		}
		if !cont {
			return errStopEnumeration
		}
		return nil
	}
	for _, v := range values {
		current[pos] = v
		if err := enumerateArgs(values, current, pos+1, visit); err != nil {
			return err
		}
	}
	return nil
}

var errStopEnumeration = fmt.Errorf("stop enumeration")

func sortUint64s(values []uint64) {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
}
