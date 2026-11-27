package spp

import (
	"fmt"
	"sort"
)

// RepairAction is one atomic policy/export adjustment. Implementations are
// PreferenceEdit (change a local-pref ranking) and EdgeDisable (shut a
// non-core export edge).
type RepairAction interface {
	Key() string
	Describe() string
}

// PreferenceEdit demotes a specific permitted path of an AS to the bottom of
// its strict preference order, breaking the local-pref ranking that feeds a
// dispute rim while keeping every link and route reachable.
type PreferenceEdit struct {
	AS   int
	Path []int
	// OldRank / NewRank document the ranking change in the repaired instance.
	OldRank int
	NewRank int
}

func (e PreferenceEdit) Key() string {
	return fmt.Sprintf("pref:%d:%s", e.AS, PathKey(e.Path))
}

func (e PreferenceEdit) Describe() string {
	return fmt.Sprintf("demote path %v of AS %d from local-pref rank %d to %d",
		e.Path, e.AS, e.OldRank, e.NewRank)
}

// EdgeDisable disables one undirected non-core export/import link. A link is
// core only when removing it disconnects some AS from the prefix; non-core
// links are backup/peer links whose shutdown preserves reachability.
type EdgeDisable struct {
	A, B int
	Link Link
}

func (d EdgeDisable) Key() string {
	a, b := d.A, d.B
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("edge:%d-%d", a, b)
}

func (d EdgeDisable) Describe() string {
	return fmt.Sprintf("disable non-core export edge %d--%d (%s)", d.A, d.B, d.Link.Relation)
}

// RepairPlan is one minimum-cardinality set of actions with verification.
type RepairPlan struct {
	Actions          []RepairAction
	RepairedInstance *Instance
	Verify           *SolveResult
	RemainingWheels  []DisputeWheel
	ReachabilityKept bool
}

// RepairOptions bounds the minimum-repair search.
type RepairOptions struct {
	MaxActions        int // 0 -> default 4
	MaxCombinations   int // 0 -> default 200000
	SolutionsPerCheck int // 0 -> default 4
	AllowEdgeDisable  bool
}

// MinimumRepair enumerates action combinations in increasing cardinality and
// deterministic order, returning every minimum plan that (1) preserves
// prefix reachability for all originally reachable ASes, (2) removes every
// dispute wheel, and (3) leaves exactly one Stable Path Assignment.
func (in *Instance) MinimumRepair(opts RepairOptions) ([]RepairPlan, error) {
	if opts.MaxActions <= 0 {
		opts.MaxActions = 4
	}
	if opts.MaxCombinations <= 0 {
		opts.MaxCombinations = 200_000
	}
	if opts.SolutionsPerCheck <= 0 {
		opts.SolutionsPerCheck = 4
	}
	actions := in.candidateActions(opts.AllowEdgeDisable)
	originallyReachable := in.reachableSet()

	var plans []RepairPlan
	combos := 0
	foundSize := -1
	for size := 1; size <= opts.MaxActions && foundSize == -1; size++ {
		var rec func(start int, chosen []RepairAction) bool
		rec = func(start int, chosen []RepairAction) bool {
			if len(chosen) == size {
				combos++
				if combos > opts.MaxCombinations {
					return true
				}
				if plan, ok := in.evaluateRepair(chosen, originallyReachable, opts); ok {
					plans = append(plans, plan)
				}
				return false
			}
			for i := start; i < len(actions); i++ {
				chosen = append(chosen, actions[i])
				stop := rec(i+1, chosen)
				chosen = chosen[:len(chosen)-1]
				if stop {
					return true
				}
			}
			return false
		}
		rec(0, nil)
		if len(plans) > 0 {
			foundSize = size
		}
	}
	if foundSize == -1 {
		return nil, fmt.Errorf("spp: no repair within %d actions (combinations tried: %d)",
			opts.MaxActions, combos)
	}
	return plans, nil
}

func (in *Instance) candidateActions(allowEdge bool) []RepairAction {
	var actions []RepairAction
	for _, u := range in.SortedNodes() {
		if u == in.Config.Origin {
			continue
		}
		for _, pr := range in.Ranked[u] {
			if len(pr.Path) < 2 {
				continue
			}
			actions = append(actions, PreferenceEdit{AS: u, Path: CopyPath(pr.Path), OldRank: pr.Rank})
		}
	}
	if allowEdge {
		for _, link := range in.sortedLinks() {
			if in.isCoreEdge(link) {
				continue
			}
			actions = append(actions, EdgeDisable{A: link.A, B: link.B, Link: link})
		}
	}
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Key() < actions[j].Key() })
	return dedupeActions(actions)
}

func dedupeActions(as []RepairAction) []RepairAction {
	seen := map[string]bool{}
	var out []RepairAction
	for _, a := range as {
		if seen[a.Key()] {
			continue
		}
		seen[a.Key()] = true
		out = append(out, a)
	}
	return out
}

func (in *Instance) sortedLinks() []Link {
	var out []Link
	for _, l := range in.Graph.links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// isCoreEdge marks links whose removal disconnects at least one AS that is
// currently able to reach the prefix through a permitted path.
func (in *Instance) isCoreEdge(link Link) bool {
	reachable := in.reachableSet()
	for _, u := range in.SortedNodes() {
		if u == in.Config.Origin || !reachable[u] {
			continue
		}
		hasAlternative := false
		for _, pr := range in.Ranked[u] {
			if in.Config.StrictGR && !pr.ValleyFree {
				continue // illegal valley routes are not valid backups
			}
			if !pathUsesEdge(pr.Path, link.A, link.B) {
				hasAlternative = true
				break
			}
		}
		if !hasAlternative {
			return true
		}
	}
	return false
}

func pathUsesEdge(p []int, a, b int) bool {
	for i := 0; i+1 < len(p); i++ {
		if (p[i] == a && p[i+1] == b) || (p[i] == b && p[i+1] == a) {
			return true
		}
	}
	return false
}

func (in *Instance) reachableSet() map[int]bool {
	out := map[int]bool{in.Config.Origin: true}
	for _, u := range in.SortedNodes() {
		if len(in.Ranked[u]) > 0 {
			out[u] = true
		}
	}
	return out
}

func (in *Instance) evaluateRepair(actions []RepairAction, reachable map[int]bool, opts RepairOptions) (RepairPlan, bool) {
	repaired, err := in.applyActions(actions)
	if err != nil {
		return RepairPlan{}, false
	}
	// Reachability must not regress for any originally reachable AS.
	for u := range reachable {
		if len(repaired.Ranked[u]) == 0 {
			return RepairPlan{}, false
		}
	}
	wheels := repaired.DetectWheels(8)
	if len(wheels) > 0 {
		return RepairPlan{}, false
	}
	res, err := repaired.Solve(SolveOptions{MaxSolutions: opts.SolutionsPerCheck, MaxBacktracks: 500_000})
	if err != nil || res.Outcome != OutcomeUnique {
		return RepairPlan{}, false
	}
	explained := in.explainActions(actions, repaired)
	return RepairPlan{
		Actions:          explained,
		RepairedInstance: repaired,
		Verify:           res,
		RemainingWheels:  wheels,
		ReachabilityKept: true,
	}, true
}

// explainActions copies each preference edit and fills in the rank it
// occupies after demotion, so the returned plan documents the concrete
// local-pref change rather than a zero placeholder.
func (in *Instance) explainActions(actions []RepairAction, repaired *Instance) []RepairAction {
	out := make([]RepairAction, len(actions))
	for i, a := range actions {
		if pe, ok := a.(PreferenceEdit); ok {
			if newRank, found := repaired.RankOf(pe.AS, pe.Path); found {
				pe.NewRank = newRank
			}
			out[i] = pe
			continue
		}
		out[i] = a
	}
	return out
}

// applyActions materializes a repaired instance by filtering ranked paths and
// re-ranking. Preference edits drop the target path to the bottom; edge
// disables drop every path traversing the link.
func (in *Instance) applyActions(actions []RepairAction) (*Instance, error) {
	dropPath := map[int]map[string]bool{}
	demotePath := map[int]map[string]int{}
	disabled := map[[2]int]bool{}
	for _, a := range actions {
		switch t := a.(type) {
		case PreferenceEdit:
			if demotePath[t.AS] == nil {
				demotePath[t.AS] = map[string]int{}
			}
			demotePath[t.AS][PathKey(t.Path)] = 0
		case EdgeDisable:
			disabled[linkKey(t.A, t.B)] = true
		default:
			return nil, fmt.Errorf("spp: unknown repair action %T", a)
		}
	}
	out := &Instance{
		Graph:  in.Graph,
		Config: in.Config,
		Ranked: map[int][]PathRank{},
		Report: in.Report,
		index:  map[int]map[string]int{},
	}
	for _, u := range in.SortedNodes() {
		var kept []PathRank
		var demoted []PathRank
		for _, pr := range in.Ranked[u] {
			blockedByEdge := false
			for key := range disabled {
				if pathUsesEdge(pr.Path, key[0], key[1]) {
					blockedByEdge = true
					break
				}
			}
			if blockedByEdge {
				continue
			}
			if dropPath[u] != nil && dropPath[u][PathKey(pr.Path)] {
				continue
			}
			cp := PathRank{Path: CopyPath(pr.Path), Tier: pr.Tier, ValleyFree: pr.ValleyFree}
			if demotePath[u] != nil && demotePath[u][PathKey(pr.Path)] == 0 {
				demoted = append(demoted, cp)
			} else {
				kept = append(kept, cp)
			}
		}
		// Demoted paths go to the very bottom, preserving relative order.
		kept = append(kept, demoted...)
		for i := range kept {
			kept[i].Rank = i
		}
		out.Ranked[u] = kept
		out.index[u] = map[string]int{}
		for _, pr := range kept {
			out.index[u][PathKey(pr.Path)] = pr.Rank
		}
	}
	return out, nil
}
