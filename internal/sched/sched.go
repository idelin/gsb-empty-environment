// Package sched is a deterministic cooperative scheduler that enumerates
// every interleaving of a set of threads, for verifying concurrent
// algorithms.
//
// A program spawns its threads with Go and returns a check function.
// Threads call Step at each atomic-step boundary (a point where a
// preemption is allowed) and BlockUntil to park until a condition holds.
// Explore re-runs the program once per possible schedule, enumerating all
// interleavings depth-first. Only one thread runs at a time, so plain
// variables suffice for shared state and data races are impossible as
// long as threads only touch shared state between Steps.
//
// Programs must be deterministic given a schedule: the same sequence of
// scheduling choices must always produce the same behavior.
package sched

import "fmt"

// maxSteps bounds the number of steps in a single run so a livelocked
// program fails instead of hanging.
const maxSteps = 1_000_000

// Sched schedules threads cooperatively. The zero value is ready to use,
// but threads are normally managed through Explore.
type Sched struct {
	threads []*thread
	current *thread
}

type thread struct {
	resume  chan struct{}
	yielded chan struct{}
	blocked func() bool // non-nil while parked in BlockUntil
	done    bool
}

// Go starts f as a new thread. It may be called before the run starts or
// from inside a running thread.
func (s *Sched) Go(f func()) {
	th := &thread{resume: make(chan struct{}), yielded: make(chan struct{})}
	s.threads = append(s.threads, th)
	go func() {
		<-th.resume
		f()
		th.done = true
		th.yielded <- struct{}{}
	}()
}

// Step ends the current atomic step: the thread may be preempted here and
// any other runnable thread may run next.
func (s *Sched) Step() {
	th := s.current
	th.yielded <- struct{}{}
	<-th.resume
}

// BlockUntil parks the current thread until cond reports true. cond is
// evaluated by the scheduler between steps and must be pure.
func (s *Sched) BlockUntil(cond func() bool) {
	th := s.current
	th.blocked = cond
	th.yielded <- struct{}{}
	<-th.resume
	th.blocked = nil
}

// runnable returns the indices of threads that can run now.
func (s *Sched) runnable() []int {
	var out []int
	for i, th := range s.threads {
		if th.done {
			continue
		}
		if th.blocked != nil && !th.blocked() {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (s *Sched) allDone() bool {
	for _, th := range s.threads {
		if !th.done {
			return false
		}
	}
	return true
}

// Config bounds an exploration.
type Config struct {
	// MaxRuns bounds the number of explored schedules; 0 means unbounded.
	MaxRuns int
	// MaxPreemptions bounds the number of preemptions per schedule: a
	// preemption is a switch away from a thread that was still runnable
	// (forced switches away from blocked or finished threads are free).
	// Negative means unbounded. Preemption bounding, as in CHESS, keeps
	// large programs tractable while still covering the interleavings
	// where races actually happen.
	MaxPreemptions int
}

// Explore runs program once per possible schedule and returns the number
// of runs explored. program is called synchronously at the start of each
// run, must spawn its threads with s.Go, and returns a check function;
// check is invoked after each completed run to assert invariants over
// that run's final state.
//
// Explore returns an error if a run deadlocks, exceeds the step limit,
// exceeds cfg.MaxRuns, or the program behaves nondeterministically for a
// fixed schedule.
func Explore(program func(s *Sched) (check func()), cfg Config) (int, error) {
	var taken []int     // thread chosen at each decision depth of the current path
	var options [][]int // runnable threads seen at each depth
	runs := 0
	for {
		if cfg.MaxRuns > 0 && runs >= cfg.MaxRuns {
			return runs, fmt.Errorf("sched: exploration exceeded MaxRuns=%d", cfg.MaxRuns)
		}
		s := new(Sched)
		check := program(s)

		depth := 0
		steps := 0
		preemptions := 0
		lastRan := -1
		for {
			runnable := s.runnable()
			if len(runnable) == 0 {
				if s.allDone() {
					break
				}
				return runs, fmt.Errorf("sched: deadlock, no runnable thread")
			}
			// Switching to a different thread while the last-run thread
			// is still runnable is a preemption; switching away from a
			// blocked or finished thread is forced and therefore free.
			lastRunnable := contains(runnable, lastRan)
			var choice int
			if depth < len(taken) {
				choice = taken[depth]
				if !contains(runnable, choice) {
					return runs, fmt.Errorf("sched: nondeterministic program, thread %d not runnable at depth %d", choice, depth)
				}
			} else {
				allowed := runnable
				if cfg.MaxPreemptions >= 0 && preemptions >= cfg.MaxPreemptions && lastRunnable {
					// Preemption budget exhausted: only the
					// currently running thread may continue.
					allowed = []int{lastRan}
				}
				options = append(options, allowed)
				choice = allowed[0]
				taken = append(taken, choice)
			}
			if choice != lastRan && lastRunnable {
				preemptions++
			}
			lastRan = choice
			depth++
			steps++
			if steps > maxSteps {
				return runs, fmt.Errorf("sched: run exceeded %d steps", maxSteps)
			}
			th := s.threads[choice]
			s.current = th
			th.resume <- struct{}{}
			<-th.yielded
		}
		runs++
		if check != nil {
			check()
		}

		// Backtrack: find the deepest decision with an untried option.
		for len(taken) > 0 {
			d := len(taken) - 1
			i := indexOf(options[d], taken[d])
			if i+1 < len(options[d]) {
				taken[d] = options[d][i+1]
				taken = taken[:d+1]
				options = options[:d+1]
				break
			}
			taken = taken[:d]
			options = options[:d]
		}
		if len(taken) == 0 {
			return runs, nil
		}
	}
}

func contains(xs []int, v int) bool {
	return indexOf(xs, v) >= 0
}

func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}
