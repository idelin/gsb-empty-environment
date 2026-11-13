package netsec

import (
	"fmt"
)

// Status is the bounded search verdict.
type Status uint8

const (
	// StatusNoLeak means every compared secret pair had identical
	// architectural and speculative probe traces.
	StatusNoLeak Status = iota
	// StatusSpeculativeLeak means at least one secret pair had identical
	// architectural traces but different speculative traces.
	StatusSpeculativeLeak
	// StatusArchitecturalLeak means architectural execution itself depends on
	// the secret. Speculative leaks, if any, are still listed separately.
	StatusArchitecturalLeak
	// StatusUnknown means the run budget was exhausted before the search
	// completed and no leak had been established.
	StatusUnknown
)

func (s Status) String() string {
	switch s {
	case StatusNoLeak:
		return "no-leak"
	case StatusSpeculativeLeak:
		return "speculative-leak"
	case StatusArchitecturalLeak:
		return "architectural-leak"
	case StatusUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("status(%d)", s)
	}
}

// LeakPair records two runs with the same public input and different secrets.
type LeakPair struct {
	PublicIndex int
	Public      PublicInput
	SecretA     Value
	SecretB     Value

	ArchProbesA []uint8
	ArchProbesB []uint8
	SpecProbesA []uint8
	SpecProbesB []uint8

	RunA *RunResult
	RunB *RunResult
}

// SearchConfig describes the bounded input search.
type SearchConfig struct {
	Program Program

	// PublicInputs are the secret-independent input combinations supplied by
	// the host. If empty, one all-zero public input is used.
	PublicInputs []PublicInput

	// Secrets are the secret values to compare. If empty, all 256 values are
	// searched.
	Secrets []Value

	// Budget is the maximum number of concrete program runs. Zero selects a
	// default of 100000.
	Budget int

	Config Config
}

// SearchResult reports both categories of leakage separately.
type SearchResult struct {
	Status Status

	// ArchLeaks are secret pairs whose architectural probe traces differ.
	ArchLeaks []LeakPair
	// SpecLeaks are secret pairs whose architectural traces agree but whose
	// speculative traces differ.
	SpecLeaks []LeakPair

	Examined int
	Complete bool
}

type searchRun struct {
	secret Value
	result *RunResult
	arch   string
	spec   string
}

// Search compares secrets with identical public inputs. An architectural
// dependency on the secret is reported separately from a purely speculative
// one. If the budget prevents completing the search, StatusUnknown is returned
// unless a leak has already been established.
func Search(cfg SearchConfig) (*SearchResult, error) {
	if err := cfg.Program.Validate(); err != nil {
		return nil, err
	}
	if cfg.Program.SecretStoreCount() != 1 {
		return nil, fmt.Errorf("search requires exactly one secret store, found %d", cfg.Program.SecretStoreCount())
	}
	if cfg.Budget <= 0 {
		cfg.Budget = 100000
	}
	if len(cfg.Secrets) == 0 {
		cfg.Secrets = make([]Value, 256)
		for i := range cfg.Secrets {
			cfg.Secrets[i] = Value(i)
		}
	}
	if len(cfg.PublicInputs) == 0 {
		cfg.PublicInputs = []PublicInput{{}}
	}

	config, err := cfg.Config.normalized()
	if err != nil {
		return nil, err
	}

	result := &SearchResult{Complete: true}
	budget := cfg.Budget

search:
	for publicIndex, public := range cfg.PublicInputs {
		if err := public.validate(); err != nil {
			return nil, fmt.Errorf("public input %d: %w", publicIndex, err)
		}

		runs := make([]searchRun, 0, len(cfg.Secrets))
		firstByArch := make(map[string]searchRun)
		var archOrder []string
		specByArch := make(map[string]map[string]searchRun)
		specOrder := make(map[string][]string)

		for _, secret := range cfg.Secrets {
			if result.Examined >= budget {
				result.Complete = false
				break search
			}
			input := Input{
				Secret: secret,
				PublicInput: PublicInput{
					Low6:   public.Low6,
					Memory: cloneMemory(public.Memory),
					Regs:   public.Regs,
				},
			}
			run, err := RunWithConfig(cfg.Program, input, config)
			if err != nil {
				return nil, fmt.Errorf("public input %d, secret %#x: %w", publicIndex, secret, err)
			}
			result.Examined++
			entry := searchRun{
				secret: secret,
				result: run,
				arch:   string(run.ArchProbes),
				spec:   string(run.SpecProbes),
			}
			runs = append(runs, entry)

			if len(archOrder) == 0 {
				firstByArch[entry.arch] = entry
				archOrder = append(archOrder, entry.arch)
			} else if _, ok := firstByArch[entry.arch]; !ok {
				first := firstByArch[archOrder[0]]
				result.ArchLeaks = append(result.ArchLeaks, makeLeakPair(publicIndex, public, first, entry))
				firstByArch[entry.arch] = entry
				archOrder = append(archOrder, entry.arch)
			}

			group := specByArch[entry.arch]
			if group == nil {
				group = make(map[string]searchRun)
				specByArch[entry.arch] = group
			}
			if _, ok := group[entry.spec]; !ok {
				order := specOrder[entry.arch]
				if len(order) > 0 {
					other := group[order[0]]
					result.SpecLeaks = append(result.SpecLeaks, makeLeakPair(publicIndex, public, other, entry))
				}
				group[entry.spec] = entry
				specOrder[entry.arch] = append(order, entry.spec)
			}
		}

		// If the budget ran out in the middle of this public input, the runs
		// collected above are still valid evidence, but the overall search is
		// not exhaustive.
		if result.Examined >= budget && len(cfg.Secrets) > len(runs) {
			result.Complete = false
		}
	}

	switch {
	case len(result.ArchLeaks) > 0:
		result.Status = StatusArchitecturalLeak
	case len(result.SpecLeaks) > 0:
		result.Status = StatusSpeculativeLeak
	case !result.Complete:
		result.Status = StatusUnknown
	default:
		result.Status = StatusNoLeak
	}
	return result, nil
}

func makeLeakPair(publicIndex int, public PublicInput, a, b searchRun) LeakPair {
	return LeakPair{
		PublicIndex: publicIndex,
		Public: PublicInput{
			Low6:   public.Low6,
			Memory: cloneMemory(public.Memory),
			Regs:   public.Regs,
		},
		SecretA:     a.secret,
		SecretB:     b.secret,
		ArchProbesA: append([]uint8(nil), a.result.ArchProbes...),
		ArchProbesB: append([]uint8(nil), b.result.ArchProbes...),
		SpecProbesA: append([]uint8(nil), a.result.SpecProbes...),
		SpecProbesB: append([]uint8(nil), b.result.SpecProbes...),
		RunA:        a.result,
		RunB:        b.result,
	}
}

// MemoryCellDomain enumerates candidate values for one public memory cell.
type MemoryCellDomain struct {
	Addr   Addr
	Values []Value
}

// PublicDomain is a convenience enumerator for attacker-controlled low-six-bit
// choices and public memory contents. Registers stay at BaseRegs.
type PublicDomain struct {
	Low6        [2][]uint8
	MemoryCells []MemoryCellDomain
	BaseRegs    [RegisterCount]Value
}

// Enumerate expands the domain in deterministic order. If the domain has more
// than limit combinations, it returns the first limit inputs and complete
// false.
func (d PublicDomain) Enumerate(limit int) (inputs []PublicInput, complete bool, err error) {
	if limit <= 0 {
		return nil, false, fmt.Errorf("enumeration limit must be positive")
	}
	lowOptions := d.Low6
	for i := range lowOptions {
		if len(lowOptions[i]) == 0 {
			lowOptions[i] = []uint8{0}
		}
		for _, low := range lowOptions[i] {
			if low > 0x3f {
				return nil, false, fmt.Errorf("public low six bits %d (%#x) out of range", i, low)
			}
		}
	}

	type memoryOption struct {
		addr   Addr
		values []Value
	}
	memory := make([]memoryOption, 0, len(d.MemoryCells))
	for _, cell := range d.MemoryCells {
		if cell.Addr > (1<<AddrBits)-1 {
			return nil, false, fmt.Errorf("memory address %#x out of range", cell.Addr)
		}
		values := cell.Values
		if len(values) == 0 {
			values = []Value{0}
		}
		memory = append(memory, memoryOption{addr: cell.Addr, values: values})
	}

	total := len(lowOptions[0]) * len(lowOptions[1])
	for _, cell := range memory {
		total *= len(cell.values)
	}
	complete = total <= limit

	var visit func(low [2]uint8, mem map[Addr]Value, cell int)
	visit = func(low [2]uint8, mem map[Addr]Value, cell int) {
		if len(inputs) >= limit {
			return
		}
		if cell == len(memory) {
			inputs = append(inputs, PublicInput{
				Low6:   low,
				Memory: cloneMemory(mem),
				Regs:   d.BaseRegs,
			})
			return
		}
		option := memory[cell]
		for _, value := range option.values {
			mem[option.addr] = value
			visit(low, mem, cell+1)
			if len(inputs) >= limit {
				return
			}
		}
	}

	for _, low0 := range lowOptions[0] {
		for _, low1 := range lowOptions[1] {
			visit([2]uint8{low0, low1}, make(map[Addr]Value, len(memory)), 0)
			if len(inputs) >= limit {
				return inputs, complete, nil
			}
		}
	}
	return inputs, complete, nil
}
