package seccomp

import "fmt"

// EnumDomain is a deliberately small finite domain for exhaustive concrete
// cross-checking.  Nil ArgValues entries default to {0}.
type EnumDomain struct {
	Archs     []Arch
	NRs       []uint32
	ArgValues [ArgCount][]uint64
}

// EnumHit is one exhaustively enumerated policy-matching lax input.
type EnumHit struct {
	Tuple  Tuple
	Action uint32
}

// Enumerate runs the concrete interpreter over the full cartesian product of
// the small domain and returns every policy-matching tuple whose action is
// lax.  This is the "small parameter domain full enumeration" oracle.
func Enumerate(insns []Instruction, policy *Policy, dom EnumDomain, opts ...Option) ([]EnumHit, int, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	archs := dom.Archs
	if len(archs) == 0 {
		archs = AllArchs
	}
	nrs := dom.NRs
	argVals := dom.ArgValues
	for i := range argVals {
		if len(argVals[i]) == 0 {
			argVals[i] = []uint64{0}
		}
	}
	var hits []EnumHit
	checked := 0
	var enumerate func(i int, t *Tuple) error
	enumerate = func(i int, t *Tuple) error {
		if i == ArgCount {
			for _, a := range archs {
				t.Arch = a
				for _, nr := range nrs {
					t.NR = nr
					checked++
					act, err := RunTuple(insns, *t)
					if err != nil {
						return err
					}
					if cfg.opts.isLax(act) && policy != nil && policy.MatchesAnyRule(*t) != nil {
						hits = append(hits, EnumHit{Tuple: *t, Action: act})
					}
				}
			}
			return nil
		}
		for _, v := range argVals[i] {
			t.Args[i] = v
			if err := enumerate(i+1, t); err != nil {
				return err
			}
		}
		return nil
	}
	if err := enumerate(0, &Tuple{}); err != nil {
		return nil, checked, err
	}
	return hits, checked, nil
}

// CrossCheckResult compares exhaustive enumeration with symbolic Verify.
type CrossCheckResult struct {
	Checked         int
	EnumBypasses    []EnumHit
	Report          *Report
	EnumFindsBypass bool
	SymbolicFinds   bool
	// Agree is true when both methods agree on bypass existence and every
	// symbolic witness reproduces concretely.
	Agree bool
	// Detail explains a disagreement.
	Detail string
}

// ExhaustiveCrossCheck runs the small-domain enumeration oracle and compares
// it against the symbolic verifier.  Every symbolic counterexample is
// independently re-run concretely.  It is intended for tests and host audit
// pipelines that want differential confidence on reduced domains.
func ExhaustiveCrossCheck(insns []Instruction, policy *Policy, dom EnumDomain, options ...Option) (*CrossCheckResult, error) {
	hits, checked, err := Enumerate(insns, policy, dom, options...)
	if err != nil {
		return nil, err
	}
	rep, verr := Verify(insns, policy, options...)
	if verr != nil && rep == nil {
		return nil, verr
	}
	res := &CrossCheckResult{
		Checked:         checked,
		EnumBypasses:    hits,
		Report:          rep,
		EnumFindsBypass: len(hits) > 0,
		SymbolicFinds:   rep != nil && !rep.Safe,
	}
	if rep != nil {
		for _, c := range rep.Counterexamples {
			act, err := RunTuple(insns, c.Tuple)
			if err != nil {
				res.Agree = false
				res.Detail = fmt.Sprintf("symbolic witness %s failed concrete run: %v", c.Tuple, err)
				return res, nil
			}
			if !rep.safeLax(act, options...) {
				res.Agree = false
				res.Detail = fmt.Sprintf("symbolic witness %s concretely returns non-lax %s", c.Tuple, ActionName(act))
				return res, nil
			}
			if rule := policy.MatchesAnyRule(c.Tuple); rule == nil {
				res.Agree = false
				res.Detail = fmt.Sprintf("symbolic witness %s does not match policy", c.Tuple)
				return res, nil
			}
		}
	}
	res.Agree = res.EnumFindsBypass == res.SymbolicFinds
	if !res.Agree {
		res.Detail = fmt.Sprintf("enum bypass=%v (%d hits) but symbolic safe=%v",
			res.EnumFindsBypass, len(hits), rep == nil || rep.Safe)
	}
	return res, nil
}

func (r *Report) safeLax(act uint32, options ...Option) bool {
	cfg := defaultConfig()
	for _, o := range options {
		o(&cfg)
	}
	return cfg.opts.isLax(act)
}
