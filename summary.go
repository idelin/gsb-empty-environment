package netsec

import "fmt"

// ParamSummary describes a library parameter.
//
// TimingBits are the parameter bits whose values may change the routine's
// execution time.  AddressBits are the subset used to form memory addresses;
// secret dependencies on those bits are always reported by default.
// PublicBits is the library's own declaration of bits that are public.  It is
// unioned with the call site's AllowedPublic declaration.
type ParamSummary struct {
	TimingBits  Mask
	AddressBits Mask
	PublicBits  Mask
}

// Summary is a hand-constructed abstract summary of a library routine.
type Summary struct {
	Name        string
	Params      []ParamSummary
	ResultWidth int
	// Model is used only by the concrete differential executor.  It must be
	// deterministic and side-effect free.  The analyzer never calls it.
	Model func(args []uint64) uint64
}

// Libraries maps library names to summaries.
type Libraries map[string]Summary

// Add registers one summary.
func (l Libraries) Add(summary Summary) Libraries {
	if l == nil {
		l = Libraries{}
	}
	l[summary.Name] = summary
	return l
}

func (l Libraries) lookup(name string) (Summary, error) {
	summary, ok := l[name]
	if !ok {
		return Summary{}, fmt.Errorf("unknown library summary %q", name)
	}
	if summary.Name == "" {
		summary.Name = name
	} else if summary.Name != name {
		return Summary{}, fmt.Errorf("library %q has summary named %q", name, summary.Name)
	}
	return summary, nil
}
