package capgate

import (
	"fmt"
	"sort"
	"strings"
)

func snapshotEdge(pe *preparedEdge) *Edge {
	if pe == nil || pe.edge == nil {
		return nil
	}
	e := *pe.edge
	if e.ID == "" {
		e.ID = pe.id
	}
	if pe.edge.Grants != nil {
		e.Grants = append([]Grant(nil), pe.edge.Grants...)
	}
	return &e
}

// ReplayResult summarizes a successful independent chain replay.
type ReplayResult struct {
	ChainID      string
	Valid        bool
	CheckedSteps int
	Instance     InstanceKey
	Kind         string
}

func (ga *graphAnalysis) buildChains(ar *assignmentAnalysis, ie *highEvidence) []*GrantChain {
	var out []*GrantChain

	for _, taint := range ie.CacheTaints {
		out = append(out, ga.cacheChain(ar, ie.Key, taint))
	}

	for _, ue := range ie.Unions {
		if c := ga.unionChain(ar, ie.Key, ue); c != nil {
			out = append(out, c)
		}
	}

	if ie.Kinds["script-credential-smuggling"] {
		for _, s := range ie.Sources {
			rec, ok := ga.grants[s.UID]
			if !ok || !rec.Grant.Temporary {
				continue
			}
			if c := ga.credentialChain(ar, ie.Key, s, "script-credential-smuggling", "unsandboxed install/trigger path carries temporary credential to publish"); c != nil {
				out = append(out, c)
				break
			}
		}
	}

	if len(out) == 0 && len(ie.Sources) > 0 {
		s := ie.Sources[0]
		out = append(out, ga.credentialChain(ar, ie.Key, s, firstKind(ie.Kinds), "capability grant reaches high-privilege instance"))
	}
	if len(out) == 0 && len(ie.CacheTaints) == 0 {
		// A high tuple with no concrete source still gets a minimal denial
		// chain so callers can replay the decision context.
		c := &GrantChain{
			Instance:   ie.Key,
			Assignment: ar.Assignment.Clone(),
			Kind:       firstKind(ie.Kinds),
			Valid:      true,
			Reason:     strings.Join(ie.Reasons, "; "),
			Steps: []ChainStep{{
				Kind:   "high-privilege-instance",
				Detail: strings.Join(ie.Reasons, "; "),
			}},
		}
		finalizeChain(c)
		out = append(out, c)
	}
	return out
}

func firstKind(kinds map[string]bool) string {
	var all []string
	for k := range kinds {
		all = append(all, k)
	}
	sort.Strings(all)
	if len(all) == 0 {
		return "high-privilege"
	}
	return all[0]
}

func (ga *graphAnalysis) credentialChain(ar *assignmentAnalysis, key InstanceKey, source capSource, kind, reason string) *GrantChain {
	branch := ga.buildUIDBranch(ar, source.UID, source.EdgeID, source.Branch)
	if branch == nil {
		return nil
	}
	c := &GrantChain{
		Instance:   key,
		Assignment: ar.Assignment.Clone(),
		Kind:       kind,
		Branches:   []ChainBranch{*branch},
		TxID:       source.TxID,
		Valid:      true,
		Reason:     reason,
	}
	appendFinalStep(c, "high-privilege-unification", reason)
	finalizeChain(c)
	return c
}

func (ga *graphAnalysis) unionChain(ar *assignmentAnalysis, key InstanceKey, ue unionEvidence) *GrantChain {
	c := &GrantChain{
		Instance:   key,
		Assignment: ar.Assignment.Clone(),
		Kind:       "privilege-union",
		Valid:      true,
		Reason:     "joint capabilities satisfy " + ue.Rule.Name,
	}
	if ue.Optional {
		c.Kind = "dual-optional-union-escalation"
		c.Reason = "two individually compliant optional branches jointly satisfy " + ue.Rule.Name
	}
	if ue.Rule.SameTransaction {
		tx := ""
		for _, s := range ue.Sources {
			if s.TxID == "" {
				return nil
			}
			if tx == "" {
				tx = s.TxID
			} else if tx != s.TxID {
				return nil
			}
		}
		c.TxID = tx
	}

	seenUID := map[string]bool{}
	for _, source := range ue.Sources {
		if source.Composed {
			for _, member := range source.Members {
				if seenUID[member.UID] {
					continue
				}
				seenUID[member.UID] = true
				if b := ga.buildUIDBranch(ar, member.UID, member.EdgeID, member.Branch); b != nil {
					c.Branches = append(c.Branches, *b)
				}
			}
			continue
		}
		dedupKey := source.UID
		if dedupKey == "" {
			dedupKey = "reach:" + source.Branch
		}
		if seenUID[dedupKey] {
			continue
		}
		seenUID[dedupKey] = true
		var b *ChainBranch
		if source.UID == "" {
			b = ga.buildReachBranch(ar, source.Branch, source.EdgeID, source.Branch)
		} else {
			b = ga.buildUIDBranch(ar, source.UID, source.EdgeID, source.Branch)
		}
		if b != nil {
			c.Branches = append(c.Branches, *b)
		}
	}
	if len(c.Branches) == 0 {
		return nil
	}
	var detail string
	if len(ue.Rule.Capabilities) > 0 {
		detail = "compose capabilities " + strings.Join(ue.Rule.Capabilities, " + ")
		if c.TxID != "" {
			detail += " under transaction " + c.TxID
		} else {
			detail += " without cross-transaction half splicing"
		}
	} else {
		detail = "unify independently compliant optional branches at high-privilege instance"
	}
	appendFinalStep(c, "compose-and-unify", detail)
	finalizeChain(c)
	return c
}

func (ga *graphAnalysis) cacheChain(ar *assignmentAnalysis, key InstanceKey, t cacheTaint) *GrantChain {
	c := &GrantChain{
		Instance:   key,
		Assignment: ar.Assignment.Clone(),
		Kind:       "cache-key-poisoning",
		CacheKey:   t.CacheKey,
		Valid:      true,
		Reason:     t.Reason,
	}
	c.Steps = []ChainStep{
		{
			Kind:          "cache-write",
			EdgeID:        t.WriterID,
			Edge:          snapshotEdge(t.Writer),
			From:          t.Writer.edge.From,
			To:            t.Writer.edge.To,
			CacheKey:      t.CacheKey,
			TransactionID: firstTx(t.Writer.edge.Grants),
			Detail:        fmt.Sprintf("%s writes %s cache at %s", t.WriterID, t.CacheKey, t.Writer.edge.Phase),
		},
		{
			Kind:     "cache-poison-cross-edge",
			CacheKey: t.CacheKey,
			From:     t.Writer.edge.To,
			To:       t.Reader.edge.From,
			Detail:   "tainted shared writable cache entry crosses dependency edge",
		},
		{
			Kind:          "cache-read-publish",
			EdgeID:        t.ReaderID,
			Edge:          snapshotEdge(t.Reader),
			From:          t.Reader.edge.From,
			To:            t.Reader.edge.To,
			CacheKey:      t.CacheKey,
			TransactionID: firstTx(t.Reader.edge.Grants),
			Detail:        fmt.Sprintf("%s reads %s in publish instance", t.ReaderID, t.CacheKey),
		},
	}
	appendFinalStep(c, "publish-taint", t.Reason)
	finalizeChain(c)
	c.EdgeIDs = []string{t.WriterID, t.ReaderID}
	return c
}

func firstTx(grants []Grant) string {
	for _, g := range grants {
		if g.TransactionID != "" {
			return g.TransactionID
		}
	}
	return ""
}

func appendFinalStep(c *GrantChain, kind, detail string) {
	c.Steps = append(c.Steps, ChainStep{
		Kind:          kind,
		Capability:    "",
		TransactionID: c.TxID,
		CacheKey:      c.CacheKey,
		Detail:        detail,
	})
}

func finalizeChain(c *GrantChain) {
	order := 1
	var flat []ChainStep
	var edgeIDs []string
	for bi := range c.Branches {
		b := &c.Branches[bi]
		for si := range b.Steps {
			st := &b.Steps[si]
			st.Order = order
			if st.BranchID == "" {
				st.BranchID = b.BranchID
			}
			flat = append(flat, *st)
			order++
			if st.EdgeID != "" {
				edgeIDs = append(edgeIDs, st.EdgeID)
			}
		}
		b.Optional = b.Optional || b.BranchID != ""
	}
	for i := range c.Steps {
		c.Steps[i].Order = order
		flat = append(flat, c.Steps[i])
		order++
		if c.Steps[i].EdgeID != "" {
			edgeIDs = append(edgeIDs, c.Steps[i].EdgeID)
		}
	}
	c.Steps = flat
	if len(c.EdgeIDs) == 0 {
		c.EdgeIDs = dedupeSorted(edgeIDs)
	}
	c.Valid = true
}

func (ga *graphAnalysis) buildUIDBranch(ar *assignmentAnalysis, uid, targetEdgeID, branchID string) *ChainBranch {
	rec, ok := ga.grants[uid]
	if !ok {
		return nil
	}
	path := ga.findPath(ar, uid, targetEdgeID)
	if len(path) == 0 {
		if pe := ga.prepared[targetEdgeID]; pe != nil {
			path = []*preparedEdge{pe}
		} else {
			return nil
		}
	}

	b := &ChainBranch{BranchID: branchID}
	for i, pe := range path {
		step := ChainStep{
			EdgeID: pe.id,
			Edge:   snapshotEdge(pe),
			From:   pe.edge.From,
			To:     pe.edge.To,
		}
		if i == 0 {
			g := rec.Grant
			step.Kind = "grant"
			step.Grant = &g
			step.Capability = g.Capability
			step.TransactionID = g.TransactionID
			if g.Temporary {
				step.Detail = "temporary credential grant"
			} else if g.Half != nil {
				step.Detail = fmt.Sprintf("credential half %d/%d family %s", g.Half.Index, g.Half.Total, g.Half.Family)
			} else {
				step.Detail = "capability grant"
			}
		} else {
			switch {
			case pe.edge.Phase.IsInstallScript() && !pe.edge.Sandboxed:
				step.Kind = "inherit-credentials"
				step.Detail = "install script has no explicit sandbox boundary; inherits and forwards trigger capability"
			case pe.edge.Optional:
				step.Kind = "optional-propagate"
				step.Detail = "optional dependency path forwards capability"
			default:
				step.Kind = "propagate"
				step.Detail = "monotone capability propagation"
			}
		}
		if pe.edge.Optional {
			b.Optional = true
		}
		b.Steps = append(b.Steps, step)
	}
	if branchID != "" {
		b.BranchID = branchID
	} else if rec.OriginEdge != "" && ga.prepared[rec.OriginEdge] != nil && ga.prepared[rec.OriginEdge].edge.Optional {
		b.BranchID = rec.OriginEdge
		b.Optional = true
	}
	b.Grants = []Grant{rec.Grant}
	return b
}

func (ga *graphAnalysis) findPath(ar *assignmentAnalysis, uid, targetID string) []*preparedEdge {
	rec, ok := ga.grants[uid]
	if !ok {
		return nil
	}
	originID := rec.OriginEdge
	active := map[string]*preparedEdge{}
	for _, pe := range ar.active {
		active[pe.id] = pe
	}
	start := active[originID]
	target := active[targetID]
	if start == nil || target == nil {
		return nil
	}
	if originID == targetID {
		return []*preparedEdge{start}
	}

	parent := map[string]string{originID: ""}
	queue := []string{originID}
	for len(queue) > 0 {
		curID := queue[0]
		queue = queue[1:]
		if curID == targetID {
			break
		}
		cur := active[curID]
		if cur == nil || cur.edge.To == "" {
			continue
		}
		var successors []*preparedEdge
		for _, pe := range ar.active {
			if pe.edge.From == cur.edge.To {
				successors = append(successors, pe)
			}
		}
		sort.SliceStable(successors, func(i, j int) bool { return successors[i].index < successors[j].index })
		for _, next := range successors {
			if _, seen := parent[next.id]; seen {
				continue
			}
			if next.edge.Phase.IsInstallScript() && next.edge.Sandboxed &&
				!strings.HasPrefix(uid, next.id+"/grant/") {
				continue
			}
			parent[next.id] = curID
			queue = append(queue, next.id)
		}
	}
	if _, ok := parent[targetID]; !ok {
		return nil
	}
	var ids []string
	for id := targetID; ; id = parent[id] {
		ids = append(ids, id)
		if parent[id] == "" {
			break
		}
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	path := make([]*preparedEdge, 0, len(ids))
	for _, id := range ids {
		path = append(path, active[id])
	}
	return path
}

func (ga *graphAnalysis) buildReachBranch(ar *assignmentAnalysis, originID, targetID, branchID string) *ChainBranch {
	path := ga.findReachPath(ar, originID, targetID)
	if len(path) == 0 {
		return nil
	}
	b := &ChainBranch{BranchID: branchID, Optional: true}
	for i, pe := range path {
		step := ChainStep{
			EdgeID: pe.id,
			Edge:   snapshotEdge(pe),
			From:   pe.edge.From,
			To:     pe.edge.To,
		}
		if i == 0 {
			step.Kind = "optional-branch"
			step.Capability = "optional-branch:" + originID
			step.Detail = "independent optional dependency branch becomes active"
		} else {
			switch {
			case pe.edge.Phase.IsInstallScript() && !pe.edge.Sandboxed:
				step.Kind = "inherit-credentials"
				step.Detail = "unsandboxed install script connects branch to trigger capabilities"
			case pe.edge.Optional:
				step.Kind = "optional-propagate"
			default:
				step.Kind = "propagate"
			}
		}
		if pe.edge.Optional {
			b.Optional = true
		}
		b.Steps = append(b.Steps, step)
	}
	return b
}

func (ga *graphAnalysis) findReachPath(ar *assignmentAnalysis, originID, targetID string) []*preparedEdge {
	active := map[string]*preparedEdge{}
	for _, pe := range ar.active {
		active[pe.id] = pe
	}
	start := active[originID]
	target := active[targetID]
	if start == nil || target == nil {
		return nil
	}
	if originID == targetID {
		return []*preparedEdge{start}
	}
	parent := map[string]string{originID: ""}
	queue := []string{originID}
	for len(queue) > 0 {
		curID := queue[0]
		queue = queue[1:]
		if curID == targetID {
			break
		}
		cur := active[curID]
		if cur == nil || cur.edge.To == "" {
			continue
		}
		var successors []*preparedEdge
		for _, pe := range ar.active {
			if pe.edge.From == cur.edge.To {
				successors = append(successors, pe)
			}
		}
		sort.SliceStable(successors, func(i, j int) bool { return successors[i].index < successors[j].index })
		for _, next := range successors {
			if _, seen := parent[next.id]; seen {
				continue
			}
			if next.edge.Phase.IsInstallScript() && next.edge.Sandboxed && next.id != originID {
				continue
			}
			parent[next.id] = curID
			queue = append(queue, next.id)
		}
	}
	if _, ok := parent[targetID]; !ok {
		return nil
	}
	var ids []string
	for id := targetID; ; id = parent[id] {
		ids = append(ids, id)
		if parent[id] == "" {
			break
		}
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	path := make([]*preparedEdge, 0, len(ids))
	for _, id := range ids {
		path = append(path, active[id])
	}
	return path
}

// Replay rechecks the self-contained structural grant/cache chain.
func (c *GrantChain) Replay() (ReplayResult, error) {
	return c.ReplayWithVerifier(nil)
}

// ReplayWithVerifier rechecks structure and every embedded grant signature.
func (c *GrantChain) ReplayWithVerifier(v SignatureVerifier) (ReplayResult, error) {
	res := ReplayResult{ChainID: c.ID, Instance: c.Instance, Kind: c.Kind}
	if c == nil {
		return res, fmt.Errorf("nil chain")
	}
	if len(c.Steps) == 0 && len(c.Branches) == 0 {
		return res, fmt.Errorf("chain %s has no steps", c.ID)
	}
	for i, st := range c.Steps {
		if st.Order != i+1 {
			return res, fmt.Errorf("chain %s step order is not contiguous at %d", c.ID, i)
		}
		if st.Edge != nil {
			expr, err := compileCondition(st.Edge.EffectiveCondition())
			if err != nil {
				return res, fmt.Errorf("chain %s edge %s condition: %w", c.ID, st.EdgeID, err)
			}
			if !expr.Eval(c.Assignment) {
				return res, fmt.Errorf("chain %s edge %s is inactive under replay assignment", c.ID, st.EdgeID)
			}
		}
		if st.Grant != nil && v != nil {
			if err := v.VerifySignature(*st.Grant); err != nil {
				return res, fmt.Errorf("grant verification failed: %w", err)
			}
		}
	}

	if c.Kind == "cache-key-poisoning" {
		if err := replayCache(c); err != nil {
			return res, err
		}
	} else {
		if err := replayGrants(c, v); err != nil {
			return res, err
		}
	}
	res.Valid = true
	res.CheckedSteps = len(c.Steps)
	c.Valid = true
	return res, nil
}

func replayCache(c *GrantChain) error {
	var write, read *ChainStep
	for i := range c.Steps {
		switch c.Steps[i].Kind {
		case "cache-write":
			write = &c.Steps[i]
		case "cache-read-publish":
			read = &c.Steps[i]
		}
	}
	if write == nil || read == nil || write.Edge == nil || read.Edge == nil {
		return fmt.Errorf("cache chain lacks embedded writer/reader edges")
	}
	if write.CacheKey == "" || write.CacheKey != read.CacheKey || write.CacheKey != c.CacheKey {
		return fmt.Errorf("cache key mismatch")
	}
	if write.Edge.CacheKey != write.CacheKey || read.Edge.CacheKey != read.CacheKey {
		return fmt.Errorf("embedded edge cacheKey does not match step")
	}
	if !effectiveCacheMode(write.Edge).writes() || !effectiveCacheMode(read.Edge).reads() {
		return fmt.Errorf("cache mode does not permit write/read")
	}
	if write.Edge.Phase.Rank() >= read.Edge.Phase.Rank() {
		return fmt.Errorf("cache writer is not in an earlier phase")
	}
	if read.Edge.Phase != PhasePublish {
		return fmt.Errorf("cache reader is not publish")
	}
	if write.Edge.ID != "" && read.Edge.ID != "" && write.Edge.ID == read.Edge.ID {
		return fmt.Errorf("cache hop is not cross-edge")
	}
	return nil
}

func replayGrants(c *GrantChain, v SignatureVerifier) error {
	checkedBranch := false
	for _, branch := range c.Branches {
		if len(branch.Steps) == 0 {
			continue
		}
		checkedBranch = true
		first := branch.Steps[0]
		if first.Kind != "grant" && first.Kind != "optional-branch" {
			return fmt.Errorf("branch %s does not start with a grant or optional-branch step", branch.BranchID)
		}
		if first.Kind == "grant" && first.Grant == nil {
			return fmt.Errorf("branch %s grant step lacks embedded credential", branch.BranchID)
		}
		if v != nil {
			if err := v.VerifySignature(*first.Grant); err != nil {
				return fmt.Errorf("branch %s signature: %w", branch.BranchID, err)
			}
		}
		var tx string
		for i, st := range branch.Steps {
			if st.Grant != nil {
				if tx == "" {
					tx = st.Grant.TransactionID
				} else if st.Grant.TransactionID != "" && tx != st.Grant.TransactionID {
					return fmt.Errorf("branch %s mixes transaction IDs", branch.BranchID)
				}
			}
			if i == 0 || st.Edge == nil {
				continue
			}
			prev := branch.Steps[i-1]
			if prev.Edge != nil && prev.Edge.To != "" && st.Edge.From != "" && prev.Edge.To != st.Edge.From {
				return fmt.Errorf("branch %s breaks From/To continuity at %s", branch.BranchID, st.EdgeID)
			}
			if st.Edge.Phase.IsInstallScript() && st.Edge.Sandboxed {
				return fmt.Errorf("branch %s crosses sandboxed install edge %s with ambient grant", branch.BranchID, st.EdgeID)
			}
		}
		last := branch.Steps[len(branch.Steps)-1]
		if last.Edge != nil {
			got := InstanceKey{Pkg: last.Edge.Pkg, Digest: last.Edge.Digest, Phase: last.Edge.Phase}
			if got != c.Instance {
				return fmt.Errorf("branch %s ends at %s, want instance %s", branch.BranchID, got, c.Instance)
			}
		}
		if c.TxID != "" && tx != "" && tx != c.TxID {
			return fmt.Errorf("chain transaction %s differs from branch transaction %s", c.TxID, tx)
		}
	}
	if !checkedBranch {
		// A context-only denial chain is replayable as an ordered decision.
		for _, st := range c.Steps {
			if st.Grant != nil && v != nil {
				if err := v.VerifySignature(*st.Grant); err != nil {
					return err
				}
			}
		}
	}

	// Half credentials are transaction scoped. A composed chain must name the
	// common transaction; a chain must never claim cross-transaction splicing.
	for i := range c.Steps {
		st := &c.Steps[i]
		if st.Kind == "compose-and-unify" && c.TxID == "" && strings.Contains(st.Detail, "under transaction") {
			return fmt.Errorf("composed credential lacks transaction binding")
		}
	}
	return nil
}
