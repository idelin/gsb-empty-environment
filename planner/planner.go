package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TaskState is the committed fingerprint of one task from the last build.
type TaskState struct {
	Version    string
	Deps       []Dep             // static + discovered dependencies
	DepSigs    map[string]string // content dep node -> signature at commit time
	OutputSigs map[string]string // output file -> signature at commit time
}

// State is the persistent record the planner uses to detect changes.
type State struct {
	Tasks map[string]*TaskState
}

// Planner schedules incremental builds over a directory tree.
type Planner struct {
	root       string
	tasks      map[string]*Task
	order      []string          // task names in registration order
	producers  map[string]string // generated file -> producing task
	state      *State
	maxRetries int
}

// New creates a planner operating on files under root.
func New(root string) *Planner {
	return &Planner{
		root:       root,
		tasks:      map[string]*Task{},
		producers:  map[string]string{},
		state:      &State{Tasks: map[string]*TaskState{}},
		maxRetries: 5,
	}
}

// AddTask registers a task. Generated outputs must be disjoint across tasks.
func (p *Planner) AddTask(t *Task) error {
	if t.Name == "" {
		return fmt.Errorf("task name must not be empty")
	}
	if _, ok := p.tasks[t.Name]; ok {
		return fmt.Errorf("duplicate task %q", t.Name)
	}
	for _, o := range t.Outputs {
		if prev, ok := p.producers[o]; ok {
			return fmt.Errorf("output %q produced by both %q and %q", o, prev, t.Name)
		}
		p.producers[o] = t.Name
	}
	p.tasks[t.Name] = t
	p.order = append(p.order, t.Name)
	return nil
}

func (p *Planner) abs(path string) string { return filepath.Join(p.root, path) }

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fileSig returns the content signature of a file, or ok=false if missing.
func (p *Planner) fileSig(path string) (string, bool) {
	data, err := ioutil.ReadFile(p.abs(path))
	if err != nil {
		return "", false
	}
	return hashBytes(data), true
}

// producerOf resolves a node to the task that must run before it is fresh:
// the task itself for task nodes, the producer for generated files, or "" for
// source files.
func (p *Planner) producerOf(node string) string {
	if _, ok := p.tasks[node]; ok {
		return node
	}
	return p.producers[node]
}

// sigOf computes the current observable signature of a node.
func (p *Planner) sigOf(node string) string {
	if t, ok := p.tasks[node]; ok {
		parts := make([]string, 0, len(t.Outputs))
		for _, o := range t.Outputs {
			s, ok := p.fileSig(o)
			if !ok {
				s = "<missing>"
			}
			parts = append(parts, o+"="+s)
		}
		sort.Strings(parts)
		return "task:" + hashBytes([]byte(strings.Join(parts, ",")))
	}
	if s, ok := p.fileSig(node); ok {
		return s
	}
	return "<missing>"
}

// SaveState persists the build state as JSON.
func (p *Planner) SaveState(path string) error {
	data, err := json.MarshalIndent(p.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

// LoadState restores previously saved build state; a missing file is not an
// error and simply yields an empty state.
func (p *Planner) LoadState(path string) error {
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	st := &State{Tasks: map[string]*TaskState{}}
	if err := json.Unmarshal(data, st); err != nil {
		return err
	}
	if st.Tasks == nil {
		st.Tasks = map[string]*TaskState{}
	}
	p.state = st
	return nil
}

// buildCtx holds per-build mutable data.
type buildCtx struct {
	p       *Planner
	known   map[string][]Dep     // task -> static + discovered deps (grows)
	reasons map[string]*Reason   // task -> shortest invalidation chain
	notes   map[string][]*Reason // task -> why discarded attempts were rejected
	runs    map[string]int       // task -> discarded run count (retry guard)
	rebuilt []string
}

// Build brings every task up to date and reports what was rebuilt and why.
func (p *Planner) Build() (*Result, error) {
	bc := &buildCtx{
		p:       p,
		known:   map[string][]Dep{},
		reasons: map[string]*Reason{},
		notes:   map[string][]*Reason{},
		runs:    map[string]int{},
	}
	for _, name := range p.order {
		deps := append([]Dep{}, p.tasks[name].Deps...)
		if st := p.state.Tasks[name]; st != nil {
			deps = mergeDeps(deps, st.Deps)
		}
		bc.known[name] = deps
	}
	for {
		order, dirty, err := bc.dirtyMap()
		if err != nil {
			return nil, err
		}
		next := ""
		for _, name := range order {
			if _, isDirty := dirty[name]; !isDirty {
				continue
			}
			ready := true
			for _, d := range bc.known[name] {
				prod := p.producerOf(d.Node)
				if prod == "" || prod == name {
					continue
				}
				if _, pending := dirty[prod]; pending {
					ready = false
					break
				}
			}
			if ready {
				next = name
				break
			}
		}
		if next == "" {
			if len(dirty) > 0 {
				names := make([]string, 0, len(dirty))
				for n := range dirty {
					names = append(names, n)
				}
				sort.Strings(names)
				return nil, fmt.Errorf("cannot schedule dirty tasks (dependency cycle?): %s", strings.Join(names, ", "))
			}
			break
		}
		if err := bc.run(p.tasks[next]); err != nil {
			return nil, err
		}
	}
	return &Result{Rebuilt: bc.rebuilt, Reasons: bc.reasons, Notes: bc.notes}, nil
}

// dirtyMap topologically sorts the task graph (both dep kinds order the
// schedule) and evaluates dirtiness in that order so that invalidation
// propagates only along content edges.
func (bc *buildCtx) dirtyMap() ([]string, map[string]*Reason, error) {
	order, err := bc.topo()
	if err != nil {
		return nil, nil, err
	}
	dirty := map[string]*Reason{}
	for _, name := range order {
		if ok, r := bc.evalDirty(name, dirty); ok {
			dirty[name] = r
			bc.noteReason(name, r)
		}
	}
	return order, dirty, nil
}

func (bc *buildCtx) topo() ([]string, error) {
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for _, name := range bc.p.order {
		indeg[name] = 0
	}
	for name, deps := range bc.known {
		seen := map[string]bool{}
		for _, d := range deps {
			prod := bc.p.producerOf(d.Node)
			if prod == "" || prod == name || seen[prod] {
				continue
			}
			seen[prod] = true
			indeg[name]++
			dependents[prod] = append(dependents[prod], name)
		}
	}
	var ready []string
	for _, name := range bc.p.order {
		if indeg[name] == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	var out []string
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		out = append(out, name)
		for _, dep := range dependents[name] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
				sort.Strings(ready)
			}
		}
	}
	if len(out) != len(bc.p.order) {
		return nil, fmt.Errorf("dependency cycle detected among tasks")
	}
	return out, nil
}

// evalDirty decides whether a task is dirty and, if so, returns the shortest
// reason. dirty holds tasks already known dirty in topological order.
func (bc *buildCtx) evalDirty(name string, dirty map[string]*Reason) (bool, *Reason) {
	t := bc.p.tasks[name]
	st := bc.p.state.Tasks[name]
	if st == nil {
		return true, &Reason{Node: name, Cause: "never built"}
	}
	var best *Reason
	consider := func(r *Reason) {
		if best == nil || r.Depth() < best.Depth() {
			best = r
		}
	}
	if st.Version != t.Version {
		consider(&Reason{Node: name, Cause: "task definition changed"})
	}
	for _, o := range t.Outputs {
		sig, ok := bc.p.fileSig(o)
		if !ok {
			consider(&Reason{Node: name, Cause: fmt.Sprintf("output %s is missing", o)})
		} else if st.OutputSigs[o] != sig {
			consider(&Reason{Node: name, Cause: fmt.Sprintf("output %s was modified outside the build", o)})
		}
	}
	for _, d := range bc.known[name] {
		if d.Kind != Content {
			continue // order-only deps never invalidate
		}
		if prod := bc.p.producerOf(d.Node); prod != "" && prod != name {
			if pr, pending := dirty[prod]; pending {
				consider(&Reason{
					Node:  name,
					Cause: fmt.Sprintf("input %s was rebuilt", d.Node),
					Via:   pr,
				})
				continue
			}
		}
		cur := bc.p.sigOf(d.Node)
		rec, ok := st.DepSigs[d.Node]
		if !ok || rec != cur {
			consider(&Reason{
				Node:  name,
				Cause: fmt.Sprintf("input %s changed", d.Node),
				Via:   bc.reasonForNode(d.Node),
			})
		}
	}
	return best != nil, best
}

func (bc *buildCtx) reasonForNode(node string) *Reason {
	if prod := bc.p.producerOf(node); prod != "" {
		if r := bc.reasons[prod]; r != nil {
			return r
		}
		return &Reason{Node: prod, Cause: "is being rebuilt"}
	}
	return &Reason{Node: node, Cause: "source file changed on disk"}
}

func (bc *buildCtx) noteReason(name string, r *Reason) {
	if old := bc.reasons[name]; old == nil || r.Depth() < old.Depth() {
		bc.reasons[name] = r
	}
}

// run executes one task and commits its state only if the result is provably
// fresh: sources read must not have changed mid-run, declared outputs must
// exist, and newly discovered content deps must already be up to date.
func (bc *buildCtx) run(t *Task) error {
	ctx := &RunContext{p: bc.p, task: t, reads: map[string]string{}}
	if err := t.Run(ctx); err != nil {
		return fmt.Errorf("task %s failed: %v", t.Name, err)
	}
	// A source that changed while the task was running makes the result stale.
	for f, sig := range ctx.reads {
		cur, ok := bc.p.fileSig(f)
		if !ok || cur != sig {
			if err := bc.retry(t, &Reason{
				Node:  t.Name,
				Cause: fmt.Sprintf("source file %s changed while the task was running", f),
			}); err != nil {
				return err
			}
			return nil
		}
	}
	for _, o := range t.Outputs {
		if _, ok := bc.p.fileSig(o); !ok {
			return fmt.Errorf("task %s did not produce declared output %s", t.Name, o)
		}
	}
	// Merge dependencies discovered during the run.
	added := false
	for _, d := range ctx.discovered {
		if !hasDep(bc.known[t.Name], d) {
			bc.known[t.Name] = append(bc.known[t.Name], d)
			added = true
		}
	}
	if added {
		// A newly discovered content dep may be stale or not even built yet:
		// the result just produced cannot be trusted.
		_, dirty, err := bc.dirtyMap()
		if err != nil {
			return err
		}
		for _, d := range ctx.discovered {
			if d.Kind != Content {
				continue
			}
			prod := bc.p.producerOf(d.Node)
			if prod == "" || prod == t.Name {
				continue
			}
			if pr, pending := dirty[prod]; pending {
				if err := bc.retry(t, &Reason{
					Node:  t.Name,
					Cause: fmt.Sprintf("discovered dependency %s was not up to date; rescheduled after %s", d.Node, prod),
					Via:   pr,
				}); err != nil {
					return err
				}
				return nil
			}
		}
	}
	// Commit: record the exact signatures the fresh result was built from.
	st := &TaskState{
		Version:    t.Version,
		Deps:       append([]Dep{}, bc.known[t.Name]...),
		DepSigs:    map[string]string{},
		OutputSigs: map[string]string{},
	}
	for _, d := range bc.known[t.Name] {
		if d.Kind == Content {
			st.DepSigs[d.Node] = bc.p.sigOf(d.Node)
		}
	}
	for _, o := range t.Outputs {
		sig, _ := bc.p.fileSig(o)
		st.OutputSigs[o] = sig
	}
	bc.p.state.Tasks[t.Name] = st
	bc.rebuilt = append(bc.rebuilt, t.Name)
	return nil
}

// retry discards a stale run, records why it was rejected, and enforces a
// retry limit so that perpetually changing inputs fail instead of looping
// forever.
func (bc *buildCtx) retry(t *Task, r *Reason) error {
	bc.runs[t.Name]++
	if bc.runs[t.Name] > bc.p.maxRetries {
		return fmt.Errorf("task %s: inputs keep changing; giving up after %d attempts (%s)", t.Name, bc.runs[t.Name], r.Cause)
	}
	bc.notes[t.Name] = append(bc.notes[t.Name], r)
	return nil
}

func hasDep(deps []Dep, want Dep) bool {
	for _, d := range deps {
		if d == want {
			return true
		}
	}
	return false
}

func mergeDeps(a, b []Dep) []Dep {
	out := append([]Dep{}, a...)
	for _, d := range b {
		if !hasDep(out, d) {
			out = append(out, d)
		}
	}
	return out
}
