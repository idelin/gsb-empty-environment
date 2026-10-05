// Package incbuild implements an incremental build planner.
//
// The planner distinguishes two kinds of dependencies:
//
//   - Content dependencies (Task.Inputs): a change to the content of an
//     input propagates invalidation to the consuming task and, transitively,
//     to everything that depends on the task's outputs.
//   - Order-only dependencies (Task.OrderOnly): these only constrain
//     scheduling order. Content changes never propagate along them.
//
// Tasks may discover additional content dependencies while they run
// (RunContext.Discover), e.g. a compiler emitting a depfile. Newly
// discovered edges may cause extra tasks to be scheduled in subsequent
// planning passes, but any result that was computed from stale input
// content is discarded and the task is re-run.
//
// For every rebuilt node the planner reports the shortest reason chain
// explaining why the rebuild happened.
package incbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// Task is a node in the build graph.
type Task struct {
	Name string
	// Cmd is an opaque identity of the command; changing it forces a rebuild.
	Cmd string
	// Inputs are declared content dependencies (files).
	Inputs []string
	// OrderOnly are order-only dependencies (files): they affect scheduling
	// order but their content changes never invalidate this task.
	OrderOnly []string
	// Outputs are files produced by this task.
	Outputs []string
	// Run executes the task. It may report dynamically discovered content
	// dependencies via RunContext.Discover.
	Run func(rc *RunContext) error
}

// RunContext is passed to Task.Run.
type RunContext struct {
	Dir  string // build root directory
	Task string // name of the running task

	discovered []string
}

// Discover reports content dependencies found while the task was running.
func (rc *RunContext) Discover(files ...string) {
	rc.discovered = append(rc.discovered, files...)
}

// TaskState is the persisted record of a task's last successful run.
type TaskState struct {
	Ran        bool              `json:"ran"`
	CmdHash    string            `json:"cmdHash"`
	Inputs     map[string]string `json:"inputs"` // content input file -> hash at run time
	Discovered []string          `json:"discovered"`
	Outputs    map[string]string `json:"outputs"` // output file -> hash after run
}

// Store is the persisted build state shared across build invocations.
type Store struct {
	Tasks map[string]*TaskState `json:"tasks"`
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{Tasks: map[string]*TaskState{}}
}

// LoadStore loads a store from path; a missing file yields an empty store.
func LoadStore(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewStore(), nil
	}
	if err != nil {
		return nil, err
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Tasks == nil {
		s.Tasks = map[string]*TaskState{}
	}
	return &s, nil
}

// Save persists the store to path.
func (s *Store) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Reason is a chain of human-readable steps explaining why a node was
// rebuilt, shortest first: Steps[0] is the root cause, the last step
// describes the rebuilt node itself.
type Reason struct {
	Steps []string
}

func (r Reason) String() string {
	return strings.Join(r.Steps, "\n  -> ")
}

func extend(r Reason, step string) Reason {
	steps := make([]string, len(r.Steps)+1)
	copy(steps, r.Steps)
	steps[len(r.Steps)] = step
	return Reason{Steps: steps}
}

// shorter returns the shorter of two chains, breaking ties
// lexicographically so results are deterministic.
func shorter(a, b Reason) Reason {
	if len(a.Steps) != len(b.Steps) {
		if len(a.Steps) < len(b.Steps) {
			return a
		}
		return b
	}
	if strings.Join(a.Steps, "\x00") <= strings.Join(b.Steps, "\x00") {
		return a
	}
	return b
}

// BuildResult summarizes one build invocation.
type BuildResult struct {
	// Rebuilt lists tasks that ran at least once, in first-run order.
	Rebuilt []string
	// Runs counts how many times each task ran (re-runs included).
	Runs map[string]int
	// Reasons holds the shortest reason chain per rebuilt task.
	Reasons map[string]Reason
	// StaleDiscarded lists tasks whose results were discarded because an
	// input changed underneath them (mid-run change or late discovery).
	StaleDiscarded []string
}

// Report renders the build result with per-node reason chains.
func (r *BuildResult) Report() string {
	var sb strings.Builder
	if len(r.Rebuilt) == 0 {
		sb.WriteString("up to date: nothing rebuilt\n")
		return sb.String()
	}
	for _, name := range r.Rebuilt {
		sb.WriteString("task " + quote(name) + " rebuilt (" + itoa(r.Runs[name]) + " run(s)); shortest reason chain:\n  " + r.Reasons[name].String() + "\n")
	}
	if len(r.StaleDiscarded) > 0 {
		sb.WriteString("stale results discarded for: " + strings.Join(r.StaleDiscarded, ", ") + "\n")
	}
	return sb.String()
}

func quote(s string) string { return `"` + s + `"` }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func commandHash(cmd string) string {
	sum := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

func shortHash(h string) string {
	if h == "" {
		return "missing"
	}
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// contentInputs returns the sorted union of declared and discovered
// content dependencies of a task.
func contentInputs(t *Task, st *TaskState) []string {
	out := unionStrings(t.Inputs, st.Discovered)
	sort.Strings(out)
	return out
}
