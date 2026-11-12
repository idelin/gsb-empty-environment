package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

var (
	ErrBadPattern    = errors.New("netsec: invalid source map pattern")
	ErrNoSource      = errors.New("netsec: no source mapped for package")
	ErrNoVersion     = errors.New("netsec: no version satisfies constraint")
	ErrUnknownSource = errors.New("netsec: mapped source is not registered")
)

// VersionOps injects caller-defined version constraint matching and
// ordering.
type VersionOps struct {
	Satisfies func(version, constraint string) bool
	Less      func(a, b string) bool
}

// Requirement names a package and the caller-defined constraint on it.
type Requirement struct {
	ID         string
	Constraint string
}

// Source is a package index the resolver can query.
type Source interface {
	Name() string
	Versions(id string) []string
	Deps(id, version string) []Requirement
}

// Cache receives restored entries only after the whole graph resolves.
type Cache interface {
	Put(id, version string) error
}

// Resolver restores dependency graphs according to the source mapping table.
type Resolver struct {
	ops     VersionOps
	sources map[string]Source
	table   atomic.Pointer[SourceMap]
}

func NewResolver(ops VersionOps, sources ...Source) *Resolver {
	r := &Resolver{ops: ops, sources: make(map[string]Source, len(sources))}
	for _, s := range sources {
		r.sources[s.Name()] = s
	}
	return r
}

// ReplaceMap atomically swaps the mapping table. In-flight restores keep
// using the snapshot they started with.
func (r *Resolver) ReplaceMap(m *SourceMap) {
	r.table.Store(m)
}

// Restore resolves the full transitive dependency graph of roots. If any
// package has no mapped source or no satisfying version in its source set,
// the whole restore fails and nothing is written to cache.
func (r *Resolver) Restore(roots []Requirement, cache Cache) error {
	table := r.table.Load() // single snapshot for the entire restore
	decided := make(map[string]string)
	inProgress := make(map[string]bool)
	var order []Requirement

	var walk func(req Requirement) error
	walk = func(req Requirement) error {
		id := strings.ToLower(req.ID)
		if v, ok := decided[id]; ok {
			if !r.ops.Satisfies(v, req.Constraint) {
				return fmt.Errorf("%w: %s@%s conflicts with %q", ErrNoVersion, req.ID, v, req.Constraint)
			}
			return nil
		}
		if inProgress[id] {
			return nil // dependency cycle; already being resolved
		}
		names := table.lookup(id)
		if len(names) == 0 {
			return fmt.Errorf("%w: %s", ErrNoSource, req.ID)
		}
		best := ""
		var bestSrc Source
		found := false
		for _, name := range names {
			src, ok := r.sources[name]
			if !ok {
				return fmt.Errorf("%w: %s", ErrUnknownSource, name)
			}
			for _, v := range src.Versions(id) {
				if !r.ops.Satisfies(v, req.Constraint) {
					continue
				}
				if !found || r.ops.Less(best, v) {
					best, bestSrc, found = v, src, true
				}
			}
		}
		if !found {
			return fmt.Errorf("%w: %s %q", ErrNoVersion, req.ID, req.Constraint)
		}
		inProgress[id] = true
		for _, dep := range bestSrc.Deps(id, best) {
			if err := walk(dep); err != nil {
				return err
			}
		}
		delete(inProgress, id)
		decided[id] = best
		order = append(order, Requirement{ID: id, Constraint: best})
		return nil
	}

	for _, req := range roots {
		if err := walk(req); err != nil {
			return err
		}
	}
	if cache != nil {
		for _, d := range order {
			if err := cache.Put(d.ID, d.Constraint); err != nil {
				return err
			}
		}
	}
	return nil
}
