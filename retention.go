// Package retention implements GFS-style (daily/weekly/monthly) backup
// retention over a dependency graph of full and incremental backups.
//
// Incremental backups depend on a parent chain that must terminate at a
// full (baseline) backup. A recovery point is restorable only if its
// entire dependency chain exists and contains no corrupted backup.
// Corrupted or broken chains make a recovery point unavailable; the
// point is reported as unavailable, never silently dropped.
package retention

import (
	"fmt"
	"sort"
	"time"
)

// BackupType classifies a backup as a full baseline or an incremental.
type BackupType int

const (
	// Full is a self-contained baseline backup.
	Full BackupType = iota
	// Incremental depends on a parent backup (full or incremental).
	Incremental
)

func (t BackupType) String() string {
	if t == Full {
		return "full"
	}
	return "incremental"
}

// Backup is a single backup object in the catalog.
type Backup struct {
	ID        string
	Time      time.Time
	Type      BackupType
	ParentID  string // empty for Full
	Corrupted bool
}

// Graph is the backup dependency graph.
type Graph struct {
	byID  map[string]*Backup
	order []*Backup // sorted by time
}

// NewGraph builds a dependency graph from a backup catalog.
func NewGraph(backups []*Backup) (*Graph, error) {
	g := &Graph{byID: make(map[string]*Backup, len(backups))}
	for _, b := range backups {
		if b.ID == "" {
			return nil, fmt.Errorf("retention: backup with empty ID")
		}
		if _, dup := g.byID[b.ID]; dup {
			return nil, fmt.Errorf("retention: duplicate backup ID %q", b.ID)
		}
		if b.Type == Full && b.ParentID != "" {
			return nil, fmt.Errorf("retention: full backup %q must not have a parent", b.ID)
		}
		if b.Type == Incremental && b.ParentID == "" {
			return nil, fmt.Errorf("retention: incremental backup %q requires a parent", b.ID)
		}
		g.byID[b.ID] = b
		g.order = append(g.order, b)
	}
	sort.Slice(g.order, func(i, j int) bool { return g.order[i].Time.Before(g.order[j].Time) })
	return g, nil
}

// Get returns a backup by ID.
func (g *Graph) Get(id string) (*Backup, bool) {
	b, ok := g.byID[id]
	return b, ok
}

// Backups returns all backups in time order.
func (g *Graph) Backups() []*Backup {
	out := make([]*Backup, len(g.order))
	copy(out, g.order)
	return out
}

// Chain returns the dependency chain for id, ordered root (full baseline)
// first and the requested backup last. It returns an error if the chain
// has a missing parent, a cycle, or contains a corrupted backup.
func (g *Graph) Chain(id string) ([]*Backup, error) {
	cur, ok := g.byID[id]
	if !ok {
		return nil, fmt.Errorf("retention: unknown backup %q", id)
	}
	var rev []*Backup
	seen := make(map[string]bool)
	for {
		if seen[cur.ID] {
			return nil, fmt.Errorf("retention: dependency cycle detected at %q", cur.ID)
		}
		seen[cur.ID] = true
		rev = append(rev, cur)
		if cur.Type == Full {
			break
		}
		parent, ok := g.byID[cur.ParentID]
		if !ok {
			return nil, fmt.Errorf("retention: backup %q depends on missing parent %q", cur.ID, cur.ParentID)
		}
		cur = parent
	}
	for _, b := range rev {
		if b.Corrupted {
			return nil, fmt.Errorf("retention: backup %q depends on corrupted backup %q", id, b.ID)
		}
	}
	chain := make([]*Backup, len(rev))
	for i, b := range rev {
		chain[len(rev)-1-i] = b
	}
	return chain, nil
}

// Restorable reports whether id is a usable recovery point.
func (g *Graph) Restorable(id string) bool {
	_, err := g.Chain(id)
	return err == nil
}
