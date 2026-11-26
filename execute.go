package crush

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrNoTopology   = errors.New("crush: nil topology root")
	ErrNoRule       = errors.New("crush: no matching rule")
	ErrTakeNotFound = errors.New("crush: take target not found")
)

type runState struct {
	m        *Mapper
	pg       uint64
	rule     *Rule
	replicas int
	res      *Result

	usedOSD    map[int]struct{}
	usedDomain map[string]map[int]struct{}

	slots     []SlotStat
	domains   []*Node
	canonical bool
}

type chosenPlacement struct {
	domain *Node
	leaf   *Node
}

func (m *Mapper) MapPG(pgID any, replicas int, options ...any) (*Result, error) {
	if m == nil {
		return nil, ErrNoTopology
	}
	if m.initErr != nil {
		return nil, m.initErr
	}
	if m.root == nil {
		return nil, ErrNoTopology
	}
	pg, err := normalizePGID(pgID)
	if err != nil {
		return nil, err
	}
	if replicas < 0 {
		return nil, fmt.Errorf("crush: replicas must be non-negative, got %d", replicas)
	}

	rule, ruleName, err := m.resolveRule(options, replicas)
	if err != nil {
		return nil, err
	}

	res := &Result{
		PGID:     pg,
		PG:       pg,
		ID:       pg,
		RuleName: ruleName,
		Rule:     ruleName,
		OSDs:     []int{},
		Slots:    make([]SlotStat, 0, maxInt(replicas, 0)),
	}
	if replicas == 0 {
		finalizeResult(res)
		return res, nil
	}

	st := &runState{
		m:          m,
		pg:         pg,
		rule:       rule,
		replicas:   replicas,
		res:        res,
		usedOSD:    make(map[int]struct{}),
		usedDomain: make(map[string]map[int]struct{}),
	}

	if err := st.execute(rule); err != nil {
		return res, err
	}
	finalizeResult(res)
	return res, nil
}

func (m *Mapper) resolveRule(options []any, replicas int) (*Rule, string, error) {
	m.mu.RLock()
	defaultName := m.defaultRule
	m.mu.RUnlock()

	for _, option := range options {
		switch v := option.(type) {
		case nil:
			continue
		case string:
			if r := m.Rule(v); r != nil {
				return r, v, nil
			}
			if r := m.Rule(defaultName); r != nil {
				return nil, "", fmt.Errorf("%w: %q", ErrNoRule, v)
			}
			return nil, "", fmt.Errorf("%w: %q", ErrNoRule, v)
		case *Rule:
			if v == nil {
				return nil, "", ErrNoRule
			}
			return v, v.Name, nil
		case Rule:
			return &v, v.Name, nil
		case Step:
			r := NewRule("adhoc", v)
			return r, r.Name, nil
		case []Step:
			r := NewRule("adhoc", v...)
			return r, r.Name, nil
		default:
			// Numeric/context-like call options are not needed by the core
			// mapper; ignore them rather than making the variadic API brittle.
			continue
		}
	}

	if r := m.Rule(defaultName); r != nil {
		return r, defaultName, nil
	}
	if len(m.ruleOrder) > 0 {
		name := m.ruleOrder[0]
		return m.Rule(name), name, nil
	}

	r := &Rule{
		Name:  "__default_direct_leaf__",
		Mode:  FirstN,
		Steps: []Step{TakeStep(m.root.ID), ChooseStep(FirstN, replicas, "osd", m.maxAttempts), EmitStep()},
	}
	return r, r.Name, nil
}

func (st *runState) execute(rule *Rule) error {
	takeNode := st.m.root
	currentIDs := []int{}
	currentNodes := []*Node{}
	emitted := []int{}
	haveTake := false

	for stepIndex, step := range rule.Steps {
		op := step.opValue()
		switch op {
		case OpTake:
			target, err := st.resolveTakeTarget(step)
			if err != nil {
				return err
			}
			takeNode = target
			currentNodes = []*Node{target}
			currentIDs = []int{target.ID}
			haveTake = true

		case OpChoose, OpChooseLeaf:
			if !haveTake {
				target := st.m.root
				if step.targetValue() != nil {
					var err error
					target, err = st.resolveTakeTarget(step)
					if err != nil {
						return err
					}
				}
				takeNode = target
				currentNodes = []*Node{target}
				currentIDs = []int{target.ID}
				haveTake = true
			}

			mode := step.modeValue(rule.Mode)
			count := step.countValue(st.replicas)
			domain := step.domainValue()
			leaf := op == OpChooseLeaf || strings.EqualFold(domain, "osd") || strings.EqualFold(domain, "leaf")
			start := takeNode
			if len(currentNodes) == 1 && currentNodes[0] != nil {
				start = currentNodes[0]
			}

			ids, nodes, stats := st.selectReplicas(start, selectConfig{
				stepIndex: stepIndex,
				mode:      mode,
				count:     count,
				domain:    domain,
				leaf:      leaf,
				step:      step,
			})
			currentIDs = ids
			currentNodes = nodes
			st.slots = stats
			st.res.Slots = stats

		case OpEmit:
			emitted = append(emitted, currentIDs...)

		default:
			return fmt.Errorf("crush: unknown rule op %q", firstNonEmpty(step.Op, step.Operation, step.Action))
		}
	}

	if len(emitted) > 0 {
		st.res.OSDs = append([]int(nil), emitted...)
	} else if len(currentIDs) > 0 {
		st.res.OSDs = append([]int(nil), currentIDs...)
	} else {
		st.res.OSDs = []int{}
	}

	// Keep result slots aligned with emitted OSDs when a rule has an emit step.
	if len(st.res.Slots) == len(st.res.OSDs) {
		for i := range st.res.Slots {
			st.res.Slots[i].OSDID = st.res.OSDs[i]
			st.res.Slots[i].OSD = st.res.OSDs[i]
			st.res.Slots[i].ID = st.res.OSDs[i]
			st.res.Slots[i].NodeID = st.res.OSDs[i]
			if st.res.OSDs[i] >= 0 {
				st.res.Slots[i].OK = true
				st.res.Slots[i].Selected = true
			}
		}
	}
	return nil
}

func (st *runState) resolveTakeTarget(step Step) (*Node, error) {
	target := step.targetValue()
	if target == nil {
		return st.m.root, nil
	}
	switch v := target.(type) {
	case int:
		if node := st.m.root.FindNode(v); node != nil {
			return node, nil
		}
	case int64:
		id := int(v)
		if node := st.m.root.FindNode(id); node != nil {
			return node, nil
		}
	case float64:
		id := int(v)
		if node := st.m.root.FindNode(id); node != nil {
			return node, nil
		}
	case string:
		if id, err := strconv.Atoi(v); err == nil {
			if node := st.m.root.FindNode(id); node != nil {
				return node, nil
			}
		}
		var found *Node
		st.m.root.Walk(func(n *Node) {
			if found == nil && (n.Name == v || n.IDString() == v) {
				found = n
			}
		})
		if found != nil {
			return found, nil
		}
		found = firstNodeOfType(st.m.root, v)
		if found != nil {
			return found, nil
		}
	default:
		id, err := normalizeID(target)
		if err == nil {
			if node := st.m.root.FindNode(id); node != nil {
				return node, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrTakeNotFound, target)
}

func firstNodeOfType(root *Node, nodeType string) *Node {
	var found *Node
	root.Walk(func(n *Node) {
		if found == nil && strings.EqualFold(n.NodeType(), nodeType) {
			found = n
		}
	})
	return found
}

type selectConfig struct {
	stepIndex int
	mode      Mode
	count     int
	domain    string
	leaf      bool
	step      Step
}

func (st *runState) selectReplicas(start *Node, cfg selectConfig) ([]int, []*Node, []SlotStat) {
	ids := make([]int, cfg.count)
	nodes := make([]*Node, cfg.count)
	stats := make([]SlotStat, cfg.count)
	for i := range ids {
		ids[i] = -1
	}
	if cfg.count <= 0 || start == nil {
		return ids, nodes, stats
	}

	maxAttempts := cfg.step.maxAttemptsValue(st.m.maxAttempts)
	localAttempts := cfg.step.localAttemptsValue(maxAttempts)
	domain := normalizeDomain(cfg.domain, cfg.leaf)
	cfg.domain = domain
	st.domains = make([]*Node, cfg.count)

	if cfg.mode == Indep {
		if !st.canonical && st.m.stableIndep && cfg.leaf {
			return st.selectStableIndep(start, cfg, maxAttempts, localAttempts)
		}
		for slot := 0; slot < cfg.count; slot++ {
			stat := newSlotStat(slot)
			placement := st.selectIndependentSlot(start, cfg, domain, slot, maxAttempts, localAttempts, &stat)
			st.assignPlacement(placement, slot, ids, nodes, stats, stat)
		}
		return ids, nodes, stats
	}

	sequence := uint64(0)
	for position := 0; position < cfg.count; position++ {
		stat := newSlotStat(position)
		var placement *chosenPlacement
		for attempt := 0; attempt < maxAttempts; attempt++ {
			upperSequence := sequence
			sequence++
			stat.Attempts++

			domainNode, _, reason := st.m.chooseDomain(start, domain, st.pg, uint64(cfg.stepIndex), uint64(position), firstNSeed, upperSequence)
			if reason != "" {
				recordRejection(&stat, reason)
				continue
			}
			if reason = st.checkDomain(domainNode, domain); reason != "" {
				recordRejection(&stat, reason)
				continue
			}

			if !cfg.leaf || isOSDDomain(domain) {
				leaf := domainNode
				if reason = st.validateLeafPlacement(domainNode, leaf); reason != "" {
					recordRejection(&stat, reason)
					continue
				}
				placement = &chosenPlacement{domain: domainNode, leaf: leaf}
				break
			}

			for local := 0; local < localAttempts; local++ {
				if local > 0 {
					stat.LocalRetry = true
					stat.Retried = true
					stat.RetryCount++
				}
				leaf, _, leafReason := st.m.walkToLeaf(domainNode, st.pg, uint64(cfg.stepIndex), firstNSeed, upperSequence, uint64(local))
				stat.Attempts++
				if leafReason != "" {
					recordRejection(&stat, leafReason)
					continue
				}
				if reason = st.validateLeafPlacement(domainNode, leaf); reason != "" {
					recordRejection(&stat, reason)
					continue
				}
				placement = &chosenPlacement{domain: domainNode, leaf: leaf}
				break
			}
			if placement != nil {
				break
			}
			stat.OuterFallback = true
			stat.Fallback = true
			stat.HasFallback = true
			stat.UsedFallback = true
			stat.OuterFallbackCount++
			stat.FallbackCount++
		}
		st.assignPlacement(placement, position, ids, nodes, stats, stat)
	}
	return ids, nodes, stats
}

func (st *runState) selectStableIndep(start *Node, cfg selectConfig, maxAttempts, localAttempts int) ([]int, []*Node, []SlotStat) {
	canonical := &runState{
		m:          st.m.canonicalView(),
		pg:         st.pg,
		rule:       st.rule,
		replicas:   st.replicas,
		usedOSD:    make(map[int]struct{}),
		usedDomain: make(map[string]map[int]struct{}),
		canonical:  true,
	}
	anchorIDs, anchorNodes, anchorStats := canonical.selectReplicas(start, cfg)

	count := cfg.count
	ids := make([]int, count)
	nodes := make([]*Node, count)
	stats := make([]SlotStat, count)
	validAnchor := make([]bool, count)
	for i := range ids {
		ids[i] = -1
		stats[i] = newSlotStat(i)
	}

	for slot := 0; slot < count; slot++ {
		if anchorIDs[slot] < 0 || anchorNodes[slot] == nil {
			continue
		}
		domainNode := canonical.domains[slot]
		if reason := st.validateLeafPlacement(domainNode, anchorNodes[slot]); reason != "" {
			continue
		}

		validAnchor[slot] = true
		st.usedOSD[anchorIDs[slot]] = struct{}{}
		if domainNode != nil && !isOSDDomain(cfg.domain) {
			domainType := domainNode.NodeType()
			if st.usedDomain[domainType] == nil {
				st.usedDomain[domainType] = make(map[int]struct{})
			}
			st.usedDomain[domainType][domainNode.ID] = struct{}{}
		}
	}

	for slot := 0; slot < count; slot++ {
		if validAnchor[slot] {
			ids[slot] = anchorIDs[slot]
			nodes[slot] = anchorNodes[slot]
			stats[slot] = anchorStats[slot]
			st.domains[slot] = canonical.domains[slot]
			continue
		}

		stat := newSlotStat(slot)
		placement := st.selectIndependentSlot(start, cfg, cfg.domain, slot, maxAttempts, localAttempts, &stat)
		st.assignPlacement(placement, slot, ids, nodes, stats, stat)
		if placement != nil {
			st.domains[slot] = placement.domain
		}
	}

	return ids, nodes, stats
}

func (st *runState) selectIndependentSlot(start *Node, cfg selectConfig, domain string, slot, maxAttempts, localAttempts int, stat *SlotStat) *chosenPlacement {
	for upperAttempt := 0; upperAttempt < maxAttempts; upperAttempt++ {
		if upperAttempt > 0 {
			stat.OuterFallback = true
			stat.Fallback = true
			stat.HasFallback = true
			stat.UsedFallback = true
			stat.OuterFallbackCount++
			stat.FallbackCount++
			stat.Retried = true
			stat.RetryCount++
		}

		domainNode, _, reason := st.m.chooseDomain(
			start, domain, st.pg, uint64(cfg.stepIndex), indepSeed, uint64(slot), uint64(upperAttempt),
		)
		stat.Attempts++
		if reason != "" {
			recordRejection(stat, reason)
			continue
		}
		if reason = st.checkDomain(domainNode, domain); reason != "" {
			recordRejection(stat, reason)
			continue
		}

		if !cfg.leaf || isOSDDomain(domain) {
			leaf := domainNode
			if reason = st.validateLeafPlacement(domainNode, leaf); reason != "" {
				recordRejection(stat, reason)
				continue
			}
			return &chosenPlacement{domain: domainNode, leaf: leaf}
		}

		for local := 0; local < localAttempts; local++ {
			if local > 0 {
				stat.LocalRetry = true
				stat.Retried = true
				stat.RetryCount++
			}
			leaf, _, leafReason := st.m.walkToLeaf(
				domainNode, st.pg, uint64(cfg.stepIndex), indepSeed, uint64(slot), uint64(upperAttempt), uint64(local),
			)
			stat.Attempts++
			if leafReason != "" {
				recordRejection(stat, leafReason)
				continue
			}
			if reason = st.validateLeafPlacement(domainNode, leaf); reason != "" {
				recordRejection(stat, reason)
				continue
			}
			return &chosenPlacement{domain: domainNode, leaf: leaf}
		}

		stat.NoLeaf = true
		stat.NoLeafCount++
		stat.Fallback = true
		stat.HasFallback = true
		stat.UsedFallback = true
		stat.FallbackCount++
	}
	stat.Error = "crush: exhausted independent slot attempts"
	return nil
}

const (
	firstNSeed uint64 = 0xf1275ee7
	indepSeed  uint64 = 0x1d3feede
)

func normalizeDomain(domain string, leaf bool) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.TrimSuffix(d, "s")
	if d == "" && leaf {
		return "osd"
	}
	if d == "disk" || d == "device" {
		return "osd"
	}
	return d
}

func isOSDDomain(domain string) bool {
	d := normalizeDomain(domain, false)
	return d == "osd" || d == "leaf"
}

func newSlotStat(slot int) SlotStat {
	return SlotStat{Slot: slot, Index: slot, Position: slot, OSDID: -1, OSD: -1, ID: -1, NodeID: -1}
}

func (st *runState) assignPlacement(placement *chosenPlacement, index int, ids []int, nodes []*Node, stats []SlotStat, stat SlotStat) {
	if placement == nil || placement.leaf == nil {
		ids[index] = -1
		nodes[index] = nil
		stat.OK = false
		stat.Selected = false
		stats[index] = stat
		return
	}

	leaf := placement.leaf
	ids[index] = leaf.ID
	nodes[index] = leaf
	if st.domains != nil && index < len(st.domains) {
		st.domains[index] = placement.domain
	}
	st.usedOSD[leaf.ID] = struct{}{}
	if placement.domain != nil {
		domainType := placement.domain.NodeType()
		if st.usedDomain[domainType] == nil {
			st.usedDomain[domainType] = make(map[int]struct{})
		}
		st.usedDomain[domainType][placement.domain.ID] = struct{}{}
	}

	stat.OSDID = leaf.ID
	stat.OSD = leaf.ID
	stat.ID = leaf.ID
	stat.NodeID = leaf.ID
	stat.OK = true
	stat.Selected = true
	stats[index] = stat
}

func (st *runState) checkDomain(domainNode *Node, domain string) string {
	if domainNode == nil {
		return "no_domain"
	}
	if domain != "" && !isOSDDomain(domain) {
		if used, ok := st.usedDomain[domainNode.NodeType()]; ok {
			if _, duplicate := used[domainNode.ID]; duplicate {
				return "conflict_domain"
			}
		}
		if strings.EqualFold(domainNode.NodeType(), domain) {
			if used, ok := st.usedDomain[domain]; ok {
				if _, duplicate := used[domainNode.ID]; duplicate {
					return "conflict_domain"
				}
			}
		}
	}
	if _, duplicate := st.usedOSD[domainNode.ID]; duplicate && isOSDDomain(domain) {
		return "conflict_osd"
	}
	return ""
}

func (st *runState) validateLeafPlacement(domainNode, leaf *Node) string {
	if leaf == nil {
		return "no_leaf"
	}
	if !leaf.IsLeaf() {
		return "not_leaf"
	}

	path := domainNode.PathTo(leaf.ID)
	if len(path) == 0 {
		path = []*Node{leaf}
	}
	for _, node := range path {
		if !st.m.nodeUp(node) {
			return "down"
		}
	}
	if st.m.nodeWeight(leaf) <= 0 {
		return "zero_weight"
	}
	if _, duplicate := st.usedOSD[leaf.ID]; duplicate {
		return "conflict_osd"
	}
	if reason := st.checkDomain(domainNode, domainNode.NodeType()); reason != "" {
		return reason
	}
	return ""
}

func recordRejection(stat *SlotStat, reason string) {
	stat.Retried = true
	stat.RetryCount++
	appendReason(stat, reason)
	switch reason {
	case "conflict_osd", "conflict_domain", "duplicate":
		stat.Conflict = true
		stat.HasConflict = true
		stat.Duplicate = true
		stat.ConflictCount++
		stat.Conflicts++
		stat.DuplicateCount++
	case "down":
		stat.Down = true
		stat.DownCount++
		stat.Fallback = true
		stat.HasFallback = true
		stat.UsedFallback = true
		stat.FallbackCount++
	case "zero_weight":
		stat.ZeroWeight = true
		stat.ZeroWeightCount++
		stat.Fallback = true
		stat.HasFallback = true
		stat.UsedFallback = true
		stat.FallbackCount++
	case "no_leaf", "no_candidate", "no_positive_child", "not_found", "not_leaf", "no_domain":
		stat.NoLeaf = true
		stat.NoLeafCount++
		stat.Fallback = true
		stat.HasFallback = true
		stat.UsedFallback = true
		stat.FallbackCount++
	}
}

func appendReason(stat *SlotStat, reason string) {
	if reason == "" {
		return
	}
	for _, existing := range stat.Reasons {
		if existing == reason {
			return
		}
	}
	if len(stat.Reasons) < 16 {
		stat.Reasons = append(stat.Reasons, reason)
	}
}

func finalizeResult(res *Result) {
	if res == nil {
		return
	}
	if res.OSDs == nil {
		res.OSDs = []int{}
	}
	res.OSD = append([]int(nil), res.OSDs...)
	res.Osds = res.OSD
	res.OSDIDs = append([]int(nil), res.OSDs...)
	res.SelectedOSDs = append([]int(nil), res.OSDs...)
	res.Placement = append([]int(nil), res.OSDs...)
	res.Out = append([]int(nil), res.OSDs...)
	res.Output = append([]int(nil), res.OSDs...)
	res.FinalOSDs = append([]int(nil), res.OSDs...)
	res.Stats = res.Slots
	res.SlotStats = res.Slots
	res.Statistics = res.Slots

	res.ConflictSlots = nil
	res.FallbackSlots = nil
	res.MissingSlots = nil
	res.ConflictCount = 0
	res.FallbackCount = 0
	res.MissingCount = 0

	for i := range res.Slots {
		s := &res.Slots[i]
		s.Index = s.Slot
		if s.Position == 0 && i != 0 {
			s.Position = s.Slot
		}
		if s.OSDID == 0 && s.OSD == 0 && s.ID == 0 && s.NodeID == 0 {
			// Keep zero IDs valid; newSlotStat initializes to -1 so this only
			// normalizes externally constructed stats.
		}
		s.OSD = s.OSDID
		s.ID = s.OSDID
		s.NodeID = s.OSDID
		s.HasConflict = s.Conflict || s.HasConflict || s.ConflictCount > 0
		s.Conflict = s.HasConflict
		s.IsConflict = s.HasConflict
		s.HasFallback = s.Fallback || s.HasFallback || s.UsedFallback || s.FallbackCount > 0 || s.OuterFallback
		s.Fallback = s.HasFallback
		s.IsFallback = s.HasFallback
		s.UsedFallback = s.HasFallback
		if s.ConflictCount == 0 && s.Conflicts > 0 {
			s.ConflictCount = s.Conflicts
		}
		if s.FallbackCount == 0 && s.Fallbacks > 0 {
			s.FallbackCount = s.Fallbacks
		}
		if s.RetryCount == 0 && s.Retries > 0 {
			s.RetryCount = s.Retries
		}
		s.Retried = s.Retried || s.RetryCount > 0

		if s.HasConflict {
			res.ConflictSlots = append(res.ConflictSlots, s.Slot)
			res.ConflictCount++
		}
		if s.HasFallback {
			res.FallbackSlots = append(res.FallbackSlots, s.Slot)
			res.FallbackCount++
		}
		if s.OSDID < 0 || !s.Selected {
			res.MissingSlots = append(res.MissingSlots, s.Slot)
			res.MissingCount++
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeID(value any) (int, error) {
	id, err := normalizePGID(value)
	return int(id), err
}
