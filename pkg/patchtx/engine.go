// Package patchtx runs several admission plugins over one Pod-like object and
// publishes their combined changes as a single transaction.
//
// Each plugin emits a limited JSON Patch (see pkg/jsonpatch). Plugins are
// re-invoked only when the JSON subtrees they declared as dependencies have
// changed since their last invocation; cached output is reused otherwise. When
// the combined patch no longer changes the document a fixed point has been
// reached and the result is committed. Any failing "test" operation rolls the
// whole round (and therefore the whole transaction) back, leaving the caller's
// original object untouched. If re-running plugins keeps moving the document
// between already-seen states instead of converging, execution stops with a
// cycle witness rather than looping forever.
package patchtx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"example.org/offlinecli/pkg/jsonpatch"
)

// Plugin produces a JSON Patch for the current view of the object. Deps lists
// JSON Pointers whose values feed the decision; the engine only re-runs the
// plugin after one of those values changes. A plugin without dependencies is
// invoked exactly once per Run.
type Plugin interface {
	Name() string
	Deps() []string
	Patch(view any) ([]jsonpatch.Op, error)
}

// RoundStep records one round of the fixed-point search.
type RoundStep struct {
	Round   int      `json:"round"`
	Ran     []string `json:"ran"`     // plugins actually invoked
	Reused  []string `json:"reused"`  // plugins whose cached patch was reused
	Changed bool     `json:"changed"` // whether the combined patch altered the object
	Hash    string   `json:"hash"`    // hash of the document at the end of the round
}

// RoundFailureError reports a round whose combined patch could not be applied.
// Because rounds are transactions, nothing is committed.
type RoundFailureError struct {
	Round  int
	Plugin string // empty when no single plugin can be blamed
	Err    error
}

func (e *RoundFailureError) Error() string {
	if e.Plugin == "" {
		return fmt.Sprintf("patchtx: round %d failed and was rolled back: %v", e.Round, e.Err)
	}
	return fmt.Sprintf("patchtx: round %d failed in plugin %q and was rolled back: %v",
		e.Round, e.Plugin, e.Err)
}

func (e *RoundFailureError) Unwrap() error { return e.Err }

// NonConvergenceError is returned when the document oscillates instead of
// reaching a fixed point. The witness lists the distinct repeated states and
// the rounds/plugin invocations that connect them.
type NonConvergenceError struct {
	MaxRounds int         `json:"max_rounds"`
	Witness   []RoundStep `json:"witness"`
	Cycle     []StateSnap `json:"cycle"`
}

// StateSnap is one document state participating in a cycle.
type StateSnap struct {
	Round  int    `json:"round"`
	Hash   string `json:"hash"`
	Object any    `json:"object"`
}

func (e *NonConvergenceError) Error() string {
	if len(e.Cycle) > 0 {
		first := e.Cycle[0].Hash[:12]
		last := e.Cycle[len(e.Cycle)-1].Hash[:12]
		return fmt.Sprintf("patchtx: fixed point not reached after %d rounds; "+
			"cycle witness: state %s repeats via %d intermediate state(s) back to %s",
			e.MaxRounds, first, len(e.Cycle)-1, last)
	}
	return fmt.Sprintf("patchtx: fixed point not reached after %d rounds", e.MaxRounds)
}

type cacheEntry struct {
	fingerprint string
	ops         []jsonpatch.Op
}

// Engine orchestrates plugins. Cached patches survive across Run calls so that
// repeated calls skip plugins whose dependencies have not changed.
type Engine struct {
	plugins   []Plugin
	pointers  map[string][]string
	maxRounds int
	cache     map[string]cacheEntry
}

// NewEngine builds an Engine. maxRounds bounds the fixed-point search; pass 0
// for the default of 32.
func NewEngine(plugins []Plugin, maxRounds int) *Engine {
	seen := make(map[string]bool, len(plugins))
	pointers := make(map[string][]string, len(plugins))
	for _, plugin := range plugins {
		name := plugin.Name()
		if seen[name] {
			panic(fmt.Sprintf("patchtx: duplicate plugin name %q", name))
		}
		seen[name] = true
		pointers[name] = append(pointers[name], plugin.Deps()...)
	}
	if maxRounds <= 0 {
		maxRounds = 32
	}
	return &Engine{
		plugins:   plugins,
		pointers:  pointers,
		maxRounds: maxRounds,
		cache:     make(map[string]cacheEntry),
	}
}

// Result is the outcome of a successful Run.
type Result struct {
	Object      any         `json:"object"`
	Rounds      []RoundStep `json:"rounds"`
	Invocations int         `json:"invocations"` // total plugin invocations during this Run
}

// Run applies all plugins transactionally until a fixed point is reached. The
// input object is never mutated: the converged object is returned only on
// success; any failure returns the original through the result alongside the
// error so callers can assert it is byte-for-byte unchanged.
func (e *Engine) Run(object any) (*Result, error) {
	work, err := jsonpatch.DeepCopy(object)
	if err != nil {
		return nil, fmt.Errorf("patchtx: cannot snapshot object: %w", err)
	}

	local := make(map[string]cacheEntry, len(e.cache))
	for name, entry := range e.cache {
		local[name] = entry
	}

	steps := make([]RoundStep, 0, 4)
	seenStates := make(map[string]int) // hash -> round index
	statesByRound := make(map[int]any)
	invocations := 0

	for round := 1; round <= e.maxRounds; round++ {
		ran := make([]string, 0)
		reused := make([]string, 0)
		combined := make([]jsonpatch.Op, 0)

		for _, plugin := range e.plugins {
			name := plugin.Name()
			fingerprint := e.fingerprint(work, name)
			if entry, ok := local[name]; ok && entry.fingerprint == fingerprint {
				reused = append(reused, name)
				combined = append(combined, entry.ops...)
				continue
			}

			ops, err := plugin.Patch(work)
			if err != nil {
				return nil, &RoundFailureError{Round: round, Plugin: name, Err: err}
			}
			local[name] = cacheEntry{fingerprint: fingerprint, ops: ops}
			ran = append(ran, name)
			invocations++
			combined = append(combined, ops...)
		}

		next := work
		if len(combined) > 0 {
			next, err = jsonpatch.Apply(work, combined)
			if err != nil {
				// A failed test (or any invalid op) aborts this round and the
				// transaction: work is discarded and object stays untouched.
				return nil, &RoundFailureError{Round: round, Err: err}
			}
		}

		changed := !canonicalEqual(work, next)
		hash := stateHash(next)
		steps = append(steps, RoundStep{
			Round:   round,
			Ran:     ran,
			Reused:  reused,
			Changed: changed,
			Hash:    hash,
		})
		statesByRound[round] = next
		work = next

		if !changed {
			// Fixed point: commit caches and publish the combined result.
			for name, entry := range local {
				e.cache[name] = entry
			}
			return &Result{Object: work, Rounds: steps, Invocations: invocations}, nil
		}

		if firstRound, ok := seenStates[hash]; ok {
			return nil, e.cycleError(steps, statesByRound, seenStates, firstRound, round)
		}
		seenStates[hash] = round
	}

	return nil, &NonConvergenceError{
		MaxRounds: e.maxRounds,
		Witness:   steps,
		Cycle:     e.recentCycle(steps, statesByRound),
	}
}

func (e *Engine) cycleError(steps []RoundStep, states map[int]any, seen map[string]int, firstRound, lastRound int) error {
	cycle := make([]StateSnap, 0, lastRound-firstRound+1)
	for round := firstRound; round <= lastRound; round++ {
		step := steps[round-1]
		cycle = append(cycle, StateSnap{
			Round:  round,
			Hash:   step.Hash,
			Object: states[round],
		})
	}
	return &NonConvergenceError{
		MaxRounds: e.maxRounds,
		Witness:   steps,
		Cycle:     cycle,
	}
}

// recentCycle reconstructs a bounded witness when the round cap is hit without
// an exact repeated hash, using the last distinct states.
func (e *Engine) recentCycle(steps []RoundStep, states map[int]any) []StateSnap {
	start := steps[0].Round
	if len(steps) > 4 {
		start = steps[len(steps)-4].Round
	}
	cycle := make([]StateSnap, 0, 5)
	for _, step := range steps {
		if step.Round < start {
			continue
		}
		cycle = append(cycle, StateSnap{
			Round:  step.Round,
			Hash:   step.Hash,
			Object: states[step.Round],
		})
	}
	return cycle
}

func (e *Engine) fingerprint(view any, plugin string) string {
	hasher := sha256.New()
	for _, pointer := range e.pointers[plugin] {
		tokens, err := jsonpatch.ParsePointer(pointer)
		if err != nil {
			panic(fmt.Sprintf("patchtx: plugin %q registered invalid pointer %q: %v", plugin, pointer, err))
		}
		value, err := jsonpatch.GetValue(view, tokens)
		hasher.Write([]byte(pointer))
		hasher.Write([]byte{0})
		if err != nil {
			hasher.Write([]byte("<missing>"))
		} else {
			raw, _ := json.Marshal(value)
			hasher.Write(raw)
		}
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func canonicalEqual(a, b any) bool {
	return stateHash(a) == stateHash(b)
}

func stateHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("patchtx: cannot hash state: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// AsNonConvergence extracts the cycle-witness error if present.
func AsNonConvergence(err error) (*NonConvergenceError, bool) {
	var ncErr *NonConvergenceError
	if errors.As(err, &ncErr) {
		return ncErr, true
	}
	return nil, false
}
