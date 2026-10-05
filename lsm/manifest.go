package lsm

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrStalePlan is returned by Commit when the plan's input segments are
// no longer part of the current version (a concurrent compaction won).
var ErrStalePlan = errors.New("lsm: compaction plan is stale")

// Version is an immutable snapshot of the manifest. Old versions stay
// readable forever; Commit builds a new one instead of mutating.
type Version struct {
	ID     uint64
	Levels [][]*Segment
}

// AllSegments flattens every level's segments.
func (v *Version) AllSegments() []*Segment {
	var out []*Segment
	for _, lvl := range v.Levels {
		out = append(out, lvl...)
	}
	return out
}

// Manifest tracks the current version and applies commits atomically.
type Manifest struct {
	mu         sync.Mutex
	cur        *Version
	nextSegID  int
	commitHook func() error // test hook: runs inside the commit critical section
}

func NewManifest() *Manifest {
	return &Manifest{cur: &Version{Levels: make([][]*Segment, 1)}, nextSegID: 1}
}

// Current returns the latest version. Safe for concurrent use.
func (m *Manifest) Current() *Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

// SetCommitHook installs a hook executed while the commit lock is held;
// a non-nil return aborts the commit leaving the old version intact.
func (m *Manifest) SetCommitHook(h func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commitHook = h
}

// AddSegment inserts a flushed segment into level 0 and publishes it.
func (m *Manifest) AddSegment(recs []Record) (*Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := NewSegment(m.nextSegID, 0, recs)
	if err != nil {
		return nil, err
	}
	m.nextSegID++
	nv := m.cur.clone()
	nv.Levels[0] = append(nv.Levels[0], s)
	m.cur = nv
	return s, nil
}

// RunCompaction executes a plan against a version and returns the new
// output segments. Nothing is published; readers are unaffected.
func (m *Manifest) RunCompaction(v *Version, p *Plan, snapshots []uint64, lower Coverage, maxRecs int) ([]*Segment, error) {
	inputs, err := gatherInputs(v, p)
	if err != nil {
		return nil, err
	}
	groups, err := Compact(inputs, snapshots, lower, maxRecs)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Segment
	for _, recs := range groups {
		s, err := NewSegment(m.nextSegID, p.Level+1, recs)
		if err != nil {
			return nil, err
		}
		m.nextSegID++
		out = append(out, s)
	}
	return out, nil
}

// Commit atomically replaces the plan's input segments with the outputs
// in level+1. On any failure the current version is left untouched, so
// readers holding it observe no change.
func (m *Manifest) Commit(p *Plan, outputs []*Segment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.commitHook != nil {
		if err := m.commitHook(); err != nil {
			return err
		}
	}
	cur := m.cur
	inSet := make(map[int]bool, len(p.InputIDs))
	for _, id := range p.InputIDs {
		inSet[id] = true
	}
	// Validate: every input must still be live in the current version.
	live := make(map[int]bool)
	for _, lvl := range cur.Levels {
		for _, s := range lvl {
			live[s.ID] = true
		}
	}
	for _, id := range p.InputIDs {
		if !live[id] {
			return fmt.Errorf("%w: segment %d no longer live", ErrStalePlan, id)
		}
	}
	for _, s := range outputs {
		if live[s.ID] {
			return fmt.Errorf("lsm: output segment %d already live", s.ID)
		}
	}

	nv := cur.clone()
	// Remove inputs from their levels.
	for lvl := range nv.Levels {
		kept := nv.Levels[lvl][:0]
		for _, s := range nv.Levels[lvl] {
			if !inSet[s.ID] {
				kept = append(kept, s)
			}
		}
		nv.Levels[lvl] = kept
	}
	// Ensure destination level exists.
	for len(nv.Levels) <= p.Level+1 {
		nv.Levels = append(nv.Levels, nil)
	}
	nv.Levels[p.Level+1] = append(nv.Levels[p.Level+1], outputs...)
	sort.Slice(nv.Levels[p.Level+1], func(i, j int) bool {
		return nv.Levels[p.Level+1][i].MinKey() < nv.Levels[p.Level+1][j].MinKey()
	})
	m.cur = nv
	return nil
}

// clone copies the version's slice structure; segments stay shared and
// immutable.
func (v *Version) clone() *Version {
	nv := &Version{ID: v.ID + 1, Levels: make([][]*Segment, len(v.Levels))}
	for i, lvl := range v.Levels {
		nv.Levels[i] = append([]*Segment(nil), lvl...)
	}
	return nv
}
