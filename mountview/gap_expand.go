package mountview

import (
	"fmt"
	"sort"
)

// expandGap enumerates every concrete operation sequence that the signed gap
// boundary permits, bounded by MaxOps and the candidate universe. Distinct
// concrete states are deduplicated so equivalent permutations collapse. The
// abstract engine stays sound even when enumeration truncates.
func expandGap(states []*concState, gap *GapSpec, capHistories int) ([]*concState, bool) {
	maxOps := gap.Candidates.MaxOps
	if maxOps < 0 {
		maxOps = 0
	}
	current := dedupStates(states)
	truncated := false
	for depth := 0; depth < maxOps; depth++ {
		var next []*concState
		for _, s := range current {
			ops := candidateOps(s, gap)
			for _, op := range ops {
				child := s.clone()
				if err := child.applyEntry(op); err != nil {
					continue
				}
				next = append(next, child)
				if capHistories > 0 && len(next) >= capHistories {
					truncated = true
					break
				}
			}
			if truncated {
				break
			}
		}
		next = dedupStates(append(current, next...))
		if capHistories > 0 && len(next) > capHistories {
			next = next[:capHistories]
			truncated = true
		}
		current = next
	}
	return current, truncated
}

// candidateOps materialises the finite set of concrete primitives allowed by
// the signed gap, restricted to its blast-radius candidates.
func candidateOps(s *concState, gap *GapSpec) []*LogEntry {
	var ops []*LogEntry
	seqBase := gap.AfterSeq
	paths := cleanCandidates(gap.Candidates.Paths)
	targets := cleanCandidates(gap.Candidates.Targets)

	for _, p := range paths {
		if gap.Allowed[OpBindMount] {
			for _, obj := range gap.Candidates.Objects {
				for _, iv := range gap.Candidates.Intervals {
					for _, pm := range gap.Candidates.Perms {
						for _, dom := range gap.Candidates.Domains {
							ops = append(ops, &LogEntry{
								Seq: seqBase, Op: OpBindMount, Path: p,
								Object: obj, Interval: iv, Perm: pm,
								Prop: Private, Domain: dom,
								Group: freshGroupID(s),
							})
						}
					}
				}
			}
		}
		if gap.Allowed[OpUnmount] {
			if _, ok := s.paths[p]; ok {
				ops = append(ops, &LogEntry{Seq: seqBase, Op: OpUnmount, Path: p})
			}
		}
		if gap.Allowed[OpMove] {
			for _, dst := range paths {
				if dst == p {
					continue
				}
				if _, ok := s.paths[p]; ok {
					if _, occupied := s.paths[dst]; !occupied {
						ops = append(ops, &LogEntry{Seq: seqBase, Op: OpMove, Path: p, NewPath: dst})
					}
				}
			}
		}
		if gap.Allowed[OpSymlink] {
			for _, t := range targets {
				ops = append(ops, &LogEntry{Seq: seqBase, Op: OpSymlink, Path: p, Target: t})
			}
		}
		if gap.Allowed[OpHardlink] {
			if src, ok := s.paths[p]; ok && !src.symlink {
				for _, dst := range paths {
					if dst == p {
						continue
					}
					if _, occupied := s.paths[dst]; !occupied {
						ops = append(ops, &LogEntry{Seq: seqBase, Op: OpHardlink, Path: p, NewPath: dst, Group: src.group})
					}
				}
			}
		}
		if gap.Allowed[OpSlice] {
			if cell, ok := s.paths[p]; ok && !cell.symlink {
				for _, iv := range gap.Candidates.Intervals {
					ops = append(ops, &LogEntry{Seq: seqBase, Op: OpSlice, Path: p, Interval: iv})
				}
			}
		}
		if gap.Allowed[OpChmod] {
			if cell, ok := s.paths[p]; ok && !cell.symlink {
				for _, pm := range gap.Candidates.Perms {
					ops = append(ops, &LogEntry{Seq: seqBase, Op: OpChmod, Path: p, Perm: pm})
				}
			}
		}
		if gap.Allowed[OpPropagation] {
			if cell, ok := s.paths[p]; ok && !cell.symlink {
				for _, dom := range gap.Candidates.Domains {
					for _, prop := range []Propagation{Private, Slave, Shared} {
						ops = append(ops, &LogEntry{Seq: seqBase, Op: OpPropagation, Path: p, Prop: prop, Domain: dom})
					}
				}
			}
		}
	}
	return ops
}
func freshGroupID(s *concState) GroupID {
	var max GroupID
	for id := range s.groups {
		if id > max {
			max = id
		}
	}
	return max + 1
}

func cleanCandidates(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		cp, err := CleanPath(p)
		if err != nil || seen[cp] {
			continue
		}
		seen[cp] = true
		out = append(out, cp)
	}
	sort.Strings(out)
	return out
}

func dedupStates(in []*concState) []*concState {
	seen := map[string]bool{}
	var out []*concState
	for _, s := range in {
		key := s.fingerprint()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func (s *concState) fingerprint() string {
	paths := make([]string, 0, len(s.paths))
	for p := range s.paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := ""
	for _, p := range paths {
		c := s.paths[p]
		if c.symlink {
			out += fmt.Sprintf("L:%s->%s;", p, c.target)
			continue
		}
		g := s.groups[c.group]
		if g == nil {
			out += fmt.Sprintf("G:%s#%d?;", p, c.group)
			continue
		}
		out += fmt.Sprintf("G:%s#%d=%s@%d:%d/%d;", p, c.group, g.object,
			g.inter.Offset, g.inter.Len, g.perm.Bits())
	}
	return out
}
