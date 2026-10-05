// Package rename plans and executes minimal-temp file rename schedules.
//
// All source and target paths are interpreted relative to one initial
// (base) directory. The planner handles cycles such as a->b->c->a by
// breaking each cycle with exactly one temporary path, which is the
// minimum possible. File identity is preserved: every file's content
// follows it through the whole schedule.
package rename

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Rename requests that the file at Src ends up at Dst. Both paths are
// interpreted relative to the plan's base directory.
type Rename struct {
	Src string
	Dst string
}

// Op is a single atomic rename step of an execution plan.
type Op struct {
	Src  string
	Dst  string
	Temp bool // true when Src or Dst is a temporary path
}

// Plan is an ordered list of rename operations plus the temporary paths
// it uses. Temp count is minimal: exactly one per cycle in the mapping.
type Plan struct {
	Ops   []Op
	Temps []string
}

// clean normalizes a user-supplied path and guarantees it stays relative
// to the base directory.
func clean(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("rename: empty path")
	}
	c := filepath.Clean(p)
	if filepath.IsAbs(c) {
		return "", fmt.Errorf("rename: path %q must be relative to the base directory", p)
	}
	if c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rename: path %q escapes the base directory", p)
	}
	return c, nil
}

// Build computes a minimal-temp execution plan for renames.
//
// exists reports whether a cleaned, base-relative path currently exists
// in the base directory. A target occupied by a file that does not
// participate in the renames is an error. Self-renames (Src == Dst) are
// no-ops and produce no operations.
func Build(renames []Rename, exists func(rel string) bool) (*Plan, error) {
	next := map[string]string{} // src -> dst, self-renames excluded
	self := map[string]bool{}
	for _, r := range renames {
		s, err := clean(r.Src)
		if err != nil {
			return nil, err
		}
		d, err := clean(r.Dst)
		if err != nil {
			return nil, err
		}
		if s == d {
			self[s] = true
			continue
		}
		if self[s] {
			return nil, fmt.Errorf("rename: %q is both self-renamed and moved", s)
		}
		if _, dup := next[s]; dup {
			return nil, fmt.Errorf("rename: duplicate source %q", s)
		}
		next[s] = d
	}

	// Every target must have at most one source (in-degree <= 1). This
	// also guarantees each graph component is a pure chain or pure cycle.
	seenDst := map[string]string{}
	for s, d := range next {
		if prev, dup := seenDst[d]; dup {
			return nil, fmt.Errorf("rename: %q and %q share target %q", prev, s, d)
		}
		seenDst[d] = s
	}

	// A target that no participant vacates must be free on disk.
	dsts := make([]string, 0, len(seenDst))
	for d := range seenDst {
		dsts = append(dsts, d)
	}
	sort.Strings(dsts)
	for _, d := range dsts {
		if _, isSrc := next[d]; !isSrc && exists(d) {
			return nil, fmt.Errorf("rename: target %q is occupied by a file not part of the plan", d)
		}
	}

	// Temp names must collide with nothing: no source, no target, no
	// self-renamed path, no existing file, no previously chosen temp.
	reserved := map[string]bool{}
	for s, d := range next {
		reserved[s] = true
		reserved[d] = true
	}
	for s := range self {
		reserved[s] = true
	}
	tempSeq := 0
	newTemp := func() string {
		for {
			cand := fmt.Sprintf(".rename-tmp-%d", tempSeq)
			tempSeq++
			if !reserved[cand] && !exists(cand) {
				reserved[cand] = true
				return cand
			}
		}
	}

	srcs := make([]string, 0, len(next))
	for s := range next {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)

	plan := &Plan{}
	visited := map[string]bool{}
	emitChain := func(chain []string) {
		// Reverse order: the tail's target is free, which frees the
		// next slot, and so on back to the head. No temp needed.
		for i := len(chain) - 1; i >= 0; i-- {
			plan.Ops = append(plan.Ops, Op{Src: chain[i], Dst: next[chain[i]]})
		}
	}
	for _, s := range srcs {
		if visited[s] {
			continue
		}
		var chain []string
		cur := s
		for {
			visited[cur] = true
			chain = append(chain, cur)
			d := next[cur]
			if _, isSrc := next[d]; !isSrc {
				emitChain(chain) // chain ends at a free slot
				break
			}
			if d == s {
				// Pure cycle v0->v1->...->vk->v0. One temp breaks it:
				// park v0, unwind the rest, then place v0 last.
				tmp := newTemp()
				plan.Temps = append(plan.Temps, tmp)
				plan.Ops = append(plan.Ops, Op{Src: s, Dst: tmp, Temp: true})
				for i := len(chain) - 1; i >= 1; i-- {
					plan.Ops = append(plan.Ops, Op{Src: chain[i], Dst: next[chain[i]]})
				}
				plan.Ops = append(plan.Ops, Op{Src: tmp, Dst: next[s], Temp: true})
				break
			}
			if visited[d] {
				// Chain merging into an already-planned component;
				// that component runs first and frees d in time.
				emitChain(chain)
				break
			}
			cur = d
		}
	}
	return plan, nil
}
