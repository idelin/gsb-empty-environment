package netsec

import (
	"fmt"
	"math"
)

// Signed is the set of integer parameter types accepted by Analyze.
type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// Analyze computes the maximum simultaneously-live stack for the program's
// entry function called with entryParam. By default entryParam is a non-negative
// depth budget. An unproven recursive cycle produces StatusUnknown rather than
// an error.
func Analyze[T Signed](p Program, entryParam T, options ...Options) Result {
	result, _ := AnalyzeStrict(p, entryParam, options...)
	return result
}

// AnalyzeStrict is like Analyze but also returns malformed-program errors.
// Unknown proofs still use a nil error and StatusUnknown.
func AnalyzeStrict[T Signed](p Program, entryParam T, options ...Options) (Result, error) {
	return analyze(p, int64(entryParam), options...)
}

// AnalyzeEntry computes the same result with an explicit entry function,
// overriding Program.Entry.
func AnalyzeEntry(entry string, p Program, entryParam int64, options ...Options) Result {
	p.Entry = entry
	result, _ := analyze(p, entryParam, options...)
	return result
}

// Analyze is a method form using Program.Entry.
func (p Program) Analyze(entryParam int64, options ...Options) Result {
	result, _ := analyze(p, entryParam, options...)
	return result
}

func analyze(p Program, entryParam int64, options ...Options) (Result, error) {
	prepared, err := prepare(p)
	if err != nil {
		unknown := Result{
			Status:     StatusUnknown,
			Known:      false,
			Reason:     err.Error(),
			Entry:      p.Entry,
			EntryParam: entryParam,
		}
		return unknown, err
	}
	opts := Options{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.StackLimit == 0 {
		opts.StackLimit = opts.Limit
	}
	result := Result{
		Status:     StatusProven,
		Known:      true,
		Entry:      p.Entry,
		EntryParam: entryParam,
		HasLimit:   opts.StackLimit > 0,
		StackLimit: opts.StackLimit,
	}
	if opts.StackLimit > 0 {
		result.Fits = true
	}

	if ok, reason := prepared.decreasingProof(opts.AllowNegativeParameters); !ok {
		result.Status = StatusUnknown
		result.Known = false
		result.Reason = reason
		result.Fits = false
		return result, nil
	}

	a := newAnalyzer(prepared, opts)
	entryIdx := prepared.byName[p.Entry]
	root, failure := a.eval(entryIdx, entryParam)
	if failure != nil {
		result.Status = StatusUnknown
		result.Known = false
		result.Reason = failure.Error()
		result.Fits = false
		return result, nil
	}

	result.MaxStack = root.peak
	result.MaxStackBytes = root.peak
	result.MaxBytes = root.peak
	result.PeakStack = root.peak
	result.WorstStack = root.peak
	result.MaxDepth = root.depth
	result.Depth = root.depth
	result.CallSites = a.callSites

	path, checks, maxLiveFrames, maxLive, pathErr := a.witness(entryIdx, entryParam)
	if pathErr != nil {
		result.Status = StatusUnknown
		result.Known = false
		result.Reason = pathErr.Error()
		result.Fits = false
		return result, nil
	}
	result.Path = path
	result.PathNames = make([]string, len(path))
	result.CallPath = make([]string, len(path))
	result.ParameterPath = make([]int64, len(path))
	result.FramePath = make([]int64, len(path))
	for i, step := range path {
		result.PathNames[i] = step.Function
		result.CallPath[i] = step.Function
		result.ParameterPath[i] = step.Parameter
		result.FramePath[i] = step.FrameSize
	}
	result.MaxLiveFrames = maxLiveFrames
	result.Checks = checks
	if maxLive > result.MaxStack {
		result.MaxStack = maxLive
		result.MaxStackBytes = maxLive
		result.MaxBytes = maxLive
		result.PeakStack = maxLive
		result.WorstStack = maxLive
	}
	if result.HasLimit {
		result.Fits = result.MaxStack <= result.StackLimit
	}
	return result, nil
}

type stateKey struct {
	funcIndex int
	param     int64
}

type stateValue struct {
	peak      int64
	depth     int
	choice    int
	choiceArg int64
}

type analysisFailure struct {
	reason string
}

func (f *analysisFailure) Error() string {
	return f.reason
}

type analyzer struct {
	prepared  *preparedProgram
	options   Options
	memo      map[stateKey]stateValue
	visiting  map[stateKey]bool
	callSites []CallSite
}

func newAnalyzer(p *preparedProgram, options Options) *analyzer {
	maxStates := options.MaxStates
	if maxStates <= 0 {
		maxStates = 1 << 20
	}
	options.MaxStates = maxStates
	return &analyzer{
		prepared: p,
		options:  options,
		memo:     make(map[stateKey]stateValue),
		visiting: make(map[stateKey]bool),
	}
}

func (a *analyzer) eval(funcIndex int, param int64) (stateValue, *analysisFailure) {
	key := stateKey{funcIndex: funcIndex, param: param}
	if value, ok := a.memo[key]; ok {
		return value, nil
	}
	if a.visiting[key] {
		return stateValue{}, &analysisFailure{reason: fmt.Sprintf(
			"recursive state %s(%d) revisited without a proven decrease",
			a.prepared.funcs[funcIndex].Name, param,
		)}
	}
	if len(a.memo)+len(a.visiting) >= a.options.MaxStates {
		return stateValue{}, &analysisFailure{reason: fmt.Sprintf(
			"analysis state limit %d exceeded at %s(%d)",
			a.options.MaxStates, a.prepared.funcs[funcIndex].Name, param,
		)}
	}

	a.visiting[key] = true
	defer delete(a.visiting, key)

	fn := &a.prepared.funcs[funcIndex]
	best := stateValue{peak: fn.FrameSize, depth: 1, choice: -1}
	for edgeIndex, edge := range fn.Calls {
		if !edge.Guard.Holds(param) {
			continue
		}
		calleeParam, ok := checkedAdd(param, edge.Delta)
		if !ok {
			return stateValue{}, &analysisFailure{reason: fmt.Sprintf(
				"parameter overflow at %s(%d) -> %s",
				fn.Name, param, edge.Target,
			)}
		}
		if !a.options.AllowNegativeParameters && (param < 0 || calleeParam < 0) {
			continue
		}
		siteSlot := len(a.callSites)
		a.callSites = append(a.callSites, CallSite{
			Caller:      fn.Name,
			Callee:      edge.Target,
			CallerParam: param,
			CalleeParam: calleeParam,
			Tail:        edge.Kind == CallTail,
			CallerFrame: fn.FrameSize,
			Label:       edge.Label,
		})
		calleeIndex := a.prepared.byName[edge.Target]
		child, failure := a.eval(calleeIndex, calleeParam)
		if failure != nil {
			return stateValue{}, failure
		}

		var candidatePeak int64
		if edge.Kind == CallTail {
			candidatePeak = max64(fn.FrameSize, child.peak)
		} else {
			var ok bool
			candidatePeak, ok = checkedAdd(fn.FrameSize, child.peak)
			if !ok {
				return stateValue{}, &analysisFailure{reason: fmt.Sprintf(
					"stack-size overflow at %s(%d) -> %s",
					fn.Name, param, edge.Target,
				)}
			}
		}
		a.callSites[siteSlot].CalleeNeed = child.peak
		a.callSites[siteSlot].Required = candidatePeak

		candidateDepth := child.depth + 1
		if candidatePeak > best.peak || (candidatePeak == best.peak && candidateDepth > best.depth) {
			best = stateValue{
				peak:      candidatePeak,
				depth:     candidateDepth,
				choice:    edgeIndex,
				choiceArg: calleeParam,
			}
		}
	}
	a.memo[key] = best
	return best, nil
}

func (a *analyzer) witness(entryIndex int, entryParam int64) ([]Step, []CallCheck, int, int64, error) {
	key := stateKey{funcIndex: entryIndex, param: entryParam}
	root, ok := a.memo[key]
	if !ok {
		return nil, nil, 0, 0, fmt.Errorf("internal error: missing entry state")
	}
	entry := &a.prepared.funcs[entryIndex]
	live := entry.FrameSize
	maxLive := live
	maxFrames := 1
	steps := []Step{{
		Function:  entry.Name,
		Parameter: entryParam,
		FrameSize: entry.FrameSize,
		Live:      live,
	}}
	checks := make([]CallCheck, 0)
	curKey := key
	cur := root
	for cur.choice >= 0 {
		fn := &a.prepared.funcs[curKey.funcIndex]
		edge := fn.Calls[cur.choice]
		calleeIndex := a.prepared.byName[edge.Target]
		calleeParam := cur.choiceArg
		childKey := stateKey{funcIndex: calleeIndex, param: calleeParam}
		child, ok := a.memo[childKey]
		if !ok {
			return nil, nil, 0, 0, fmt.Errorf("internal error: missing witness state")
		}
		prefix := live - fn.FrameSize
		before := live
		var peakAfter int64
		var nextLive int64
		if edge.Kind == CallTail {
			peakAfter = max64(before, prefix+child.peak)
			nextLive = prefix + a.prepared.funcs[calleeIndex].FrameSize
		} else {
			peakAfter = before + child.peak
			nextLive = before + a.prepared.funcs[calleeIndex].FrameSize
		}
		additional := peakAfter - before
		if additional < 0 {
			additional = 0
		}
		check := CallCheck{
			Caller:      fn.Name,
			Callee:      edge.Target,
			CallerParam: curKey.param,
			CalleeParam: calleeParam,
			Tail:        edge.Kind == CallTail,
			LiveBefore:  before,
			CalleeNeed:  child.peak,
			Peak:        peakAfter,
			Additional:  additional,
			Fits:        true,
			Label:       edge.Label,
		}
		if a.options.StackLimit > 0 {
			check.Remaining = a.options.StackLimit - before
			check.Fits = peakAfter <= a.options.StackLimit
		}
		checks = append(checks, check)
		live = nextLive
		if peakAfter > maxLive {
			maxLive = peakAfter
		}
		if live > maxLive {
			maxLive = live
		}
		if edge.Kind != CallTail {
			maxFrames++
		}
		steps = append(steps, Step{
			Function:  edge.Target,
			Parameter: calleeParam,
			FrameSize: a.prepared.funcs[calleeIndex].FrameSize,
			Live:      live,
			TailCall:  edge.Kind == CallTail,
		})
		curKey = childKey
		cur = child
	}
	return steps, checks, maxFrames, maxLive, nil
}

func checkedAdd(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
