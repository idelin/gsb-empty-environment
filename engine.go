// Package recovery implements a content-addressed recovery planner for task
// pipelines. Tasks consume and produce versioned files; when files are
// corrupted or edited, only the affected tasks and their downstream
// successors are re-run, while already-succeeded independent branches are
// reused. A re-run whose outputs hash to the same content digest stops
// further invalidation (early cutoff), and failed tasks are retried.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// digestOf returns the content digest (SHA-256, hex) of data.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Task is a node in the pipeline graph. Run must read Inputs from the store
// and write every file in Outputs.
type Task struct {
	Name    string
	Inputs  []string
	Outputs []string
	Run     func(fs *MemFS) error
}

// Engine plans and executes builds and recoveries over a task graph.
type Engine struct {
	fs           *MemFS
	order        []*Task            // topological order
	producer     map[string]*Task   // output path -> producing task
	consumers    map[string][]*Task // file path -> consuming tasks
	maxRetries   int
	inputDigest  map[string]map[string]string // task name -> input path -> digest at last success
	outputDigest map[string]map[string]string // task name -> output path -> digest at last success
	execCount    map[string]int
}

// NewEngine validates the task graph and returns an Engine. maxRetries is the
// number of extra attempts after the first one for a failing task.
func NewEngine(fs *MemFS, tasks []*Task, maxRetries int) (*Engine, error) {
	if maxRetries < 0 {
		maxRetries = 0
	}
	e := &Engine{
		fs:           fs,
		producer:     make(map[string]*Task),
		consumers:    make(map[string][]*Task),
		maxRetries:   maxRetries,
		inputDigest:  make(map[string]map[string]string),
		outputDigest: make(map[string]map[string]string),
		execCount:    make(map[string]int),
	}
	seen := make(map[string]bool)
	for _, t := range tasks {
		if t.Name == "" {
			return nil, fmt.Errorf("task with empty name")
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("duplicate task name %q", t.Name)
		}
		seen[t.Name] = true
		if t.Run == nil {
			return nil, fmt.Errorf("task %q has nil Run", t.Name)
		}
		for _, out := range t.Outputs {
			if prev, ok := e.producer[out]; ok {
				return nil, fmt.Errorf("file %q produced by both %q and %q", out, prev.Name, t.Name)
			}
			e.producer[out] = t
		}
	}
	for _, t := range tasks {
		for _, in := range t.Inputs {
			e.consumers[in] = append(e.consumers[in], t)
		}
	}
	order, err := topoSort(tasks, e.producer)
	if err != nil {
		return nil, err
	}
	e.order = order
	return e, nil
}

// topoSort orders tasks so that producers run before consumers, and detects
// dependency cycles.
func topoSort(tasks []*Task, producer map[string]*Task) ([]*Task, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[*Task]int)
	var order []*Task
	var visit func(t *Task) error
	visit = func(t *Task) error {
		switch color[t] {
		case black:
			return nil
		case gray:
			return fmt.Errorf("dependency cycle involving task %q", t.Name)
		}
		color[t] = gray
		for _, in := range t.Inputs {
			if p, ok := producer[in]; ok && p != t {
				if err := visit(p); err != nil {
					return err
				}
			}
		}
		color[t] = black
		order = append(order, t)
		return nil
	}
	for _, t := range tasks {
		if err := visit(t); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// ExecCount reports how many times a task's Run has been invoked, including
// failed attempts.
func (e *Engine) ExecCount(name string) int {
	return e.execCount[name]
}

// ResetExecCounts zeroes all execution counters.
func (e *Engine) ResetExecCounts() {
	e.execCount = make(map[string]int)
}

// currentDigest returns the digest of the file currently in the store, or ""
// if the file is missing (which never collides with a real digest).
func (e *Engine) currentDigest(path string) string {
	data, ok := e.fs.Read(path)
	if !ok {
		return ""
	}
	return digestOf(data)
}

// record snapshots the input and output digests of a task after a successful
// run.
func (e *Engine) record(t *Task) error {
	in := make(map[string]string)
	for _, path := range t.Inputs {
		data, ok := e.fs.Read(path)
		if !ok {
			return fmt.Errorf("task %q: input %q missing after run", t.Name, path)
		}
		in[path] = digestOf(data)
	}
	out := make(map[string]string)
	for _, path := range t.Outputs {
		data, ok := e.fs.Read(path)
		if !ok {
			return fmt.Errorf("task %q: output %q missing after run", t.Name, path)
		}
		out[path] = digestOf(data)
	}
	e.inputDigest[t.Name] = in
	e.outputDigest[t.Name] = out
	return nil
}

// runTask executes a task, retrying on failure up to maxRetries extra times,
// and records its digests on success.
func (e *Engine) runTask(t *Task) error {
	var err error
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		e.execCount[t.Name]++
		if err = t.Run(e.fs); err == nil {
			return e.record(t)
		}
	}
	return fmt.Errorf("task %q failed after %d attempts: %v", t.Name, e.maxRetries+1, err)
}

// Build runs every task in topological order (a full rebuild).
func (e *Engine) Build() error {
	for _, t := range e.order {
		if err := e.runTask(t); err != nil {
			return err
		}
	}
	return nil
}

// Recover re-runs only the tasks affected by corrupted or edited files plus
// their downstream successors. A re-run that reproduces byte-identical
// outputs stops the invalidation from propagating further (early cutoff).
// Tasks on independent, still-valid branches are never re-run.
func (e *Engine) Recover() error {
	dirty := make(map[*Task]bool)
	for _, t := range e.order {
		// Own outputs corrupted or deleted on disk.
		for _, out := range t.Outputs {
			if e.currentDigest(out) != e.outputDigest[t.Name][out] {
				dirty[t] = true
			}
		}
		// Source inputs (not produced by any task) edited externally.
		for _, in := range t.Inputs {
			if _, produced := e.producer[in]; produced {
				continue
			}
			if e.currentDigest(in) != e.inputDigest[t.Name][in] {
				dirty[t] = true
			}
		}
	}
	for _, t := range e.order {
		if !dirty[t] {
			continue
		}
		previous := e.outputDigest[t.Name]
		if err := e.runTask(t); err != nil {
			return err
		}
		// Propagate invalidation only through outputs whose content digest
		// actually changed; identical re-run results cut off the cascade.
		for _, out := range t.Outputs {
			if e.outputDigest[t.Name][out] != previous[out] {
				for _, consumer := range e.consumers[out] {
					dirty[consumer] = true
				}
			}
		}
	}
	return nil
}

// FileDigest exposes the current on-disk digest of a path, for tests and
// callers that verify digest propagation.
func (e *Engine) FileDigest(path string) string {
	return e.currentDigest(path)
}

// RecordedOutputDigest exposes the digest recorded for a task output at its
// last successful run.
func (e *Engine) RecordedOutputDigest(task, path string) string {
	return e.outputDigest[task][path]
}

// SortedOutputs lists all produced file paths, for deterministic comparison.
func (e *Engine) SortedOutputs() []string {
	var paths []string
	for p := range e.producer {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
