package pathsec

import (
	"fmt"
	"sort"
)

// AttackType names the adversarial outcome.
type AttackType string

const (
	AttackRouteLeak         AttackType = "route_leak"
	AttackPathShortening    AttackType = "path_shortening"
	AttackFalseOriginHijack AttackType = "false_origin_hijack"
	AttackPCountCompression AttackType = "pcount_compression"
)

// WeakASPAMode selects a deliberately incomplete validator.
type WeakASPAMode string

const (
	WeakASPAStrong         WeakASPAMode = "strong"
	WeakASPAOriginRamp     WeakASPAMode = "origin-ramp"
	WeakASPAReceiverRamp   WeakASPAMode = "receiver-ramp"
	WeakASPAOneSided       WeakASPAMode = "one-sided"
	WeakASPAOneSidedEither WeakASPAMode = "either-side"
)

// WeakPolicy describes the deployed/defective validation policy the attacker
// tries to bypass.
type WeakPolicy struct {
	ASPAMode          WeakASPAMode `json:"aspaMode,omitempty"`
	AllowMissingROA   bool         `json:"allowMissingROA"`
	AllowASPAUnknown  bool         `json:"allowASPAUnknown"`
	RequireBGPsec     bool         `json:"requireBGPsec"`
	IgnorePCount      bool         `json:"ignorePCount"`
	AllowUnknownEdges bool         `json:"allowUnknownEdges"`
}

// Searcher is a configurable bounded adversarial-path searcher.
type Searcher struct {
	MaxAS  int
	Policy WeakPolicy
}

// NewSearcher creates a searcher with the vulnerable weak policy default.
func NewSearcher() *Searcher {
	return &Searcher{MaxAS: MaxAdversarialAS, Policy: DefaultWeakPolicy()}
}

// Search runs search using the searcher's default policy.
func (s *Searcher) Search(g *AttackGraph, req SearchRequest) (*SearchResult, error) {
	if g == nil {
		g = NewGraph()
	}
	if s != nil {
		if req.Policy == (WeakPolicy{}) {
			req.Policy = s.Policy
		}
		if s.MaxAS > 0 && len(g.Nodes) > s.MaxAS {
			return nil, fmt.Errorf("%w: configured limit %d", ErrGraphLimit, s.MaxAS)
		}
	}
	return g.Search(req)
}

// FindShortest returns the shortest chain.
func (s *Searcher) FindShortest(g *AttackGraph, req SearchRequest) (*AttackChain, error) {
	result, err := s.Search(g, req)
	if err != nil {
		return nil, err
	}
	return result.Shortest, nil
}

// DefaultWeakPolicy allows missing ROA/ASPA data and uses either one-sided
// ASPA ramp, matching the vulnerable deployment described in the task.
func DefaultWeakPolicy() WeakPolicy {
	return WeakPolicy{
		ASPAMode:         WeakASPAOneSidedEither,
		AllowMissingROA:  true,
		AllowASPAUnknown: true,
		IgnorePCount:     true,
	}
}

// StrictAdmissionPolicy admits only a fully Valid joint result.
func StrictAdmissionPolicy() WeakPolicy {
	return WeakPolicy{ASPAMode: WeakASPAStrong}
}

// AttackAction is an attacker-controlled transformation.
type AttackAction struct {
	Type        string `json:"type"`
	ASN         uint32 `json:"asn,omitempty"`
	Index       int    `json:"index,omitempty"`
	Description string `json:"description,omitempty"`
}

// AttackChain is one successful adversarial path/transformation.
type AttackChain struct {
	Type           AttackType     `json:"type"`
	Key            string         `json:"key"`
	ObserverASN    uint32         `json:"observerASN"`
	ObserverRole   Role           `json:"observerRole,omitempty"`
	NodePath       []uint32       `json:"nodePath,omitempty"`
	TruePath       []uint32       `json:"truePath,omitempty"`
	AdvertisedPath []uint32       `json:"advertisedPath"`
	AttackPath     []uint32       `json:"attackPath,omitempty"`
	ShortenedPath  []uint32       `json:"shortenedPath,omitempty"`
	AttackASN      uint32         `json:"attackASN,omitempty"`
	CorruptedASNs  []uint32       `json:"corruptedASNs,omitempty"`
	Actions        []AttackAction `json:"actions,omitempty"`
	PCounts        []int          `json:"pCounts,omitempty"`
	Length         int            `json:"length"`

	PolicyLeak    bool       `json:"policyLeak,omitempty"`
	MissingROA    bool       `json:"missingROA,omitempty"`
	WeakOneSided  bool       `json:"weakOneSided,omitempty"`
	PCountZero    bool       `json:"pCountZero,omitempty"`
	ROA           ROAResult  `json:"roa,omitempty"`
	ASPA          ASPAResult `json:"aspa,omitempty"`
	WeakASPA      Status     `json:"weakASPA,omitempty"`
	StrongStatus  Status     `json:"strongStatus,omitempty"`
	BGPsecStatus  Status     `json:"bgpsecStatus,omitempty"`
	Admitted      bool       `json:"admitted"`
	Bypass        bool       `json:"bypass"`
	Success       bool       `json:"success"`
	BypassFactors []string   `json:"bypassFactors,omitempty"`
	Description   string     `json:"description,omitempty"`
}

// SearchRequest configures bounded adversarial search.
type SearchRequest struct {
	ObserverASN uint32 `json:"observerASN,omitempty"`
	ObserverAS  uint32 `json:"observerAS,omitempty"`
	LocalASN    uint32 `json:"localASN,omitempty"`
	Role        Role   `json:"role,omitempty"`
	ReceiveSide Role   `json:"receiveSide,omitempty"`
	LocalRole   Role   `json:"localRole,omitempty"`

	Prefix       string `json:"prefix,omitempty"`
	TargetPrefix string `json:"targetPrefix,omitempty"`
	VictimASN    uint32 `json:"victimASN,omitempty"`
	OriginASN    uint32 `json:"originASN,omitempty"`
	AttackerASN  uint32 `json:"attackerASN,omitempty"`

	MaxHops int `json:"maxHops,omitempty"`
	Limit   int `json:"limit,omitempty"`

	// Types defaults to route leaks and path-shortening attacks.
	Types []AttackType `json:"types,omitempty"`

	Policy WeakPolicy `json:"policy,omitempty"`

	ROA  *ROATable  `json:"-"`
	ASPA *ASPATable `json:"-"`
	Keys *KeyStore  `json:"-"`

	// RequireCorrupted defaults to true.
	RequireCorrupted *bool `json:"requireCorrupted,omitempty"`
}

// SearchResult contains sorted attacks and search metadata.
type SearchResult struct {
	Chains         []AttackChain `json:"chains"`
	Attacks        []AttackChain `json:"attacks,omitempty"`
	Shortest       *AttackChain  `json:"shortest,omitempty"`
	ShortestAttack *AttackChain  `json:"shortestAttack,omitempty"`
	RouteLeaks     []AttackChain `json:"routeLeaks,omitempty"`
	Shortenings    []AttackChain `json:"shortenings,omitempty"`
	PCountAttacks  []AttackChain `json:"pCountAttacks,omitempty"`
	Count          int           `json:"count"`
	NodeCount      int           `json:"nodeCount"`
	ExploredPaths  int           `json:"exploredPaths"`
	Exhaustive     bool          `json:"exhaustive"`
}

// Search runs bounded BFS adversarial search on the graph.
func (g *AttackGraph) Search(req SearchRequest) (*SearchResult, error) {
	if err := g.CheckBounds(); err != nil {
		return nil, err
	}
	cfg := normalizeSearchRequest(req, g)
	result := &SearchResult{NodeCount: len(g.Nodes), Exhaustive: true}
	seen := make(map[string]bool)

	observers := cfg.observers(g)
	for _, observer := range observers {
		initial := []uint32{observer}
		queue := [][]uint32{initial}
		for len(queue) > 0 {
			path := queue[0]
			queue = queue[1:]
			result.ExploredPaths++
			for _, chain := range chainsForNodePath(g, path, cfg) {
				addChain(result, chain, seen)
			}
			if len(path)-1 >= cfg.MaxHops {
				continue
			}
			last := path[len(path)-1]
			for _, next := range g.Neighbors(last) {
				if containsAS(path, next) {
					continue
				}
				expanded := append(append([]uint32(nil), path...), next)
				queue = append(queue, expanded)
			}
		}
	}
	finalizeResult(result, cfg.Limit)
	return result, nil
}

// SearchAttacks is a package-level wrapper.
func SearchAttacks(graph *AttackGraph, req SearchRequest) (*SearchResult, error) {
	if graph == nil {
		graph = NewGraph()
	}
	return graph.Search(req)
}

// FindShortestAttack returns the shortest admitted chain of any requested type.
func (g *AttackGraph) FindShortestAttack(req SearchRequest) (*AttackChain, error) {
	result, err := g.Search(req)
	if err != nil {
		return nil, err
	}
	return result.Shortest, nil
}

func normalizeSearchRequest(req SearchRequest, g *AttackGraph) SearchRequest {
	cfg := req
	cfg.ObserverASN = firstNonZero(req.ObserverASN, req.ObserverAS, req.LocalASN)
	cfg.Prefix = firstNonEmpty(req.Prefix, req.TargetPrefix)
	cfg.VictimASN = firstNonZero(req.VictimASN, req.OriginASN)
	if cfg.MaxHops <= 0 || cfg.MaxHops >= MaxAdversarialAS {
		cfg.MaxHops = MaxAdversarialAS - 1
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 0
	}
	if req.Policy == (WeakPolicy{}) {
		cfg.Policy = DefaultWeakPolicy()
	} else if cfg.Policy.ASPAMode == "" {
		cfg.Policy.ASPAMode = WeakASPAOneSidedEither
	}
	if cfg.ASPA == nil {
		cfg.ASPA = g.EffectiveASPA()
	}
	if cfg.ROA == nil {
		cfg.ROA = g.ROA
	}
	if cfg.Keys == nil {
		cfg.Keys = g.Keys
	}
	if len(cfg.Types) == 0 {
		cfg.Types = []AttackType{AttackRouteLeak, AttackPathShortening, AttackFalseOriginHijack, AttackPCountCompression}
	}
	if cfg.RequireCorrupted == nil {
		required := true
		cfg.RequireCorrupted = &required
	}
	return cfg
}

func (req SearchRequest) observers(g *AttackGraph) []uint32 {
	if req.ObserverASN != 0 {
		return []uint32{req.ObserverASN}
	}
	return g.ASNs()
}

func (req SearchRequest) side() Role {
	if req.ReceiveSide != "" {
		return normalizeRole(req.ReceiveSide)
	}
	if req.LocalRole != "" {
		return localRoleToReceiveSide(normalizeRole(req.LocalRole))
	}
	return normalizeRole(req.Role)
}

func (req SearchRequest) typeEnabled(attackType AttackType) bool {
	for _, enabled := range req.Types {
		if enabled == attackType {
			return true
		}
	}
	return false
}

func chainsForNodePath(g *AttackGraph, nodePath []uint32, req SearchRequest) []AttackChain {
	var out []AttackChain
	observer := nodePath[0]
	side := req.side()
	advertised := append([]uint32(nil), nodePath[1:]...)
	corruptOnPath := g.corruptedIn(nodePath)
	requireCorrupt := req.RequireCorrupted == nil || *req.RequireCorrupted

	destination := nodePath[len(nodePath)-1]
	victimMatch := req.VictimASN == 0 || destination == req.VictimASN
	attackerFilter := req.AttackerASN
	orientations := g.OrientationSequence(nodePath)
	if len(orientations) > 0 && orientations[0] == '?' {
		if roleOrientation := roleEdgeOrientation(side); roleOrientation != '?' {
			orientations[0] = roleOrientation
		}
	}
	policyLeak := !IsValleyFree(orientations, req.Policy.AllowUnknownEdges)

	// Route leak candidates use the complete topology/policy path.
	if req.typeEnabled(AttackRouteLeak) && victimMatch && len(nodePath) >= 3 &&
		(!requireCorrupt || len(corruptOnPath) > 0) {
		if policyLeak {
			chain := buildEvaluatedChain(g, req, AttackRouteLeak, observer, side, nodePath,
				advertised, 0, corruptOnPath)
			chain.PolicyLeak = true
			chain.Description = "valley violation accepted by weak one-sided ASPA validation"
			out = append(out, chain)
		}
	}

	// Truncation/false-origin candidates end the advertised path at a
	// corrupted AS.
	if len(nodePath) >= 2 {
		for index := 1; index < len(nodePath); index++ {
			attacker := nodePath[index]
			if !g.Corrupted[attacker] && requireCorrupt {
				continue
			}
			if attackerFilter != 0 && attacker != attackerFilter {
				continue
			}
			shortAdvertised := append([]uint32(nil), nodePath[1:index+1]...)
			hasTail := index < len(nodePath)-1
			attackType := AttackFalseOriginHijack
			if hasTail {
				attackType = AttackPathShortening
			}
			if !req.typeEnabled(attackType) && !req.typeEnabled(AttackPathShortening) {
				continue
			}
			chain := buildEvaluatedChain(g, req, attackType, observer, side, nodePath,
				shortAdvertised, attacker, g.corruptedIn(nodePath[:index+1]))
			chain.ShortenedPath = append([]uint32(nil), shortAdvertised...)
			if hasTail {
				chain.Actions = append(chain.Actions, AttackAction{
					Type: "truncate_tail", ASN: attacker, Index: index - 1,
					Description: "remove honest tail and announce the corrupted AS as terminal origin",
				})
			} else {
				chain.Actions = append(chain.Actions, AttackAction{
					Type: "false_origin", ASN: attacker, Index: index - 1,
					Description: "originate the target prefix at a corrupted AS",
				})
			}
			out = append(out, chain)

			if req.typeEnabled(AttackPCountCompression) {
				pcountChain := chain
				pcountChain.Type = AttackPCountCompression
				pcountChain.Key = ""
				pcountChain.PCounts = ones(len(shortAdvertised))
				pcountIndex := index - 1
				if pcountIndex >= len(pcountChain.PCounts) {
					pcountIndex = len(pcountChain.PCounts) - 1
				}
				if pcountIndex >= 0 {
					pcountChain.PCounts[pcountIndex] = 0
					pcountChain.Actions = append(append([]AttackAction(nil), chain.Actions...), AttackAction{
						Type: "forge_pcount_zero", ASN: attacker, Index: pcountIndex,
						Description: "set pCount=0 to compress/remove a BGPsec hop count",
					})
				}
				applyPCountWeakness(&pcountChain, req.Policy)
				pcountChain.Key = ""
				out = append(out, pcountChain)
			}
		}
	}

	// Standalone pCount compression on the full advertised path.
	if req.typeEnabled(AttackPCountCompression) && len(advertised) > 0 && len(corruptOnPath) > 0 {
		pcountIndex := indexOfAS(advertised, corruptOnPath[0])
		if pcountIndex >= 0 {
			chain := buildEvaluatedChain(g, req, AttackPCountCompression, observer, side, nodePath,
				advertised, corruptOnPath[0], corruptOnPath)
			chain.PCounts = ones(len(advertised))
			chain.PCounts[pcountIndex] = 0
			chain.Actions = append(chain.Actions, AttackAction{
				Type: "forge_pcount_zero", ASN: corruptOnPath[0], Index: pcountIndex,
				Description: "set pCount=0 to compress/remove a BGPsec hop count",
			})
			applyPCountWeakness(&chain, req.Policy)
			out = append(out, chain)
		}
	}

	filtered := out[:0]
	for _, chain := range out {
		if chain.Admitted {
			chain.Key = chain.canonicalKey()
			filtered = append(filtered, chain)
		}
	}
	return filtered
}

func applyPCountWeakness(chain *AttackChain, policy WeakPolicy) {
	chain.PCountZero = true
	chain.BGPsecStatus = StatusInvalid
	chain.StrongStatus = statusWorse(chain.StrongStatus, StatusInvalid)
	if policy.IgnorePCount {
		chain.BGPsecStatus = StatusValid
	}
	admitted, factors := admitWeakly(*chain, policy)
	chain.Admitted = admitted
	chain.Bypass = admitted
	chain.Success = admitted
	chain.BypassFactors = factors
	if admitted && policy.IgnorePCount {
		chain.BypassFactors = append(chain.BypassFactors, "forged_pcount_zero_ignored")
	}
	chain.MissingROA = chain.ROA.Status == StatusNotFound
}

func buildEvaluatedChain(g *AttackGraph, req SearchRequest, attackType AttackType, observer uint32, side Role,
	nodePath, advertised []uint32, attacker uint32, corrupted []uint32) AttackChain {

	chain := AttackChain{
		Type:           attackType,
		ObserverASN:    observer,
		ObserverRole:   side,
		NodePath:       append([]uint32(nil), nodePath...),
		TruePath:       append([]uint32(nil), nodePath...),
		AdvertisedPath: append([]uint32(nil), advertised...),
		AttackASN:      attacker,
		CorruptedASNs:  append([]uint32(nil), corrupted...),
		Length:         len(advertised),
	}
	if len(advertised) == 0 {
		return chain
	}
	origin := advertised[len(advertised)-1]
	prefix := req.Prefix
	if prefix == "" {
		prefix = "0.0.0.0/0"
	}

	roaTable := req.ROA
	if roaTable == nil && g != nil {
		roaTable = g.ROA
	}
	if roaTable != nil && req.Prefix != "" {
		chain.ROA = roaTable.Validate(req.Prefix, origin)
	} else {
		chain.ROA = ROAResult{Status: StatusNotFound}
		if req.Prefix == "" {
			chain.ROA.Reasons = []Reason{{Code: "prefix_unspecified", Message: "ROA check skipped; treating as missing coverage"}}
		}
	}

	aspaTable := req.ASPA
	if aspaTable == nil && g != nil {
		aspaTable = g.EffectiveASPA()
	}
	chain.ASPA = VerifyASPA(aspaTable, ASPARequest{
		ASPath:              advertised,
		Role:                side,
		AllowUnattestedApex: false,
	})
	chain.WeakASPA = EvaluateWeakASPA(aspaTable, ASPARequest{
		ASPath: advertised,
		Role:   side,
	}, req.Policy.ASPAMode)

	joint := Verify(VerifyRequest{
		Prefix:    req.Prefix,
		OriginASN: origin,
		ASPath:    advertised,
		Role:      side,
	}, roaTable, aspaTable, req.Keys)
	chain.StrongStatus = joint.Status

	chain.Admitted, chain.BypassFactors = admitWeakly(chain, req.Policy)
	chain.Bypass = chain.Admitted
	chain.Success = chain.Admitted
	chain.MissingROA = chain.ROA.Status == StatusNotFound
	chain.WeakOneSided = chain.PolicyLeak && chain.WeakASPA == StatusValid && chain.ASPA.Status != StatusValid
	chain.Key = chain.canonicalKey()
	return chain
}

func admitWeakly(chain AttackChain, policy WeakPolicy) (bool, []string) {
	var factors []string
	roaOK := chain.ROA.Status == StatusValid
	if !roaOK {
		if chain.ROA.Status == StatusNotFound || chain.ROA.Status == StatusSkipped {
			if policy.AllowMissingROA {
				factors = append(factors, "missing_roa_allowed")
				roaOK = true
			}
		} else if chain.ROA.Status == StatusUnknown && policy.AllowASPAUnknown {
			// ROA unknown is governed by the same permissive unknown policy.
			if policy.AllowMissingROA {
				factors = append(factors, "roa_unknown_allowed")
				roaOK = true
			}
		}
	}
	if !roaOK {
		return false, nil
	}

	aspaOK := chain.WeakASPA == StatusValid
	if !aspaOK && chain.WeakASPA == StatusUnknown && policy.AllowASPAUnknown {
		factors = append(factors, "aspa_unknown_allowed")
		aspaOK = true
	}
	if !aspaOK {
		return false, nil
	}

	if policy.RequireBGPsec && chain.BGPsecStatus != StatusValid {
		return false, nil
	}
	if chain.BGPsecStatus == StatusInvalid && !policy.IgnorePCount {
		return false, nil
	}
	if chain.PolicyLeak && chain.WeakASPA == StatusValid {
		factors = append(factors, "weak_one_sided_aspa")
	}
	if chain.Type == AttackPathShortening || chain.Type == AttackFalseOriginHijack {
		if hasFactor(factors, "missing_roa_allowed") || hasFactor(factors, "roa_unknown_allowed") {
			factors = append(factors, "short_path_origin_accepted")
		}
	}
	return true, uniqueStrings(factors)
}

// EvaluateWeakASPA implements flawed/one-sided ramp checks.
func EvaluateWeakASPA(table *ASPATable, req ASPARequest, mode WeakASPAMode) Status {
	if mode == "" {
		mode = WeakASPAOneSidedEither
	}
	strong := VerifyASPA(table, req)
	if strong.Status == StatusInvalid && strong.Normalized.Loop {
		return StatusInvalid
	}
	if strong.Normalized.HasASSet || strong.Normalized.HasConfedSet {
		return StatusInvalid
	}
	path := strong.Path
	if len(path) <= 1 {
		return StatusValid
	}
	hops := strong.Hops

	originRamp := func() Status {
		if len(hops) == 0 {
			return StatusValid
		}
		// The origin-adjacent directional authorization must exist; after
		// that the weak checker ignores the opposite/receiver ramp.
		if hops[len(hops)-1].LeftIsProvider != StatusValid {
			return hops[len(hops)-1].LeftIsProvider
		}
		for i := len(hops) - 1; i >= 0; i-- {
			if hops[i].LeftIsProvider != StatusValid {
				break
			}
		}
		return StatusValid
	}
	receiverRamp := func() Status {
		if hops[0].RightIsProvider != StatusValid {
			return hops[0].RightIsProvider
		}
		for i := 0; i < len(hops); i++ {
			if hops[i].RightIsProvider != StatusValid {
				break
			}
		}
		return StatusValid
	}

	switch mode {
	case WeakASPAStrong:
		return strong.Status
	case WeakASPAOriginRamp:
		return originRamp()
	case WeakASPAReceiverRamp:
		return receiverRamp()
	case WeakASPAOneSided, WeakASPAOneSidedEither:
		origin := originRamp()
		receiver := receiverRamp()
		if origin == StatusValid || receiver == StatusValid {
			return StatusValid
		}
		if origin == StatusUnknown || receiver == StatusUnknown {
			return StatusUnknown
		}
		return StatusInvalid
	default:
		return strong.Status
	}
}

func (c AttackChain) canonicalKey() string {
	path := joinAS(c.AdvertisedPath)
	actions := make([]string, 0, len(c.Actions))
	for _, action := range c.Actions {
		actions = append(actions, fmt.Sprintf("%s@%d:%d", action.Type, action.ASN, action.Index))
	}
	return fmt.Sprintf("%s|o%d|r%s|a%d|adv%s|true%s|pc%v|actions%v", c.Type, c.ObserverASN,
		c.ObserverRole, c.AttackASN, path, joinAS(c.TruePath), c.PCounts, actions)
}

func addChain(result *SearchResult, chain AttackChain, seen map[string]bool) {
	if !chain.Admitted {
		return
	}
	key := chain.canonicalKey()
	if seen[key] {
		return
	}
	seen[key] = true
	result.Chains = append(result.Chains, chain)
}

func finalizeResult(result *SearchResult, limit int) {
	sort.SliceStable(result.Chains, func(i, j int) bool {
		if result.Chains[i].Length != result.Chains[j].Length {
			return result.Chains[i].Length < result.Chains[j].Length
		}
		if result.Chains[i].ObserverASN != result.Chains[j].ObserverASN {
			return result.Chains[i].ObserverASN < result.Chains[j].ObserverASN
		}
		return result.Chains[i].Key < result.Chains[j].Key
	})
	if limit > 0 && len(result.Chains) > limit {
		result.Chains = append([]AttackChain(nil), result.Chains[:limit]...)
	}
	result.RouteLeaks = nil
	result.Shortenings = nil
	result.PCountAttacks = nil
	for i := range result.Chains {
		result.Chains[i].Key = result.Chains[i].canonicalKey()
		if len(result.Chains[i].AttackPath) == 0 {
			result.Chains[i].AttackPath = append([]uint32(nil), result.Chains[i].AdvertisedPath...)
		}
		switch result.Chains[i].Type {
		case AttackRouteLeak:
			result.RouteLeaks = append(result.RouteLeaks, result.Chains[i])
		case AttackPathShortening, AttackFalseOriginHijack:
			result.Shortenings = append(result.Shortenings, result.Chains[i])
		case AttackPCountCompression:
			result.PCountAttacks = append(result.PCountAttacks, result.Chains[i])
		}
	}
	result.Count = len(result.Chains)
	result.Attacks = result.Chains
	if len(result.Chains) > 0 {
		shortest := result.Chains[0]
		result.Shortest = &shortest
		result.ShortestAttack = result.Shortest
	}
}

func (g *AttackGraph) corruptedIn(path []uint32) []uint32 {
	var out []uint32
	for _, asn := range path {
		if g.Corrupted[asn] {
			out = append(out, asn)
		}
	}
	return uniqueAS(out)
}

func containsAS(path []uint32, target uint32) bool {
	return indexOfAS(path, target) >= 0
}

func indexOfAS(path []uint32, target uint32) int {
	for i, asn := range path {
		if asn == target {
			return i
		}
	}
	return -1
}

func uniqueAS(in []uint32) []uint32 {
	seen := make(map[uint32]bool)
	var out []uint32
	for _, asn := range in {
		if !seen[asn] {
			seen[asn] = true
			out = append(out, asn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func ones(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

func joinAS(path []uint32) string {
	if len(path) == 0 {
		return ""
	}
	out := fmt.Sprintf("%d", path[0])
	for _, asn := range path[1:] {
		out += fmt.Sprintf(">%d", asn)
	}
	return out
}

func hasFactor(factors []string, target string) bool {
	for _, factor := range factors {
		if factor == target {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
