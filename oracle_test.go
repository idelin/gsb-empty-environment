package netsec

import (
	"fmt"
	"sort"
	"testing"
)

// Independent oracle: full-universe enumeration.
//
// The oracle deliberately uses a different code path from Assign: it rebuilds
// every candidate with its own recursive matcher and then enumerates the
// complete power set of candidates, rejecting overlapping subsets. It is only
// used on small instances where 2^|candidates| stays tractable.

type oracleCandidate struct {
	templateName string
	events       []int
	mask         uint32
	span         int64
}

func oracleEnumerateCandidates(req Request) []oracleCandidate {
	var out []oracleCandidate
	for tplIdx, tpl := range req.Templates {
		if len(tpl.Stages) == 0 || len(tpl.Stages) > len(req.Events) {
			continue
		}
		var walk func(stage, from int, path []int)
		walk = func(stage, from int, path []int) {
			if stage == len(tpl.Stages) {
				matched := append([]int(nil), path...)
				var mask uint32
				for _, idx := range matched {
					mask |= 1 << uint(idx)
				}
				out = append(out, oracleCandidate{
					templateName: req.Templates[tplIdx].Name,
					events:       matched,
					mask:         mask,
					span: req.Events[matched[len(matched)-1]].Time -
						req.Events[matched[0]].Time,
				})
				return
			}
			var base int64
			if stage > 0 {
				base = req.Events[path[0]].Time
			}
			for idx := from; idx < len(req.Events); idx++ {
				if req.Events[idx].Stage != tpl.Stages[stage] {
					continue
				}
				if stage > 0 {
					gap := req.Events[idx].Time - req.Events[path[stage-1]].Time
					if gap > req.MaxGap {
						continue
					}
					if req.MaxSpan > 0 && req.Events[idx].Time-base > req.MaxSpan {
						break
					}
				}
				walk(stage+1, idx+1, append(path, idx))
			}
		}
		walk(0, 0, nil)
	}
	return out
}

type oracleSolution struct {
	picked []int
	count  int
	span   int64
	fp     []string
}

func oracleFingerprint(req Request, cands []oracleCandidate, picked []int) []string {
	sigs := make([]string, 0, len(picked))
	for _, ci := range picked {
		ids := make([]string, 0, len(cands[ci].events)+1)
		for _, evIdx := range cands[ci].events {
			ids = append(ids, req.Events[evIdx].ID)
		}
		sort.Strings(ids)
		ids = append(ids, cands[ci].templateName)
		sig := ""
		for k, id := range ids {
			if k > 0 {
				sig += "|"
			}
			sig += id
		}
		sigs = append(sigs, sig)
	}
	sort.Strings(sigs)
	return sigs
}

func oracleBetter(cand, best oracleSolution) bool {
	if cand.count != best.count {
		return cand.count > best.count
	}
	if cand.span != best.span {
		return cand.span < best.span
	}
	for i := range cand.fp {
		if i >= len(best.fp) {
			return false
		}
		if cand.fp[i] != best.fp[i] {
			return cand.fp[i] < best.fp[i]
		}
	}
	return len(cand.fp) < len(best.fp)
}

const oracleMaxCandidates = 20

func oracleSolve(req Request, cands []oracleCandidate) oracleSolution {
	best := oracleSolution{count: -1}

	var enumerate func(pos int, used uint32, picked []int, span int64)
	enumerate = func(pos int, used uint32, picked []int, span int64) {
		if pos == len(cands) {
			cur := oracleSolution{
				picked: append([]int(nil), picked...),
				count:  len(picked),
				span:   span,
			}
			cur.fp = oracleFingerprint(req, cands, cur.picked)
			if oracleBetter(cur, best) {
				best = cur
			}
			return
		}
		enumerate(pos+1, used, picked, span)
		if cands[pos].mask&used == 0 {
			enumerate(pos+1, used|cands[pos].mask,
				append(picked, pos), span+cands[pos].span)
		}
	}
	enumerate(0, 0, nil, 0)
	return best
}

func assignmentFingerprint(req Request, got *Assignment) []string {
	sigs := make([]string, 0, len(got.Chains))
	for _, chain := range got.Chains {
		ids := append([]string(nil), chain.EventIDs...)
		sort.Strings(ids)
		ids = append(ids, chain.TemplateName)
		sig := ""
		for k, id := range ids {
			if k > 0 {
				sig += "|"
			}
			sig += id
		}
		sigs = append(sigs, sig)
	}
	sort.Strings(sigs)
	return sigs
}

func findTemplate(req Request, name string) *ChainTemplate {
	for i := range req.Templates {
		if req.Templates[i].Name == name {
			return &req.Templates[i]
		}
	}
	return nil
}

func findEvent(req Request, id string) *Event {
	for i := range req.Events {
		if req.Events[i].ID == id {
			return &req.Events[i]
		}
	}
	return nil
}

// assertFeasible independently re-validates every returned chain against the
// raw request and enforces the single-consumption invariant.
func assertFeasible(t *testing.T, req Request, got *Assignment) {
	t.Helper()
	consumed := map[string]int{}
	var totalSpan int64
	for chainIdx, chain := range got.Chains {
		tpl := findTemplate(req, chain.TemplateName)
		if tpl == nil {
			t.Fatalf("chain %d uses unknown template %q", chainIdx, chain.TemplateName)
		}
		if len(chain.EventIDs) != len(tpl.Stages) {
			t.Fatalf("chain %d length %d != template stages %d",
				chainIdx, len(chain.EventIDs), len(tpl.Stages))
		}
		var prevTime, firstTime int64
		for k, id := range chain.EventIDs {
			ev := findEvent(req, id)
			if ev == nil {
				t.Fatalf("chain %d references unknown event %q", chainIdx, id)
			}
			if ev.Stage != tpl.Stages[k] {
				t.Fatalf("chain %d event %q stage %q != expected %q",
					chainIdx, id, ev.Stage, tpl.Stages[k])
			}
			if k > 0 && ev.Time-prevTime > req.MaxGap {
				t.Fatalf("chain %d violates MaxGap at event %q", chainIdx, id)
			}
			if req.MaxSpan > 0 && k > 0 && ev.Time-firstTime > req.MaxSpan {
				t.Fatalf("chain %d violates MaxSpan at event %q", chainIdx, id)
			}
			if k == 0 {
				firstTime = ev.Time
			}
			prevTime = ev.Time
			if other, dup := consumed[id]; dup {
				t.Fatalf("event %q consumed by chain %d and chain %d", id, other, chainIdx)
			}
			consumed[id] = chainIdx
		}
		last := findEvent(req, chain.EventIDs[len(chain.EventIDs)-1]).Time
		totalSpan += last - firstTime
	}
	if got.EventCount != len(consumed) {
		t.Fatalf("EventCount=%d but %d distinct events consumed",
			got.EventCount, len(consumed))
	}
	if got.TotalSpan != totalSpan {
		t.Fatalf("TotalSpan=%d but recomputed span=%d", got.TotalSpan, totalSpan)
	}
}

func assertMatchesOracle(t *testing.T, req Request) *Assignment {
	t.Helper()
	got, err := Assign(req)
	if err != nil {
		t.Fatalf("Assign returned error: %v", err)
	}
	assertFeasible(t, req, got)

	cands := oracleEnumerateCandidates(req)
	if len(cands) > oracleMaxCandidates {
		t.Fatalf("oracle universe too large for exhaustive test (%d candidates)",
			len(cands))
	}
	best := oracleSolve(req, cands)
	if !got.ProvenOptimal {
		t.Fatalf("expected proven-optimal result, got reason %q", got.Reason)
	}
	if len(got.Chains) != best.count {
		t.Fatalf("chain count %d != oracle %d", len(got.Chains), best.count)
	}
	if got.TotalSpan != best.span {
		t.Fatalf("span %d != oracle %d", got.TotalSpan, best.span)
	}
	gotFP := assignmentFingerprint(req, got)
	if fmt.Sprint(gotFP) != fmt.Sprint(best.fp) {
		t.Fatalf("fingerprint %v != oracle %v", gotFP, best.fp)
	}
	return got
}
