package capgate

import (
	"fmt"
	"sort"
	"strings"
)

type preparedEdge struct {
	edge  *Edge
	id    string
	expr  Expr
	index int
}

type presence struct {
	UID    string `json:"uid"`
	Branch string `json:"branch"`
}

type presenceKey struct {
	UID    string
	Branch string
}

type grantRecord struct {
	UID        string
	Grant      Grant
	OriginEdge string
	Index      int
}

type instancePresence struct {
	presence
	EdgeID string
}

type cacheTaint struct {
	CacheKey  string
	WriterID  string
	ReaderID  string
	Writer    *preparedEdge
	Reader    *preparedEdge
	Reason    string
	Untrusted bool
}

type unionEvidence struct {
	Rule     PrivilegeUnion
	Sources  []capSource
	Branches []string
	Optional bool
}

type capSource struct {
	Capability string
	UID        string
	Branch     string
	TxID       string
	EdgeID     string
	Composed   bool
	Members    []instancePresence
}

type highEvidence struct {
	Key               InstanceKey
	High              bool
	Kinds             map[string]bool
	Reasons           []string
	EdgeIDs           []string
	ReachableBranches map[string]bool
	Presences         []instancePresence
	CacheTaints       []cacheTaint
	Unions            []unionEvidence
	ComposedSources   []capSource
	Sources           []capSource
	BranchCaps        map[string]map[string]bool
	BranchHigh        map[string]bool
	InvalidSignature  []string
}

type assignmentAnalysis struct {
	Index int
	Assignment
	active     []*preparedEdge
	instances  map[InstanceKey]*highEvidence
	iterations int
	reach      map[string]map[string]bool
}

type graphAnalysis struct {
	graph    *Graph
	u        *universe
	policy   Policy
	prepared map[string]*preparedEdge
	edges    []*preparedEdge
	grants   map[string]grantRecord
}

func analyzeGraph(graph *Graph, u *universe, policy Policy) (*graphAnalysis, error) {
	if graph == nil {
		graph = &Graph{}
	}
	ga := &graphAnalysis{
		graph:    graph,
		u:        u,
		policy:   policy,
		prepared: map[string]*preparedEdge{},
		grants:   map[string]grantRecord{},
	}

	seenIDs := map[string]int{}
	for i, edge := range graph.Edges {
		if edge == nil {
			return nil, fmt.Errorf("capgate: graph %q edge at index %d is nil", graph.Name, i)
		}
		if edge.Pkg == "" {
			return nil, fmt.Errorf("capgate: edge %q missing pkg", edge.ID)
		}
		if edge.Digest == "" {
			return nil, fmt.Errorf("capgate: edge %q missing digest", edge.ID)
		}
		if !edge.Phase.Valid() {
			return nil, fmt.Errorf("capgate: edge %q has unsupported phase %q", edge.ID, edge.Phase)
		}
		if edge.CacheMode != CacheModeNone && edge.CacheMode != CacheModeRead &&
			edge.CacheMode != CacheModeWrite && edge.CacheMode != CacheModeReadWrite {
			return nil, fmt.Errorf("capgate: edge %q has unsupported cacheMode %q", edge.ID, edge.CacheMode)
		}
		id := edge.ID
		if id == "" {
			id = fmt.Sprintf("%s/edge-%d", graph.Name, i)
			if graph.Name == "" {
				id = fmt.Sprintf("edge-%d", i)
			}
		}
		if prior, ok := seenIDs[id]; ok && edge.ID != "" {
			return nil, fmt.Errorf("capgate: duplicate edge id %q at indexes %d and %d", id, prior, i)
		}
		seenIDs[id] = i
		expr, err := compileCondition(edge.EffectiveCondition())
		if err != nil {
			return nil, fmt.Errorf("capgate: edge %q: %w", id, err)
		}
		pe := &preparedEdge{edge: edge, id: id, expr: expr, index: i}
		ga.edges = append(ga.edges, pe)
		ga.prepared[id] = pe

		for j, grant := range edge.Grants {
			if grant.Capability == "" {
				return nil, fmt.Errorf("capgate: edge %q grant %d missing capability", id, j)
			}
			if grant.Half != nil {
				h := grant.Half
				if h.Family == "" || h.Index <= 0 || h.Total <= 0 || h.Index > h.Total {
					return nil, fmt.Errorf("capgate: edge %q grant %d has invalid half reference", id, j)
				}
			}
			uid := fmt.Sprintf("%s/grant/%d", id, j)
			ga.grants[uid] = grantRecord{UID: uid, Grant: grant, OriginEdge: id, Index: j}
		}
	}
	return ga, nil
}

func (ga *graphAnalysis) run(index int) (*assignmentAnalysis, error) {
	a := ga.u.assignmentForIndex(index)
	ar := &assignmentAnalysis{
		Index:      index,
		Assignment: a,
		instances:  map[InstanceKey]*highEvidence{},
	}
	for _, pe := range ga.edges {
		if pe.expr.Eval(a) {
			ar.active = append(ar.active, pe)
		}
	}
	ar.reach = activeReachability(ar.active)

	nodeFacts := map[string]map[presenceKey]presence{}
	edgeFacts := map[string]map[presenceKey]presence{}
	nodeBranches := map[string]map[string]bool{}
	edgeBranches := map[string]map[string]bool{}
	getSet := func(m map[string]map[presenceKey]presence, k string) map[presenceKey]presence {
		if m[k] == nil {
			m[k] = map[presenceKey]presence{}
		}
		return m[k]
	}
	add := func(m map[presenceKey]presence, p presence) bool {
		k := presenceKey{UID: p.UID, Branch: p.Branch}
		if _, ok := m[k]; ok {
			return false
		}
		m[k] = p
		return true
	}
	getBranchSet := func(m map[string]map[string]bool, k string) map[string]bool {
		if m[k] == nil {
			m[k] = map[string]bool{}
		}
		return m[k]
	}
	addBranch := func(m map[string]bool, branch string) bool {
		if branch == "" || m[branch] {
			return false
		}
		m[branch] = true
		return true
	}

	explicit := map[string][]presence{}
	for _, pe := range ar.active {
		for j := range pe.edge.Grants {
			uid := fmt.Sprintf("%s/grant/%d", pe.id, j)
			branch := ""
			if pe.edge.Optional {
				branch = pe.id
			}
			p := presence{UID: uid, Branch: branch}
			explicit[pe.id] = append(explicit[pe.id], p)
		}
	}

	for {
		changed := false
		ar.iterations++
		for _, pe := range ar.active {
			out := getSet(edgeFacts, pe.id)
			toNode := getSet(nodeFacts, pe.edge.To)
			outBranches := getBranchSet(edgeBranches, pe.id)
			toBranches := getBranchSet(nodeBranches, pe.edge.To)

			if pe.edge.Optional {
				if addBranch(outBranches, pe.id) {
					changed = true
				}
				if pe.edge.To != "" && addBranch(toBranches, pe.id) {
					changed = true
				}
			}

			for _, p := range explicit[pe.id] {
				if add(out, p) {
					changed = true
				}
				if pe.edge.To != "" {
					if add(toNode, p) {
						changed = true
					}
				}
			}

			sandboxBarrier := pe.edge.Phase.IsInstallScript() && pe.edge.Sandboxed
			if pe.edge.From != "" && !sandboxBarrier {
				for branch := range nodeBranches[pe.edge.From] {
					if addBranch(outBranches, branch) {
						changed = true
					}
					if pe.edge.To != "" && addBranch(toBranches, branch) {
						changed = true
					}
				}
			}

			if pe.edge.From == "" || sandboxBarrier {
				continue
			}
			for _, p := range nodeFacts[pe.edge.From] {
				if pe.edge.Phase.IsInstallScript() && pe.edge.Sandboxed {
					continue
				}
				next := p
				if next.Branch == "" && pe.edge.Optional {
					next.Branch = pe.id
				}
				if add(out, next) {
					changed = true
				}
				if pe.edge.To != "" {
					if add(toNode, next) {
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	for _, pe := range ar.active {
		key := InstanceKey{Pkg: pe.edge.Pkg, Digest: pe.edge.Digest, Phase: pe.edge.Phase}
		ie := ga.evidence(ar, key)
		ie.EdgeIDs = append(ie.EdgeIDs, pe.id)
		for branch := range edgeBranches[pe.id] {
			ie.ReachableBranches[branch] = true
		}
		for _, p := range edgeFacts[pe.id] {
			ie.Presences = append(ie.Presences, instancePresence{presence: p, EdgeID: pe.id})
		}
	}

	ga.addCacheTaints(ar)
	for key, ie := range ar.instances {
		ga.evaluate(ar, ie)
		ar.instances[key] = ie
	}
	return ar, nil
}

func activeReachability(active []*preparedEdge) map[string]map[string]bool {
	byFrom := map[string][]*preparedEdge{}
	for _, pe := range active {
		if pe.edge.From != "" {
			byFrom[pe.edge.From] = append(byFrom[pe.edge.From], pe)
		}
	}
	out := map[string]map[string]bool{}
	for _, start := range active {
		seen := map[string]bool{start.id: true}
		queue := []string{start.id}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			var current *preparedEdge
			for _, pe := range active {
				if pe.id == id {
					current = pe
					break
				}
			}
			if current == nil || current.edge.To == "" {
				continue
			}
			for _, next := range byFrom[current.edge.To] {
				if seen[next.id] {
					continue
				}
				seen[next.id] = true
				queue = append(queue, next.id)
			}
		}
		delete(seen, start.id)
		out[start.id] = seen
	}
	return out
}

func hasIndependentBranchPair(ar *assignmentAnalysis, branches []string) bool {
	for i := 0; i < len(branches); i++ {
		for j := i + 1; j < len(branches); j++ {
			a, b := branches[i], branches[j]
			if strings.HasPrefix(a, "$") || strings.HasPrefix(b, "$") {
				continue
			}
			if ar.reach[a] != nil && ar.reach[a][b] {
				continue
			}
			if ar.reach[b] != nil && ar.reach[b][a] {
				continue
			}
			return true
		}
	}
	return false
}

func (ga *graphAnalysis) evidence(ar *assignmentAnalysis, key InstanceKey) *highEvidence {
	if ar.instances[key] == nil {
		ar.instances[key] = &highEvidence{
			Key:               key,
			Kinds:             map[string]bool{},
			ReachableBranches: map[string]bool{},
		}
	}
	return ar.instances[key]
}

func (ga *graphAnalysis) addCacheTaints(ar *assignmentAnalysis) {
	byKey := map[string][]*preparedEdge{}
	for _, pe := range ar.active {
		if pe.edge.CacheKey != "" {
			byKey[pe.edge.CacheKey] = append(byKey[pe.edge.CacheKey], pe)
		}
	}
	for _, pe := range ar.active {
		readerMode := effectiveCacheMode(pe.edge)
		if pe.edge.Phase != PhasePublish || pe.edge.CacheKey == "" || !readerMode.reads() {
			continue
		}
		for _, w := range byKey[pe.edge.CacheKey] {
			writerMode := effectiveCacheMode(w.edge)
			if w.id == pe.id || !writerMode.writes() || !cacheBefore(w, pe) {
				continue
			}
			untrusted := w.edge.Optional ||
				(w.edge.Phase.IsInstallScript() && !w.edge.Sandboxed) ||
				ga.edgeHasTemporary(ar, w.id)
			reason := "earlier cross-edge write to shared cache key reaches publish reader"
			if ga.policy.strictCache() && !w.edge.TrustedCacheWrite {
				ar.instances[InstanceKey{Pkg: pe.edge.Pkg, Digest: pe.edge.Digest, Phase: pe.edge.Phase}] =
					ga.evidence(ar, InstanceKey{Pkg: pe.edge.Pkg, Digest: pe.edge.Digest, Phase: pe.edge.Phase})
				ie := ar.instances[InstanceKey{Pkg: pe.edge.Pkg, Digest: pe.edge.Digest, Phase: pe.edge.Phase}]
				ie.CacheTaints = append(ie.CacheTaints, cacheTaint{
					CacheKey: pe.edge.CacheKey, WriterID: w.id, ReaderID: pe.id,
					Writer: w, Reader: pe, Reason: reason, Untrusted: untrusted,
				})
			} else if !ga.policy.strictCache() && untrusted && !w.edge.TrustedCacheWrite {
				key := InstanceKey{Pkg: pe.edge.Pkg, Digest: pe.edge.Digest, Phase: pe.edge.Phase}
				ie := ga.evidence(ar, key)
				ie.CacheTaints = append(ie.CacheTaints, cacheTaint{
					CacheKey: pe.edge.CacheKey, WriterID: w.id, ReaderID: pe.id,
					Writer: w, Reader: pe, Reason: reason + " from untrusted writer", Untrusted: true,
				})
			}
		}
	}
}

func effectiveCacheMode(e *Edge) CacheMode {
	if e == nil {
		return CacheModeNone
	}
	if e.CacheMode != CacheModeNone {
		return e.CacheMode
	}
	if e.CacheKey != "" {
		return CacheModeReadWrite
	}
	return CacheModeNone
}

func cacheBefore(w, r *preparedEdge) bool {
	wr, rr := w.edge.Phase.Rank(), r.edge.Phase.Rank()
	if wr != rr {
		return wr < rr
	}
	return w.edge.Sequence > 0 && r.edge.Sequence > 0 && w.edge.Sequence < r.edge.Sequence
}

func (ga *graphAnalysis) edgeHasTemporary(ar *assignmentAnalysis, edgeID string) bool {
	// edgeFacts are local to run(); reconstruct through instances is awkward.
	// This helper is currently called before evaluation but after edge facts
	// were copied into instance presences. Search all instance presences.
	for _, ie := range ar.instances {
		for _, ip := range ie.Presences {
			if ip.EdgeID != edgeID {
				continue
			}
			if rec, ok := ga.grants[ip.UID]; ok && rec.Grant.Temporary {
				return true
			}
		}
	}
	return false
}

func (ga *graphAnalysis) evaluate(ar *assignmentAnalysis, ie *highEvidence) {
	sort.SliceStable(ie.Presences, func(i, j int) bool {
		if ie.Presences[i].EdgeID != ie.Presences[j].EdgeID {
			return ie.Presences[i].EdgeID < ie.Presences[j].EdgeID
		}
		return ie.Presences[i].UID < ie.Presences[j].UID
	})

	ie.BranchCaps = map[string]map[string]bool{}
	capSet := map[string]bool{}
	addSource := func(s capSource) {
		ie.Sources = append(ie.Sources, s)
		capSet[s.Capability] = true
		branch := s.Branch
		if branch == "" {
			branch = "$trusted"
		}
		if ie.BranchCaps[branch] == nil {
			ie.BranchCaps[branch] = map[string]bool{}
		}
		ie.BranchCaps[branch][s.Capability] = true
	}

	for _, ip := range ie.Presences {
		rec, ok := ga.grants[ip.UID]
		if !ok {
			continue
		}
		g := rec.Grant
		s := capSource{
			Capability: g.Capability,
			UID:        ip.UID,
			Branch:     ip.Branch,
			TxID:       g.TransactionID,
			EdgeID:     ip.EdgeID,
		}
		addSource(s)
	}

	composed := ga.composeHalves(ie.Presences)
	for _, s := range composed {
		ie.ComposedSources = append(ie.ComposedSources, s)
		addSource(s)
	}

	contextHigh := ga.policy.highPrivilegePhase(ie.Key.Phase)
	for _, matcher := range ga.policy.HighPrivilegeInstances {
		if ie.Key.Match(matcher) {
			contextHigh = true
			ie.High = true
			ie.Kinds["high-privilege-instance"] = true
			ie.Reasons = append(ie.Reasons, "instance matches explicit high-privilege policy")
		}
	}

	for _, s := range ie.Sources {
		if containsString(ga.policy.HighPrivilegeCapabilities, s.Capability) {
			ie.High = true
			ie.Kinds["high-privilege-capability"] = true
			ie.Reasons = append(ie.Reasons, "capability "+s.Capability)
		}
	}

	if contextHigh && ga.policy.denyTemporary() {
		for _, ip := range ie.Presences {
			rec, ok := ga.grants[ip.UID]
			if ok && rec.Grant.Temporary {
				ie.High = true
				ie.Kinds["script-credential-smuggling"] = true
				ie.Reasons = append(ie.Reasons, "temporary credential "+rec.Grant.Capability+" reaches "+string(ie.Key.Phase))
				break
			}
		}
	}

	if len(ie.CacheTaints) > 0 {
		ie.High = true
		ie.Kinds["cache-key-poisoning"] = true
		ie.Reasons = append(ie.Reasons, "shared writable cache key taints publish")
	}

	ie.BranchHigh = map[string]bool{}
	for branch, caps := range ie.BranchCaps {
		branchHigh := false
		for cap := range caps {
			if containsString(ga.policy.HighPrivilegeCapabilities, cap) {
				branchHigh = true
			}
			if contextHigh && ga.policy.denyTemporary() {
				if ga.branchHasTemporary(ie, branch) {
					branchHigh = true
				}
			}
		}
		for _, rule := range ga.policy.HighPrivilegeUnions {
			if capsContainAll(caps, rule.Capabilities) {
				branchHigh = true
			}
		}
		ie.BranchHigh[branch] = branchHigh
	}

	for _, rule := range ga.policy.HighPrivilegeUnions {
		ue, ok := selectUnion(rule, ie.Sources, ie.BranchHigh)
		if !ok {
			continue
		}
		if ue.Optional && !hasIndependentBranchPair(ar, ue.Branches) {
			ue.Optional = false
		}
		ie.Unions = append(ie.Unions, ue)
		ie.High = true
		if ue.Optional && len(ue.Branches) >= 2 {
			ie.Kinds["dual-optional-union-escalation"] = true
			ie.Reasons = append(ie.Reasons, "optional branches "+strings.Join(ue.Branches, ",")+" jointly satisfy "+rule.Name)
		} else {
			ie.Kinds["privilege-union"] = true
			ie.Reasons = append(ie.Reasons, "capability union satisfies "+rule.Name)
		}
	}

	if ga.policy.denyOptionalUnification() && !ie.Kinds["dual-optional-union-escalation"] {
		branchSet := map[string]bool{}
		for branch := range ie.ReachableBranches {
			branchSet[branch] = true
		}
		for _, s := range ie.Sources {
			if strings.HasPrefix(s.Branch, "$union:") {
				for _, b := range strings.Split(strings.TrimPrefix(s.Branch, "$union:"), ",") {
					branchSet[b] = true
				}
				continue
			}
			if s.Branch != "" && s.Branch != "$trusted" {
				branchSet[s.Branch] = true
			}
		}
		branches := sortedKeys(branchSet)
		individuallyCompliant := len(branches) >= 2
		for _, b := range branches {
			if ie.BranchHigh[b] {
				individuallyCompliant = false
			}
		}
		independent := hasIndependentBranchPair(ar, branches)
		if individuallyCompliant && independent {
			ue := unionEvidence{
				Rule:     PrivilegeUnion{Name: "optional-branch-unification"},
				Branches: branches,
				Optional: true,
			}
			targetEdgeID := ""
			if len(ie.EdgeIDs) > 0 {
				targetEdgeID = ie.EdgeIDs[0]
			}
			for _, branch := range branches {
				found := false
				for _, s := range ie.Sources {
					if s.Branch == branch {
						ue.Sources = append(ue.Sources, s)
						found = true
						break
					}
				}
				if !found {
					ue.Sources = append(ue.Sources, capSource{Branch: branch, EdgeID: targetEdgeID})
				}
			}
			ie.Unions = append(ie.Unions, ue)
			ie.High = true
			ie.Kinds["dual-optional-union-escalation"] = true
			ie.Reasons = append(ie.Reasons, "two individually compliant optional branches unify at high-privilege instance")
		}
	}

	if ga.policy.SignatureVerifier != nil {
		for _, ip := range ie.Presences {
			rec := ga.grants[ip.UID]
			if err := ga.policy.SignatureVerifier.VerifySignature(rec.Grant); err != nil {
				ie.InvalidSignature = append(ie.InvalidSignature, ip.UID+": "+err.Error())
				ie.High = true
				ie.Kinds["invalid-signature"] = true
			}
		}
	}
}

func (ga *graphAnalysis) branchHasTemporary(ie *highEvidence, branch string) bool {
	for _, ip := range ie.Presences {
		b := ip.Branch
		if b == "" {
			b = "$trusted"
		}
		if b != branch {
			continue
		}
		if rec, ok := ga.grants[ip.UID]; ok && rec.Grant.Temporary {
			return true
		}
	}
	return false
}

func (ga *graphAnalysis) composeHalves(presences []instancePresence) []capSource {
	type groupKey struct{ family, tx string }
	groups := map[groupKey][]instancePresence{}
	for _, ip := range presences {
		rec, ok := ga.grants[ip.UID]
		if !ok || rec.Grant.Half == nil {
			continue
		}
		tx := rec.Grant.TransactionID
		if tx == "" {
			continue
		}
		k := groupKey{family: rec.Grant.Half.Family, tx: tx}
		groups[k] = append(groups[k], ip)
	}

	var out []capSource
	for k, members := range groups {
		total := 0
		byIndex := map[int]instancePresence{}
		for _, ip := range members {
			rec := ga.grants[ip.UID]
			h := rec.Grant.Half
			total = h.Total
			byIndex[h.Index] = ip
		}
		complete := total > 0
		for i := 1; i <= total; i++ {
			if _, ok := byIndex[i]; !ok {
				complete = false
			}
		}
		if !complete {
			continue
		}
		branches := map[string]bool{}
		var edgeID string
		var memberList []instancePresence
		for i := 1; i <= total; i++ {
			ip := byIndex[i]
			memberList = append(memberList, ip)
			if ip.Branch != "" {
				branches[ip.Branch] = true
			}
			if edgeID == "" {
				edgeID = ip.EdgeID
			}
		}
		branch := ""
		if len(branches) == 1 {
			for b := range branches {
				branch = b
			}
		} else if len(branches) > 1 {
			branch = "$union:" + strings.Join(sortedKeys(branches), ",")
		}
		out = append(out, capSource{
			Capability: "credential:" + k.family,
			Branch:     branch,
			TxID:       k.tx,
			EdgeID:     edgeID,
			Composed:   true,
			Members:    memberList,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Capability < out[j].Capability })
	return out
}

func selectUnion(rule PrivilegeUnion, sources []capSource, branchHigh map[string]bool) (unionEvidence, bool) {
	// Backtracking assignment of each required capability to one source. Keep
	// the first assignment maximizing branch diversity and transaction match.
	byCap := map[string][]capSource{}
	for _, s := range sources {
		byCap[s.Capability] = append(byCap[s.Capability], s)
	}
	for _, wanted := range rule.Capabilities {
		if len(byCap[wanted]) == 0 {
			return unionEvidence{}, false
		}
	}

	var best *unionEvidence
	var chosen []capSource
	var walk func(i int, tx string, branches map[string]bool)
	walk = func(i int, tx string, branches map[string]bool) {
		if i == len(rule.Capabilities) {
			ue := unionEvidence{Rule: rule, Sources: append([]capSource(nil), chosen...)}
			branchSet := map[string]bool{}
			for b := range branches {
				if b != "" && b != "$trusted" {
					branchSet[b] = true
				}
			}
			ue.Branches = sortedKeys(branchSet)
			ue.Optional = len(ue.Branches) >= 2
			if best == nil || scoreUnion(ue) > scoreUnion(*best) {
				cp := ue
				best = &cp
			}
			return
		}
		wanted := rule.Capabilities[i]
		for _, s := range byCap[wanted] {
			nextTx := tx
			if rule.SameTransaction {
				if s.TxID == "" {
					continue
				}
				if nextTx == "" {
					nextTx = s.TxID
				} else if nextTx != s.TxID {
					continue
				}
			}
			branch := s.Branch
			// Composed cross-branch source already encodes multiple branches.
			nextBranches := cloneBoolSet(branches)
			if strings.HasPrefix(branch, "$union:") {
				for _, b := range strings.Split(strings.TrimPrefix(branch, "$union:"), ",") {
					nextBranches[b] = true
				}
			} else if branch != "" && branch != "$trusted" {
				nextBranches[branch] = true
			}
			chosen = append(chosen, s)
			walk(i+1, nextTx, nextBranches)
			chosen = chosen[:len(chosen)-1]
		}
	}
	walk(0, "", map[string]bool{})
	if best == nil {
		return unionEvidence{}, false
	}

	// The "dual optional" label is reserved for branches that are individually
	// below the high-privilege boundary.
	if best.Optional {
		for _, b := range best.Branches {
			if branchHigh[b] {
				best.Optional = false
				break
			}
		}
	}
	return *best, true
}

func scoreUnion(u unionEvidence) int {
	score := len(u.Branches) * 10
	if u.Optional {
		score += 5
	}
	if u.Rule.SameTransaction {
		score += 2
	}
	return score
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func capsContainAll(set map[string]bool, required []string) bool {
	for _, r := range required {
		if !set[r] {
			return false
		}
	}
	return true
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func cloneBoolSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
