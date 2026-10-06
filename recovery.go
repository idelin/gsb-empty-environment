// Package recovery implements an incremental execution planner.
//
// Tasks declare the files they read and the tasks they depend on. The
// planner fingerprints every input by content digest and persists one
// record per task in a Store. On subsequent runs a task is reused when
// its recorded input digest still matches; otherwise it is re-executed.
// Because downstream tasks fingerprint the *output digests* of their
// dependencies, a re-run that yields identical content stops the
// invalidation from propagating any further (early cut-off). Failed
// tasks are recorded as not-OK and are therefore retried on the next
// run, while their dependents stay blocked until they succeed.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
)

// Task is a unit of work in the dependency graph.
type Task struct {
	Name  string                      // unique task name
	Files []string                    // files this task reads
	Deps  []string                    // names of tasks this task depends on
	Run   func(Input) ([]byte, error) // produces the task output
}

// Input gives a running task access to its declared inputs only.
type Input struct {
	files   map[string][]byte
	outputs map[string][]byte
}

// File returns the content of a declared file dependency.
func (in Input) File(path string) ([]byte, error) {
	b, ok := in.files[path]
	if !ok {
		return nil, fmt.Errorf("read of undeclared file %q", path)
	}
	return b, nil
}

// Output returns the output of a declared task dependency.
func (in Input) Output(task string) ([]byte, error) {
	b, ok := in.outputs[task]
	if !ok {
		return nil, fmt.Errorf("read of undeclared dependency %q", task)
	}
	return b, nil
}

// Planner schedules and executes tasks against a Store.
type Planner struct {
	Store Store
	// ReadFile loads file contents; defaults to os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// Retries is the number of extra attempts granted to a failing
	// task within a single Run call. Zero means a single attempt.
	Retries int
}

// Report describes what a Run call did, in topological order.
type Report struct {
	Ran     []string // tasks executed (and succeeded)
	Reused  []string // tasks served from the store
	Failed  []string // tasks that exhausted their attempts
	Blocked []string // tasks skipped because a dependency was unavailable
}

// Run plans and executes the dirty subset of tasks. Tasks whose inputs
// are unchanged are reused; tasks that re-run but reproduce identical
// output content do not invalidate their dependents.
func (p *Planner) Run(tasks []Task) (Report, error) {
	order, byName, err := topoSort(tasks)
	if err != nil {
		return Report{}, err
	}
	readFile := p.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	attempts := 1 + p.Retries
	if attempts < 1 {
		attempts = 1
	}

	var rep Report
	unavailable := map[string]bool{}
	outputs := map[string][]byte{}
	outDigest := map[string]string{}
	fileCache := map[string][]byte{}

	for _, name := range order {
		t := byName[name]

		blocked := false
		for _, d := range t.Deps {
			if unavailable[d] {
				blocked = true
				break
			}
		}
		if blocked {
			unavailable[name] = true
			rep.Blocked = append(rep.Blocked, name)
			continue
		}

		// Fingerprint declared files.
		in := Input{files: map[string][]byte{}, outputs: map[string][]byte{}}
		h := sha256.New()
		files := append([]string(nil), t.Files...)
		sort.Strings(files)
		fileErr := false
		for _, f := range files {
			content, ok := fileCache[f]
			if !ok {
				content, err = readFile(f)
				if err != nil {
					fileErr = true
					break
				}
				fileCache[f] = content
			}
			in.files[f] = content
			h.Write([]byte(f))
			h.Write([]byte{0})
			h.Write(content)
			h.Write([]byte{0})
		}
		if fileErr {
			unavailable[name] = true
			rep.Failed = append(rep.Failed, name)
			continue
		}

		// Fingerprint dependency output digests.
		deps := append([]string(nil), t.Deps...)
		sort.Strings(deps)
		for _, d := range deps {
			in.outputs[d] = outputs[d]
			h.Write([]byte(d))
			h.Write([]byte{0})
			h.Write([]byte(outDigest[d]))
			h.Write([]byte{0})
		}
		digest := hex.EncodeToString(h.Sum(nil))

		if rec, ok := p.Store.Get(name); ok && rec.OK && rec.InputDigest == digest {
			outputs[name] = rec.Output
			outDigest[name] = rec.OutputDigest
			rep.Reused = append(rep.Reused, name)
			continue
		}

		var out []byte
		var runErr error
		for i := 0; i < attempts; i++ {
			out, runErr = t.Run(in)
			if runErr == nil {
				break
			}
		}
		if runErr != nil {
			// Record the failure so the task is retried next run.
			_ = p.Store.Put(name, Record{OK: false, InputDigest: digest})
			unavailable[name] = true
			rep.Failed = append(rep.Failed, name)
			continue
		}

		rec := Record{
			OK:           true,
			InputDigest:  digest,
			Output:       out,
			OutputDigest: digestBytes(out),
		}
		if err := p.Store.Put(name, rec); err != nil {
			return rep, fmt.Errorf("persist record for %q: %w", name, err)
		}
		outputs[name] = out
		outDigest[name] = rec.OutputDigest
		rep.Ran = append(rep.Ran, name)
	}
	return rep, nil
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// topoSort returns a deterministic topological order and a name index.
func topoSort(tasks []Task) ([]string, map[string]Task, error) {
	byName := map[string]Task{}
	for _, t := range tasks {
		if t.Name == "" {
			return nil, nil, fmt.Errorf("task with empty name")
		}
		if t.Run == nil {
			return nil, nil, fmt.Errorf("task %q has no Run function", t.Name)
		}
		if _, dup := byName[t.Name]; dup {
			return nil, nil, fmt.Errorf("duplicate task %q", t.Name)
		}
		byName[t.Name] = t
	}
	indegree := map[string]int{}
	children := map[string][]string{}
	for _, t := range tasks {
		indegree[t.Name] = 0
	}
	for _, t := range tasks {
		for _, d := range t.Deps {
			if _, ok := byName[d]; !ok {
				return nil, nil, fmt.Errorf("task %q depends on unknown task %q", t.Name, d)
			}
			indegree[t.Name]++
			children[d] = append(children[d], t.Name)
		}
	}
	var ready []string
	for name, deg := range indegree {
		if deg == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		next := append([]string(nil), children[name]...)
		sort.Strings(next)
		for _, c := range next {
			indegree[c]--
			if indegree[c] == 0 {
				ready = append(ready, c)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(tasks) {
		return nil, nil, fmt.Errorf("dependency cycle detected")
	}
	return order, byName, nil
}
