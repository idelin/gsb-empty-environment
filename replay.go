package hexgrid

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReplayFile is a self-contained, replayable scenario: the finite map, the
// partition layout, batch updates, and queries with expected results.
type ReplayFile struct {
	PageSize      int            `json:"pageSize"`
	PartitionSize int            `json:"partitionSize"`
	MaxRetries    int            `json:"maxRetries"`
	Walkable      [][2]int       `json:"walkable"`
	Obstacles     [][2]int       `json:"obstacles"`
	Updates       []ReplayUpdate `json:"updates"`
	Queries       []ReplayQuery  `json:"queries"`
}

// ReplayUpdate is one batch walkability update (bumps the version by one).
type ReplayUpdate struct {
	Block   [][2]int `json:"block"`
	Unblock [][2]int `json:"unblock"`
}

// ReplayQuery is one query plus its expected outcome.
type ReplayQuery struct {
	ID          string        `json:"id"`
	Version     uint64        `json:"version"` // 0 = latest
	Start       [2]int        `json:"start"`
	K           int           `json:"k"`
	Target      *[2]int       `json:"target,omitempty"`
	ExpectError string        `json:"expectError,omitempty"`
	Expect      *ReplayExpect `json:"expect,omitempty"`
}

// ReplayExpect pins the expected reachable set, target distance and path.
type ReplayExpect struct {
	Reachable  [][2]int `json:"reachable"`
	TargetDist int      `json:"targetDist"`
	Path       [][2]int `json:"path"`
}

// ReplayQueryReport is the outcome of replaying one query.
type ReplayQueryReport struct {
	ID               string   `json:"id"`
	Version          uint64   `json:"version"`
	Reachable        [][2]int `json:"reachable,omitempty"`
	TargetDist       *int     `json:"targetDist,omitempty"`
	Path             [][2]int `json:"path,omitempty"`
	Stats            Stats    `json:"stats"`
	MatchesReference bool     `json:"matchesReference"`
	MatchesExpect    bool     `json:"matchesExpect"`
	Error            string   `json:"error,omitempty"`
}

// ReplayReport is the outcome of replaying a whole file.
type ReplayReport struct {
	OK      bool                `json:"ok"`
	Queries []ReplayQueryReport `json:"queries"`
}

// LoadReplayFile reads a replay scenario from disk.
func LoadReplayFile(path string) (*ReplayFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f ReplayFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("hexgrid: parsing %s: %w", path, err)
	}
	return &f, nil
}

func toHexes(coords [][2]int) []Hex {
	out := make([]Hex, len(coords))
	for i, c := range coords {
		out[i] = Hex{c[0], c[1]}
	}
	return out
}

func toCoords(cells []Hex) [][2]int {
	out := make([][2]int, len(cells))
	for i, h := range cells {
		out[i] = [2]int{h.Q, h.R}
	}
	return out
}

func coordsEqual(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func resultsEqual(a, b *Result) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	if len(a.Reachable) != len(b.Reachable) || len(a.Dist) != len(b.Dist) || len(a.Path) != len(b.Path) {
		return false
	}
	for i := range a.Reachable {
		if a.Reachable[i] != b.Reachable[i] {
			return false
		}
	}
	for h, d := range a.Dist {
		if b.Dist[h] != d {
			return false
		}
	}
	for i := range a.Path {
		if a.Path[i] != b.Path[i] {
			return false
		}
	}
	return true
}

// RunReplay executes a scenario against both the partitioned store and the
// flat reference, and checks every query against both the reference and
// the pinned expectations.
func RunReplay(f *ReplayFile) *ReplayReport {
	layout := Layout{PageSize: f.PageSize, PartitionSize: f.PartitionSize}
	walkable := toHexes(f.Walkable)
	obstacles := toHexes(f.Obstacles)
	store := NewStore(layout, walkable, obstacles)
	if f.MaxRetries > 0 {
		store.MaxRetries = f.MaxRetries
	}
	ref := NewReference(walkable, obstacles)
	for _, u := range f.Updates {
		changes := map[Hex]bool{}
		for _, c := range u.Block {
			changes[Hex{c[0], c[1]}] = false
		}
		for _, c := range u.Unblock {
			changes[Hex{c[0], c[1]}] = true
		}
		store.Update(changes)
		ref.Update(changes)
	}
	report := &ReplayReport{OK: true}
	for _, rq := range f.Queries {
		report.Queries = append(report.Queries, runReplayQuery(store, ref, rq))
	}
	for _, q := range report.Queries {
		if !q.MatchesReference || !q.MatchesExpect {
			report.OK = false
		}
	}
	return report
}

func runReplayQuery(store *Store, ref *Reference, rq ReplayQuery) ReplayQueryReport {
	q := Query{Start: Hex{rq.Start[0], rq.Start[1]}, K: rq.K, Version: rq.Version}
	if rq.Target != nil {
		t := Hex{rq.Target[0], rq.Target[1]}
		q.Target = &t
	}
	version := q.Version
	if version == 0 {
		version = store.Version()
	}
	rep := ReplayQueryReport{
		ID:               rq.ID,
		Version:          version,
		MatchesReference: true,
		MatchesExpect:    true,
	}

	got, gotErr := store.Query(q)
	want, wantErr := ref.Query(q)
	if got != nil {
		rep.Stats = got.Stats
		rep.Reachable = toCoords(got.Reachable)
		if q.Target != nil {
			if d, ok := got.Dist[*q.Target]; ok {
				rep.TargetDist = &d
			}
			rep.Path = toCoords(got.Path)
		}
	}
	if gotErr != nil {
		rep.Error = gotErr.Error()
	}

	// The partitioned store and the flat reference must agree: both fail
	// or both succeed with identical results.
	if (gotErr == nil) != (wantErr == nil) || (gotErr == nil && !resultsEqual(got, want)) {
		rep.MatchesReference = false
	}

	switch {
	case rq.ExpectError != "":
		if gotErr == nil || !strings.Contains(gotErr.Error(), rq.ExpectError) {
			rep.MatchesExpect = false
		}
	case rq.Expect != nil:
		if gotErr != nil || !coordsEqual(rep.Reachable, rq.Expect.Reachable) {
			rep.MatchesExpect = false
		} else if q.Target != nil {
			if rep.TargetDist == nil || *rep.TargetDist != rq.Expect.TargetDist ||
				!coordsEqual(rep.Path, rq.Expect.Path) {
				rep.MatchesExpect = false
			}
		}
	}
	return rep
}
