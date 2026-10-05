package incbuild

import (
	"fmt"
	"path/filepath"
	"sort"
)

// Engine plans and runs incremental builds over a fixed set of tasks.
type Engine struct {
	Dir   string
	Tasks []*Task
	Store *Store
	// MaxPasses bounds the number of scheduling passes before the build is
	// declared non-convergent (e.g. a task keeps modifying its own inputs).
	// Zero means 8.
	MaxPasses int
}

// build holds the transient state of one Build invocation.
type build struct {
	eng      *Engine
	tasks    map[string]*Task
	producer map[string]string // file -> producing task name

	changed        map[string]Reason // files known changed this build, with root cause
	pendingInvalid map[string]Reason // tasks invalidated, to re-run next pass
	stale          map[string]bool

	result *BuildResult
}

// Build plans and executes one incremental build, updating e.Store.
func (e *Engine) Build() (*BuildResult, error) {
	if e.Store == nil {
		e.Store = NewStore()
	}
	maxPasses := e.MaxPasses
	if maxPasses <= 0 {
		maxPasses = 8
	}
	b := &build{
		eng:            e,
		tasks:          map[string]*Task{},
		producer:       map[string]string{},
		changed:        map[string]Reason{},
		pendingInvalid: map[string]Reason{},
		stale:          map[string]bool{},
		result: &BuildResult{
			Runs:    map[string]int{},
			Reasons: map[string]Reason{},
		},
	}
	for _, t := range e.Tasks {
		if _, dup := b.tasks[t.Name]; dup {
			return nil, fmt.Errorf("duplicate task name %q", t.Name)
		}
		b.tasks[t.Name] = t
	}
	for _, t := range e.Tasks {
		for _, o := range t.Outputs {
			if p, dup := b.producer[o]; dup {
				return nil, fmt.Errorf("file %q is produced by both %q and %q", o, p, t.Name)
			}
			b.producer[o] = t.Name
		}
	}
	b.seedSourceChanges()
	for pass := 1; ; pass++ {
		if pass > maxPasses {
			return nil, fmt.Errorf("build did not converge after %d passes (a task may be modifying its own inputs)", maxPasses)
		}
		order, err := b.topo()
		if err != nil {
			return nil, err
		}
		for _, name := range order {
			t := b.tasks[name]
			st := b.state(name)
			reason, dirty := b.evalDirty(t, st)
			if !dirty {
				continue
			}
			if err := b.runTask(t, st, reason); err != nil {
				return nil, err
			}
		}
		// Convergence check: no task may hold a result computed from
		// content that no longer matches the disk. This discards stale
		// results caused by mid-run source changes or by dependencies
		// discovered too late to order this pass.
		b.validate()
		if len(b.pendingInvalid) == 0 {
			break
		}
	}
	return b.result, nil
}

func (b *build) state(name string) *TaskState {
	st, ok := b.eng.Store.Tasks[name]
	if !ok {
		st = &TaskState{}
		b.eng.Store.Tasks[name] = st
	}
	if st.Inputs == nil {
		st.Inputs = map[string]string{}
	}
	if st.Outputs == nil {
		st.Outputs = map[string]string{}
	}
	return st
}

func (b *build) hash(file string) string {
	return hashFile(filepath.Join(b.eng.Dir, file))
}

func (b *build) snapshot(files []string) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f] = b.hash(f)
	}
	return m
}

// seedSourceChanges records root causes for source files (no producing
// task) whose on-disk content differs from what any consumer recorded.
func (b *build) seedSourceChanges() {
	for _, name := range b.sortedTaskNames() {
		t := b.tasks[name]
		st := b.state(name)
		for _, f := range contentInputs(t, st) {
			if _, generated := b.producer[f]; generated {
				continue
			}
			cur := b.hash(f)
			if st.Inputs[f] != cur {
				if _, seen := b.changed[f]; !seen {
					b.changed[f] = Reason{Steps: []string{fmt.Sprintf("file %q changed on disk (hash %s -> %s)", f, shortHash(st.Inputs[f]), shortHash(cur))}}
				}
			}
		}
	}
}

// topo returns task names in dependency order. Both content and order-only
// edges constrain ordering; only content edges propagate invalidation.
func (b *build) topo() ([]string, error) {
	indeg := map[string]int{}
	adj := map[string][]string{}
	for name := range b.tasks {
		indeg[name] = 0
	}
	for _, t := range b.tasks {
		st := b.state(t.Name)
		deps := unionStrings(unionStrings(t.Inputs, t.OrderOnly), st.Discovered)
		seen := map[string]bool{}
		for _, f := range deps {
			p, ok := b.producer[f]
			if !ok || p == t.Name || seen[p] {
				continue
			}
			seen[p] = true
			adj[p] = append(adj[p], t.Name)
			indeg[t.Name]++
		}
	}
	var ready []string
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				i := sort.SearchStrings(ready, m)
				ready = append(ready, "")
				copy(ready[i+1:], ready[i:])
				ready[i] = m
			}
		}
	}
	if len(order) != len(b.tasks) {
		return nil, fmt.Errorf("dependency cycle detected among tasks")
	}
	return order, nil
}

// evalDirty decides whether a task must (re)run and returns the shortest
// reason chain. Invalidation propagates only along content dependencies.
func (b *build) evalDirty(t *Task, st *TaskState) (Reason, bool) {
	var causes []Reason
	if !st.Ran {
		causes = append(causes, Reason{Steps: []string{fmt.Sprintf("task %q rebuilt: no previous build record", t.Name)}})
	} else if commandHash(t.Cmd) != st.CmdHash {
		causes = append(causes, Reason{Steps: []string{fmt.Sprintf("task %q rebuilt: command changed", t.Name)}})
	}
	for _, o := range t.Outputs {
		cur := b.hash(o)
		if st.Outputs[o] != cur {
			causes = append(causes, Reason{Steps: []string{fmt.Sprintf("task %q rebuilt: output %q missing or modified outside the build (hash %s -> %s)", t.Name, o, shortHash(st.Outputs[o]), shortHash(cur))}})
		}
	}
	if r, ok := b.pendingInvalid[t.Name]; ok {
		causes = append(causes, r)
		delete(b.pendingInvalid, t.Name)
	}
	for _, f := range contentInputs(t, st) {
		cur := b.hash(f)
		if st.Inputs[f] == cur {
			continue
		}
		r, known := b.changed[f]
		if !known {
			r = Reason{Steps: []string{fmt.Sprintf("file %q changed on disk (hash %s -> %s)", f, shortHash(st.Inputs[f]), shortHash(cur))}}
		}
		causes = append(causes, extend(r, fmt.Sprintf("task %q rebuilt: input %q changed", t.Name, f)))
	}
	if len(causes) == 0 {
		return Reason{}, false
	}
	best := causes[0]
	for _, c := range causes[1:] {
		best = shorter(best, c)
	}
	return best, true
}

// runTask executes one task, verifies that its inputs did not change while
// it ran, merges dynamically discovered dependencies, and records output
// hashes so downstream tasks are invalidated only when content actually
// changes (restat semantics).
func (b *build) runTask(t *Task, st *TaskState, reason Reason) error {
	inputs := contentInputs(t, st)
	pre := b.snapshot(inputs)
	rc := &RunContext{Dir: b.eng.Dir, Task: t.Name}
	if err := t.Run(rc); err != nil {
		return fmt.Errorf("task %q failed: %w", t.Name, err)
	}
	b.result.Runs[t.Name]++
	b.noteRebuilt(t.Name, reason)
	post := b.snapshot(unionStrings(inputs, rc.discovered))
	for _, f := range inputs {
		if pre[f] != post[f] {
			// A source file changed while the task was running: the
			// result may be stale, discard it and re-run next pass.
			b.pendingInvalid[t.Name] = Reason{Steps: []string{
				fmt.Sprintf("file %q changed while task %q was running (hash %s -> %s)", f, t.Name, shortHash(pre[f]), shortHash(post[f])),
				fmt.Sprintf("task %q rebuilt: input %q changed during its run", t.Name, f),
			}}
			b.markStale(t.Name)
			st.Discovered = unionStrings(st.Discovered, rc.discovered)
			return nil
		}
	}
	oldOutputs := st.Outputs
	st.Ran = true
	st.CmdHash = commandHash(t.Cmd)
	st.Discovered = append([]string(nil), rc.discovered...)
	st.Inputs = post
	st.Outputs = b.snapshot(t.Outputs)
	for _, o := range t.Outputs {
		if oldOutputs[o] != st.Outputs[o] {
			b.changed[o] = extend(reason, fmt.Sprintf("file %q regenerated by task %q", o, t.Name))
		}
	}
	return nil
}

// validate discards stale results: any task whose recorded content inputs
// no longer match the disk is invalidated and re-run in the next pass.
// This catches inputs changed mid-run by external writers and dependencies
// discovered after their producer had already been scheduled.
func (b *build) validate() {
	for _, name := range b.sortedTaskNames() {
		t := b.tasks[name]
		st := b.eng.Store.Tasks[name]
		if !st.Ran {
			continue
		}
		if _, pending := b.pendingInvalid[name]; pending {
			continue
		}
		for _, f := range contentInputs(t, st) {
			cur := b.hash(f)
			if st.Inputs[f] != cur {
				b.pendingInvalid[name] = Reason{Steps: []string{
					fmt.Sprintf("file %q changed during the build (hash %s -> %s); stale result of task %q discarded", f, shortHash(st.Inputs[f]), shortHash(cur), name),
					fmt.Sprintf("task %q rebuilt: input %q no longer matches what it read", name, f),
				}}
				b.markStale(name)
				break
			}
		}
	}
}

func (b *build) noteRebuilt(name string, r Reason) {
	if _, ok := b.result.Reasons[name]; !ok {
		b.result.Rebuilt = append(b.result.Rebuilt, name)
		b.result.Reasons[name] = r
		return
	}
	b.result.Reasons[name] = shorter(b.result.Reasons[name], r)
}

func (b *build) markStale(name string) {
	if !b.stale[name] {
		b.stale[name] = true
		b.result.StaleDiscarded = append(b.result.StaleDiscarded, name)
	}
}

func (b *build) sortedTaskNames() []string {
	names := make([]string, 0, len(b.tasks))
	for name := range b.tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
