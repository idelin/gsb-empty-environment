package sim

import "fmt"

// Emit records one observation into the run's trace. Workloads call it
// from inside event handlers with a tag and a state sample.
type Emit func(tag string, state uint64)

// Workload schedules the initial events of a simulation and emits trace
// observations from its handlers.
type Workload func(e *Engine, emit Emit)

// Invariant checks one observation; returning non-nil fails the run.
type Invariant func(step uint64, tag string, state uint64) error

// Entry is one recorded observation.
type Entry struct {
	Step  uint64
	Time  uint64
	Tag   string
	State uint64
}

// Result summarizes one workload run.
type Result struct {
	Seed     uint64
	Steps    uint64  // events executed
	Hash     uint64  // FNV-1a over the whole trace
	Entries  []Entry // recorded observations (nil when KeepTrace is false)
	FailStep uint64  // step at which the invariant failed, 0 if none
	Err      error   // invariant failure, nil on success
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// RunWorkload executes w for up to maxSteps events, recording a trace
// and checking inv after every observation. The first invariant failure
// stops the engine and is reported in the Result.
func RunWorkload(seed uint64, maxSteps uint64, w Workload, inv Invariant) *Result {
	return run(seed, maxSteps, w, inv, true)
}

// RunWorkloadNoTrace is RunWorkload without keeping the entry list;
// the hash is still computed, which suits million-step regression runs.
func RunWorkloadNoTrace(seed uint64, maxSteps uint64, w Workload, inv Invariant) *Result {
	return run(seed, maxSteps, w, inv, false)
}

func run(seed uint64, maxSteps uint64, w Workload, inv Invariant, keep bool) *Result {
	e := New(seed)
	res := &Result{Seed: seed}
	h := uint64(fnvOffset)
	mix := func(v uint64) { h ^= v; h *= fnvPrime }
	var failed bool
	emit := func(tag string, state uint64) {
		step := e.Step()
		now := e.Now()
		mix(step)
		mix(now)
		mix(state)
		for i := 0; i < len(tag); i++ {
			mix(uint64(tag[i]))
		}
		if keep {
			res.Entries = append(res.Entries, Entry{Step: step, Time: now, Tag: tag, State: state})
		}
		if !failed && inv != nil {
			if err := inv(step, tag, state); err != nil {
				failed = true
				res.FailStep = step
				res.Err = err
				e.Stop()
			}
		}
	}
	w(e, emit)
	e.RunSteps(maxSteps)
	res.Steps = e.Step()
	res.Hash = h
	return res
}

// Reproduce re-runs the exact workload and seed of orig and reports
// whether the failure (or clean run) is reproduced bit-for-bit: same
// trace hash, same step count, same failing step.
func Reproduce(orig *Result, maxSteps uint64, w Workload, inv Invariant) (bool, *Result) {
	again := RunWorkloadNoTrace(orig.Seed, maxSteps, w, inv)
	ok := again.Hash == orig.Hash &&
		again.Steps == orig.Steps &&
		again.FailStep == orig.FailStep &&
		(again.Err == nil) == (orig.Err == nil)
	return ok, again
}

// ReproReport renders a human-readable reproduction summary.
func ReproReport(orig *Result, reproduced bool) string {
	status := "CLEAN"
	if orig.Err != nil {
		status = fmt.Sprintf("FAILED at step %d: %v", orig.FailStep, orig.Err)
	}
	confirm := "not reproduced"
	if reproduced {
		confirm = "reproduced deterministically"
	}
	return fmt.Sprintf("seed=%d steps=%d hash=%016x %s (%s)",
		orig.Seed, orig.Steps, orig.Hash, status, confirm)
}
