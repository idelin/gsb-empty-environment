package linker

import "fmt"

// CallProbe names one function invocation to compare before and after folding.
// Symbol takes precedence; Section can be used for unnamed section-level entry
// points.
type CallProbe struct {
	Symbol  string
	Section SectionID
	Args    []int64
}

// ValidateOptions controls semantic validation.
type ValidateOptions struct {
	Probes   []CallProbe
	MaxSteps int
	// LiveSections restricts observable pointer checks to relocations whose
	// source section is live.  A nil map means every section is considered
	// live.
	LiveSections map[SectionID]bool
}

// CallComparison is the result of one probe.
type CallComparison struct {
	Probe  CallProbe
	Before Value
	After  Value
	Equal  bool
	Error  string
}

// PointerComparison checks that an observable pointer-equality relation is
// unchanged.  Only address-taken symbols are checked, because those are the
// identities the program can legally compare.
type PointerComparison struct {
	A           string
	B           string
	BeforeEqual bool
	AfterEqual  bool
	Equal       bool
	Error       string
}

// ValidationReport is returned by Validate.
type ValidationReport struct {
	Calls      []CallComparison
	Pointers   []PointerComparison
	CallsOK    bool
	PointersOK bool
	OK         bool
	Errors     []string
}

// AutoProbes creates a probe for every code symbol using its section's Arity.
// Code sections without symbols are probed directly at offset zero.  Arguments
// are deterministic positive integers.
func AutoProbes(img *Image) []CallProbe {
	return AutoProbesFor(img, nil)
}

// AutoProbesFor is AutoProbes restricted to live sections when live is
// non-nil.
func AutoProbesFor(img *Image, live map[SectionID]bool) []CallProbe {
	if img == nil {
		return nil
	}
	var probes []CallProbe
	for i, sec := range img.Sections {
		id := SectionID(i)
		if live != nil && !live[id] {
			continue
		}
		if kindOf(sec) != KindCode {
			continue
		}
		args := make([]int64, int(sec.Arity))
		for j := range args {
			args[j] = int64(j + 1)
		}
		seenOffset := make(map[uint64]bool)
		found := false
		for _, sym := range img.Symbols {
			if sym.Section != id || seenOffset[sym.Value] {
				continue
			}
			seenOffset[sym.Value] = true
			found = true
			probes = append(probes, CallProbe{Symbol: sym.Name, Args: append([]int64(nil), args...)})
		}
		if !found {
			probes = append(probes, CallProbe{Section: id, Args: args})
		}
	}
	return probes
}

// Validate executes probes in both images and compares both return values and
// all observable ADDR_TAKEN pointer-equality relations.
//
// oldToNew maps section IDs in before to section IDs in after.  It is normally
// ICFResult.OldToNew.
func Validate(before, after *Image, oldToNew map[SectionID]SectionID, opts ValidateOptions) (*ValidationReport, error) {
	if before == nil || after == nil {
		return nil, fmt.Errorf("linker: nil image")
	}
	if err := before.Validate(); err != nil {
		return nil, fmt.Errorf("linker: before image: %w", err)
	}
	if err := after.Validate(); err != nil {
		return nil, fmt.Errorf("linker: after image: %w", err)
	}
	if oldToNew == nil {
		oldToNew = make(map[SectionID]SectionID)
		for i := range before.Sections {
			id := SectionID(i)
			oldToNew[id] = id
		}
	}
	report := &ValidationReport{CallsOK: true, PointersOK: true}

	for _, probe := range opts.Probes {
		cmp := CallComparison{Probe: probe}
		bv, av, err := runProbe(before, after, oldToNew, probe, opts.MaxSteps)
		if err != nil {
			cmp.Error = err.Error()
			cmp.Equal = false
			report.CallsOK = false
			report.Errors = append(report.Errors, err.Error())
		} else {
			cmp.Before, cmp.After = bv, av
			cmp.Equal = valuesEquivalent(bv, av, oldToNew)
			if !cmp.Equal {
				report.CallsOK = false
				report.Errors = append(report.Errors, fmt.Sprintf("call %s returned %s before and %s after", probeName(probe), bv, av))
			}
		}
		report.Calls = append(report.Calls, cmp)
	}

	exprs := observableAddresses(before, opts.LiveSections)
	for i := 0; i < len(exprs); i++ {
		for j := i + 1; j < len(exprs); j++ {
			cmp := PointerComparison{A: exprs[i].String(), B: exprs[j].String()}
			sa, oka := exprs[i].resolve(before)
			sb, okb := exprs[j].resolve(before)
			tsa, okta := exprs[i].resolveMapped(after, oldToNew)
			tsb, oktb := exprs[j].resolveMapped(after, oldToNew)
			if !oka || !okb || !okta || !oktb {
				cmp.Error = "unable to resolve observable address"
				cmp.Equal = false
				report.PointersOK = false
				report.Errors = append(report.Errors, cmp.Error)
				report.Pointers = append(report.Pointers, cmp)
				continue
			}
			cmp.BeforeEqual = sa == sb
			cmp.AfterEqual = tsa == tsb
			cmp.Equal = cmp.BeforeEqual == cmp.AfterEqual
			if !cmp.Equal {
				report.PointersOK = false
				report.Errors = append(report.Errors, fmt.Sprintf("pointer identity changed: %s == %s was %v, now %v", cmp.A, cmp.B, cmp.BeforeEqual, cmp.AfterEqual))
			}
			report.Pointers = append(report.Pointers, cmp)
		}
	}

	report.OK = report.CallsOK && report.PointersOK
	return report, nil
}

// ValidateAll is Validate with AutoProbes(before).
func ValidateAll(before, after *Image, oldToNew map[SectionID]SectionID, maxSteps int) (*ValidationReport, error) {
	return Validate(before, after, oldToNew, ValidateOptions{
		Probes:   AutoProbes(before),
		MaxSteps: maxSteps,
	})
}

func runProbe(before, after *Image, oldToNew map[SectionID]SectionID, probe CallProbe, maxSteps int) (Value, Value, error) {
	if probe.Symbol != "" {
		bv, err := ExecuteSymbol(before, probe.Symbol, probe.Args, maxSteps)
		if err != nil {
			return Value{}, Value{}, fmt.Errorf("before probe %s: %w", probeName(probe), err)
		}
		av, err := ExecuteSymbol(after, probe.Symbol, probe.Args, maxSteps)
		if err != nil {
			return Value{}, Value{}, fmt.Errorf("after probe %s: %w", probeName(probe), err)
		}
		return bv, av, nil
	}
	if int(probe.Section) < 0 || int(probe.Section) >= len(before.Sections) {
		return Value{}, Value{}, fmt.Errorf("probe has invalid section %d", probe.Section)
	}
	newSection, ok := oldToNew[probe.Section]
	if !ok || int(newSection) < 0 || int(newSection) >= len(after.Sections) {
		return Value{}, Value{}, fmt.Errorf("probe section %d has no after-image mapping", probe.Section)
	}
	bv, err := Execute(before, probe.Section, probe.Args, maxSteps)
	if err != nil {
		return Value{}, Value{}, fmt.Errorf("before probe %s: %w", probeName(probe), err)
	}
	av, err := Execute(after, newSection, probe.Args, maxSteps)
	if err != nil {
		return Value{}, Value{}, fmt.Errorf("after probe %s: %w", probeName(probe), err)
	}
	return bv, av, nil
}

func probeName(p CallProbe) string {
	if p.Symbol != "" {
		return p.Symbol
	}
	return fmt.Sprintf("section-%d", p.Section)
}

func valuesEquivalent(before, after Value, oldToNew map[SectionID]SectionID) bool {
	if before.Kind != after.Kind {
		return false
	}
	if before.Kind == ValueInt {
		return before.Int == after.Int
	}
	mapped, ok := oldToNew[before.Ptr.Section]
	if !ok {
		return false
	}
	return mapped == after.Ptr.Section && before.Ptr.Offset == after.Ptr.Offset
}

type addressExpr struct {
	Symbol  string
	Section SectionID
	Offset  int64
}

func (a addressExpr) String() string {
	if a.Symbol != "" {
		return a.Symbol
	}
	return fmt.Sprintf("section-%d+%d", a.Section, a.Offset)
}

func (a addressExpr) resolve(img *Image) (string, bool) {
	if a.Symbol != "" {
		sym, ok := img.LookupSymbol(a.Symbol)
		if !ok || sym.Section == NoSection {
			return "", false
		}
		return fmt.Sprintf("%d+%d", sym.Section, int64(sym.Value)+a.Offset), true
	}
	if int(a.Section) < 0 || int(a.Section) >= len(img.Sections) {
		return "", false
	}
	return fmt.Sprintf("%d+%d", a.Section, a.Offset), true
}

func (a addressExpr) resolveMapped(img *Image, oldToNew map[SectionID]SectionID) (string, bool) {
	if a.Symbol != "" {
		sym, ok := img.LookupSymbol(a.Symbol)
		if !ok || sym.Section == NoSection {
			return "", false
		}
		return fmt.Sprintf("%d+%d", sym.Section, int64(sym.Value)+a.Offset), true
	}
	newID, ok := oldToNew[a.Section]
	if !ok || int(newID) < 0 || int(newID) >= len(img.Sections) {
		return "", false
	}
	return fmt.Sprintf("%d+%d", newID, a.Offset), true
}

func observableAddresses(img *Image, live map[SectionID]bool) []addressExpr {
	symbols := img.SymbolMap()
	seen := make(map[string]bool)
	var out []addressExpr
	for _, rel := range img.Relocations {
		if live != nil && !live[rel.From] {
			continue
		}
		if relocTypeOf(rel) != RelocAddrTaken {
			continue
		}
		tgt, err := img.resolveRelocation(rel, symbols)
		if err != nil || tgt.IsExternal || tgt.Section == NoSection {
			continue
		}
		expr := addressExpr{Section: tgt.Section, Offset: rel.Addend}
		if rel.Symbol != "" {
			expr.Symbol = rel.Symbol
		} else {
			expr.Offset = tgt.Offset
		}
		key := expr.String() + fmt.Sprintf("@%d", expr.Offset)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, expr)
	}
	return out
}
