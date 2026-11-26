package seccomp

import (
	"sort"
)

// Counterexample is a concrete bypass witness.
type Counterexample struct {
	// Tuple is the reproducible (arch, nr, arg0..arg5) input.
	Tuple Tuple
	// RuleName / RuleIndex identify the violated host prohibition.
	RuleName  string
	RuleIndex int
	// Action is the lax action returned by a single filter.
	Action uint32
	// Path is the sequence of executed pc values for reproducibility.
	Path []int
	// LayerActions and MergedAction are populated for stacked checks.
	LayerActions []uint32
	MergedAction uint32
}

// ActionName renders Action (or MergedAction for stacks).
func (c *Counterexample) ActionName() string {
	if c.MergedAction != 0 || len(c.LayerActions) > 1 {
		return ActionName(c.MergedAction)
	}
	return ActionName(c.Action)
}

// Report is the result of a symbolic authorization verification.
type Report struct {
	// Valid reports whether the program passed static validation.
	Valid bool
	// Validation is the aggregated static error when Valid is false.
	Validation *ValidationError
	// Safe is true when no concrete verified bypass was found.
	Safe bool
	// Complete is false when a path/solver budget was exhausted; Safe may
	// then be a false negative.
	Complete bool
	// ExploredStates counts symbolic states explored.
	ExploredStates int
	// Counterexamples are all verified witnesses found (sorted minimal first).
	Counterexamples []*Counterexample
	// Minimal is the lexicographically smallest counterexample.
	Minimal *Counterexample
}

// VerifyOptions configures VerifyWith.
type VerifyOptions struct {
	// LaxActions lists additional SECCOMP_RET_* action classes treated as
	// permissive.  SECCOMP_RET_ALLOW is always lax.
	LaxActions []uint32
	// IncludeLog treats SECCOMP_RET_LOG as lax (it allows after logging).
	IncludeLog bool
	// PathBudget bounds symbolic states per (arch,nr) binding.
	PathBudget int
	// SolverBudget bounds DPLL nodes per witness.
	SolverBudget int
	// SkipValidation disables the initial static validation.
	SkipValidation bool
}

// Option is a functional option for Verify / VerifyStack.
type Option func(*verifyConfig)

type verifyConfig struct {
	opts       VerifyOptions
	merge      MergeFunc
	mergeLabel string
}

// WithLaxActions adds permissive action classes.
func WithLaxActions(actions ...uint32) Option {
	return func(c *verifyConfig) { c.opts.LaxActions = append(c.opts.LaxActions, actions...) }
}

// WithLogAsLax makes SECCOMP_RET_LOG count as a bypass.
func WithLogAsLax() Option {
	return func(c *verifyConfig) { c.opts.IncludeLog = true }
}

// WithPathBudget overrides the symbolic state budget.
func WithPathBudget(n int) Option {
	return func(c *verifyConfig) { c.opts.PathBudget = n }
}

// WithSolverBudget overrides the DPLL node budget.
func WithSolverBudget(n int) Option {
	return func(c *verifyConfig) { c.opts.SolverBudget = n }
}

// WithMerge selects the stacked-filter merge function.
func WithMerge(m MergeFunc) Option {
	return func(c *verifyConfig) { c.merge = m; c.mergeLabel = "custom" }
}

// SkipValidation turns off static validation.
func SkipValidation() Option {
	return func(c *verifyConfig) { c.opts.SkipValidation = true }
}

func defaultConfig() verifyConfig {
	return verifyConfig{
		opts: VerifyOptions{
			PathBudget:   DefaultPathBudget,
			SolverBudget: DefaultSolverBudget,
		},
		merge:      MinReturns,
		mergeLabel: "numeric-min",
	}
}

func (o VerifyOptions) isLax(ret uint32) bool {
	class := ret & RetActionFull
	if class == RetAllow {
		return true
	}
	if o.IncludeLog && class == RetLog {
		return true
	}
	for _, a := range o.LaxActions {
		if class == a&RetActionFull {
			return true
		}
	}
	return false
}

// Verify decodes/validates (if needed) and symbolically checks one filter
// against the host policy.  A non-nil error reports an invalid program or
// policy; a returned Report with Safe=false carries reproducible witnesses.
func Verify(insns []Instruction, policy *Policy, options ...Option) (*Report, error) {
	cfg := defaultConfig()
	for _, o := range options {
		o(&cfg)
	}
	return verifyOne(insns, policy, cfg.opts)
}

// VerifyWith is the options-struct entry point.
func VerifyWith(insns []Instruction, policy *Policy, opts VerifyOptions) (*Report, error) {
	cfg := defaultConfig()
	cfg.opts = opts
	if cfg.opts.PathBudget == 0 {
		cfg.opts.PathBudget = DefaultPathBudget
	}
	if cfg.opts.SolverBudget == 0 {
		cfg.opts.SolverBudget = DefaultSolverBudget
	}
	return verifyOne(insns, policy, cfg.opts)
}

func verifyOne(insns []Instruction, policy *Policy, opts VerifyOptions) (*Report, error) {
	rep := &Report{Complete: true}
	if !opts.SkipValidation {
		if verr := Validate(insns); verr != nil {
			if as, ok := verr.(*ValidationError); ok {
				rep.Valid = false
				rep.Validation = as
			}
			return rep, verr
		}
	}
	rep.Valid = true
	if err := policy.Validate(); err != nil {
		return rep, err
	}

	var found []*Counterexample
	for _, b := range policy.bindings() {
		seeds, ok := seedDomains(b)
		if !ok {
			continue
		}
		outs, grown, nodes, trunc := exploreOutcomes(insns, seeds[:], opts.PathBudget)
		rep.ExploredStates += nodes
		if trunc {
			rep.Complete = false
		}
		for _, oc := range outs {
			actions := []uint32{oc.Action}
			preds := oc.Preds
			if oc.Dynamic {
				actions = laxClassList(opts)
			}
			for _, laxAct := range actions {
				usePreds := preds
				if oc.Dynamic {
					usePreds = withPreds(preds, Pred{Kind: PredEQ, L: oc.ActionExpr, R: eConst(laxAct)})
				} else if !opts.isLax(laxAct) {
					continue
				}
				vals, complete, budgetHit := solveMinimal(usePreds, grown, opts.SolverBudget)
				if budgetHit || !complete {
					rep.Complete = false
					continue
				}
				if vals == nil {
					continue
				}
				t := tupleFromVals(vals, b)
				got, err := RunTuple(insns, t)
				if err != nil {
					continue
				}
				if !opts.isLax(got) {
					continue
				}
				if b.rule != nil && !b.rule.Matches(t) {
					continue
				}
				found = append(found, &Counterexample{
					Tuple:     t,
					RuleName:  b.rule.Name,
					RuleIndex: b.ruleIndex,
					Action:    got,
					Path:      append([]int(nil), oc.Path...),
				})
			}
		}
	}
	finalizeReport(rep, found)
	return rep, nil
}

func laxClassList(opts VerifyOptions) []uint32 {
	list := []uint32{RetAllow}
	if opts.IncludeLog {
		list = append(list, RetLog)
	}
	list = append(list, opts.LaxActions...)
	return list
}

func finalizeReport(rep *Report, found []*Counterexample) {
	sort.SliceStable(found, func(i, j int) bool {
		return found[i].Tuple.Less(found[j].Tuple)
	})
	// De-duplicate identical tuples.
	var uniq []*Counterexample
	for _, c := range found {
		if len(uniq) > 0 && tuplesEqual(uniq[len(uniq)-1].Tuple, c.Tuple) {
			continue
		}
		uniq = append(uniq, c)
	}
	rep.Counterexamples = uniq
	rep.Safe = len(uniq) == 0
	if len(uniq) > 0 {
		rep.Minimal = uniq[0]
	}
}

func tuplesEqual(a, b Tuple) bool {
	if a.Arch != b.Arch || a.NR != b.NR {
		return false
	}
	return a.Args == b.Args
}

func seedDomains(b binding) ([SeccompDataSize / 4]Dom, bool) {
	var d [SeccompDataSize / 4]Dom
	for i := range d {
		d[i] = topDom()
	}
	d[WordNR] = constDom(b.nr)
	d[WordArch] = constDom(b.arch.AuditArch())
	if b.rule != nil {
		for _, c := range b.rule.Args {
			mLo := uint32(c.Mask)
			vLo := uint32(c.Value)
			mHi := uint32(c.Mask >> 32)
			vHi := uint32(c.Value >> 32)
			if mLo != 0 {
				m, ok := meetBits(d[ArgLoWord(c.Index)], bitsDom(mLo, vLo))
				if !ok {
					return d, false
				}
				d[ArgLoWord(c.Index)] = m
			}
			if mHi != 0 {
				m, ok := meetBits(d[ArgHiWord(c.Index)], bitsDom(mHi, vHi))
				if !ok {
					return d, false
				}
				d[ArgHiWord(c.Index)] = m
			}
		}
	}
	return d, true
}

func tupleFromVals(vals []uint32, b binding) Tuple {
	var args [ArgCount]uint64
	for i := 0; i < ArgCount; i++ {
		lo := uint32(0)
		hi := uint32(0)
		if id := ArgLoWord(i); id < len(vals) {
			lo = vals[id]
		}
		if id := ArgHiWord(i); id < len(vals) {
			hi = vals[id]
		}
		args[i] = uint64(hi)<<32 | uint64(lo)
	}
	return Tuple{Arch: b.arch, NR: b.nr, Args: args}
}

// FindBypass returns the minimal verified counterexample or nil when the
// policy holds.  It is a thin convenience wrapper over Verify.
func FindBypass(insns []Instruction, policy *Policy, options ...Option) (*Counterexample, error) {
	rep, err := Verify(insns, policy, options...)
	if err != nil && rep == nil {
		return nil, err
	}
	if rep == nil {
		return nil, err
	}
	return rep.Minimal, err
}
