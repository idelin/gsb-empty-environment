// Package testsel implements change-aware test selection for Go projects.
//
// The library keeps a snapshot of a codebase: a content hash (version) for
// every function, the static call graph, a conservative model of dynamic
// dispatch, and the set of functions each test was observed to cover.
// Given an old snapshot and a freshly computed one, Select decides which
// tests must run again:
//
//   - a modified function selects every test whose recorded coverage
//     reaches it, following the union of the old and new call graphs;
//   - a function with unknown coverage (newly added) forces a full run;
//   - deleted functions, changed dynamic-dispatch targets and stale
//     coverage entries are reconciled through the full dependency
//     closure (see Verify).
package testsel

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
)

// FuncID uniquely identifies a function, e.g. "example.com/mod/pkg.Foo" or
// "example.com/mod/pkg.(T).Bar" for methods.
type FuncID string

// TestID identifies a test, e.g. "example.com/mod/pkg.TestFoo".
type TestID string

// Edge is a static caller -> callee call edge.
type Edge struct {
	Caller FuncID `json:"caller"`
	Callee FuncID `json:"callee"`
}

// Snapshot is a versioned view of a codebase plus recorded test coverage.
type Snapshot struct {
	// Funcs maps every known function to a content hash of its body.
	Funcs map[FuncID]string `json:"funcs"`
	// Edges is the static call graph.
	Edges []Edge `json:"edges,omitempty"`
	// DynCalls maps a function to the names of methods it invokes
	// dynamically (through interfaces, function values, ...).
	DynCalls map[FuncID][]string `json:"dynCalls,omitempty"`
	// DynImpls maps a method name to every function that provides an
	// implementation of it. Combined with DynCalls it over-approximates
	// dynamic dispatch.
	DynImpls map[string][]FuncID `json:"dynImpls,omitempty"`
	// Coverage maps a test to the functions it was observed to cover.
	Coverage map[TestID][]FuncID `json:"coverage,omitempty"`
}

// NewSnapshot returns an empty, ready-to-use snapshot.
func NewSnapshot() *Snapshot {
	return &Snapshot{
		Funcs:    map[FuncID]string{},
		DynCalls: map[FuncID][]string{},
		DynImpls: map[string][]FuncID{},
		Coverage: map[TestID][]FuncID{},
	}
}

// Normalize sorts and deduplicates all sets so snapshots compare and
// serialize deterministically.
func (s *Snapshot) Normalize() {
	seen := map[Edge]bool{}
	edges := s.Edges[:0]
	for _, e := range s.Edges {
		if seen[e] {
			continue
		}
		seen[e] = true
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Caller != edges[j].Caller {
			return edges[i].Caller < edges[j].Caller
		}
		return edges[i].Callee < edges[j].Callee
	})
	s.Edges = edges
	for k, v := range s.DynCalls {
		s.DynCalls[k] = sortedStrings(v)
	}
	for k, v := range s.DynImpls {
		s.DynImpls[k] = sortedFuncs(v)
	}
	for k, v := range s.Coverage {
		s.Coverage[k] = sortedFuncs(v)
	}
}

func sortedStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedFuncs(in []FuncID) []FuncID {
	seen := map[FuncID]bool{}
	out := in[:0]
	for _, f := range in {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

const storeVersion = 1

type storeFile struct {
	Version  int       `json:"version"`
	Snapshot *Snapshot `json:"snapshot"`
}

// SaveSnapshot persists a snapshot as JSON.
func SaveSnapshot(path string, snap *Snapshot) error {
	snap.Normalize()
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Snapshot: snap}, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

// LoadSnapshot reads a snapshot previously written by SaveSnapshot.
func LoadSnapshot(path string) (*Snapshot, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("testsel: corrupt snapshot %s: %v", path, err)
	}
	if sf.Version != storeVersion {
		return nil, fmt.Errorf("testsel: unsupported snapshot version %d", sf.Version)
	}
	if sf.Snapshot == nil {
		return nil, fmt.Errorf("testsel: snapshot %s is empty", path)
	}
	return sf.Snapshot, nil
}
