package pathsec

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// AS0 is the reserved zero AS. In ASPA fixture encodings it denotes an
// explicitly empty provider set.
const AS0 uint32 = 0

// Role is the local BGP/AS path perspective.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleProvider Role = "provider"
	RolePeer     Role = "peer"
	RoleRS       Role = "rs"
	RoleRSClient Role = "rs-client"
)

// PathSegmentType identifies an AS_PATH segment.
type PathSegmentType string

const (
	SegmentSequence       PathSegmentType = "AS_SEQUENCE"
	SegmentSet            PathSegmentType = "AS_SET"
	SegmentConfedSequence PathSegmentType = "AS_CONFED_SEQUENCE"
	SegmentConfedSet      PathSegmentType = "AS_CONFED_SET"
)

// ASPathSegment is a parsed AS_PATH segment.
type ASPathSegment struct {
	Type  PathSegmentType `json:"type"`
	ASNs  []uint32        `json:"asns"`
	ASNs2 []uint32        `json:"ASNs,omitempty"` // compatibility alias for alternate JSON fixtures
}

func (s ASPathSegment) numbers() []uint32 {
	if len(s.ASNs) > 0 {
		return s.ASNs
	}
	return s.ASNs2
}

// NormalizedPath contains a collapsed sequence and normalization metadata.
type NormalizedPath struct {
	ASNs         []uint32 `json:"asns"`
	Original     []uint32 `json:"original,omitempty"`
	Runs         []int    `json:"runs,omitempty"`
	HasASSet     bool     `json:"hasASSet"`
	HasConfedSet bool     `json:"hasConfedSet,omitempty"`
	Loop         bool     `json:"loop"`
	HasAS0       bool     `json:"hasAS0,omitempty"`
}

// NormalizeASNs collapses adjacent identical ASNs and detects loops.
func NormalizeASNs(asPath []uint32) NormalizedPath {
	np := NormalizedPath{Original: append([]uint32(nil), asPath...)}
	seen := make(map[uint32]int)
	for _, asn := range asPath {
		if asn == 0 {
			np.HasAS0 = true
		}
		if len(np.ASNs) == 0 || np.ASNs[len(np.ASNs)-1] != asn {
			np.ASNs = append(np.ASNs, asn)
			np.Runs = append(np.Runs, 1)
		} else {
			np.Runs[len(np.Runs)-1]++
		}
		if prior, ok := seen[asn]; ok && asn != 0 {
			// Adjacent duplicates were collapsed; any second occurrence at a
			// different normalized position is a non-adjacent loop.
			if prior != len(np.ASNs)-1 {
				np.Loop = true
			}
		}
		if asn != 0 {
			seen[asn] = len(np.ASNs) - 1
		}
	}
	return np
}

// DedupASPath collapses adjacent identical ASNs and returns the sequence.
func DedupASPath(asPath []uint32) []uint32 {
	return NormalizeASNs(asPath).ASNs
}

// CollapseASPath is an alias for DedupASPath.
func CollapseASPath(asPath []uint32) []uint32 { return DedupASPath(asPath) }

// NormalizeSegments flattens sequence segments. AS_SET and confederation sets
// are reported so callers can reject them.
func NormalizeSegments(segments []ASPathSegment) NormalizedPath {
	var raw []uint32
	var np NormalizedPath
	for _, seg := range segments {
		switch seg.Type {
		case SegmentSet, "SET":
			np.HasASSet = true
		case SegmentConfedSet:
			np.HasConfedSet = true
		}
		if seg.Type == SegmentSet || seg.Type == SegmentConfedSet ||
			seg.Type == "SET" || seg.Type == "AS_SET" || seg.Type == "AS_CONFED_SET" {
			continue
		}
		raw = append(raw, seg.numbers()...)
	}
	seq := NormalizeASNs(raw)
	seq.HasASSet = np.HasASSet
	seq.HasConfedSet = np.HasConfedSet
	return seq
}

// ASPARecord is a customer AS and its authorized providers.
type ASPARecord struct {
	CustomerAS    uint32   `json:"customerAS"`
	Providers     []uint32 `json:"providers"`
	ExplicitEmpty bool     `json:"explicitEmpty,omitempty"`
}

// ASPATable stores ASPA records indexed by customer AS.
type ASPATable struct {
	mu      sync.RWMutex
	records map[uint32]ASPARecord
}

// NewASPATable creates an empty ASPA table.
func NewASPATable() *ASPATable {
	return &ASPATable{records: make(map[uint32]ASPARecord)}
}

// NewASPATableFromMap builds a table from customer -> providers. A nil slice
// means unpublished; an empty non-nil slice or []uint32{AS0} means explicit
// empty.
func NewASPATableFromMap(records map[uint32][]uint32) *ASPATable {
	table := NewASPATable()
	for customer, providers := range records {
		table.Set(customer, providers)
	}
	return table
}

// Publish stores providers for customerAS. Calling Publish with no providers
// publishes an explicit empty set. A sole AS0 is accepted as the fixture
// encoding of an explicit empty set.
func (t *ASPATable) Publish(customerAS uint32, providers ...uint32) error {
	if customerAS == 0 {
		return fmt.Errorf("%w: ASPA customer AS must not be AS0", ErrInvalidASN)
	}
	record := ASPARecord{CustomerAS: customerAS}
	if len(providers) == 0 || (len(providers) == 1 && providers[0] == AS0) {
		record.ExplicitEmpty = true
		record.Providers = []uint32{}
	} else {
		seen := make(map[uint32]struct{}, len(providers))
		for _, provider := range providers {
			if provider == 0 {
				continue
			}
			if _, dup := seen[provider]; dup {
				continue
			}
			seen[provider] = struct{}{}
			record.Providers = append(record.Providers, provider)
		}
		sort.Slice(record.Providers, func(i, j int) bool { return record.Providers[i] < record.Providers[j] })
	}
	t.mu.Lock()
	if t.records == nil {
		t.records = make(map[uint32]ASPARecord)
	}
	t.records[customerAS] = record
	t.mu.Unlock()
	return nil
}

// MustPublish panics if publication fails.
func (t *ASPATable) MustPublish(customerAS uint32, providers ...uint32) *ASPATable {
	if err := t.Publish(customerAS, providers...); err != nil {
		panic(err)
	}
	return t
}

// PublishEmpty records an explicit empty provider set. This is distinct from
// Revoke/Delete, which leaves no published ASPA.
func (t *ASPATable) PublishEmpty(customerAS uint32) error {
	return t.Publish(customerAS)
}

// SetRecord stores a complete record. Providers []uint32{AS0} is interpreted
// as an explicit empty set.
func (t *ASPATable) SetRecord(record ASPARecord) error {
	providers := record.Providers
	if record.ExplicitEmpty || (len(providers) == 1 && providers[0] == AS0) {
		return t.Publish(record.CustomerAS)
	}
	return t.Publish(record.CustomerAS, providers...)
}

// SetProviders replaces a customer's provider set. A nil slice removes the
// record; a non-nil empty slice or []uint32{AS0} publishes an explicit empty
// set.
func (t *ASPATable) SetProviders(customerAS uint32, providers []uint32) error {
	if providers == nil {
		t.Delete(customerAS)
		return nil
	}
	if len(providers) == 0 || (len(providers) == 1 && providers[0] == AS0) {
		return t.PublishEmpty(customerAS)
	}
	return t.Publish(customerAS, providers...)
}

// Set is an alias for SetProviders and panics on an invalid AS0 customer.
func (t *ASPATable) Set(customerAS uint32, providers []uint32) *ASPATable {
	if err := t.SetProviders(customerAS, providers); err != nil {
		panic(err)
	}
	return t
}

// Delete removes an ASPA, representing an unpublished customer.
func (t *ASPATable) Delete(customerAS uint32) {
	t.mu.Lock()
	delete(t.records, customerAS)
	t.mu.Unlock()
}

// Revoke is an alias for Delete.
func (t *ASPATable) Revoke(customerAS uint32) { t.Delete(customerAS) }

// HasRecord reports whether an ASPA is published.
func (t *ASPATable) HasRecord(customerAS uint32) bool {
	_, ok := t.Get(customerAS)
	return ok
}

// Get returns a copy of the record and whether it was published.
func (t *ASPATable) Get(customerAS uint32) (ASPARecord, bool) {
	if t == nil {
		return ASPARecord{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	rec, ok := t.records[customerAS]
	if !ok {
		return ASPARecord{}, false
	}
	rec.Providers = append([]uint32(nil), rec.Providers...)
	return rec, true
}

// Providers returns the authorized provider slice. The boolean is false when
// no record exists. An explicit empty record returns a non-nil empty slice and
// true.
func (t *ASPATable) Providers(customerAS uint32) ([]uint32, bool) {
	rec, ok := t.Get(customerAS)
	if !ok {
		return nil, false
	}
	if rec.Providers == nil {
		return []uint32{}, true
	}
	return rec.Providers, true
}

// RawProviders preserves the requested AS0 encoding for explicit empties.
func (t *ASPATable) RawProviders(customerAS uint32) ([]uint32, bool) {
	rec, ok := t.Get(customerAS)
	if !ok {
		return nil, false
	}
	if rec.ExplicitEmpty {
		return []uint32{AS0}, true
	}
	return rec.Providers, true
}

// IsExplicitEmpty reports a published but empty provider set.
func (t *ASPATable) IsExplicitEmpty(customerAS uint32) bool {
	rec, ok := t.Get(customerAS)
	return ok && rec.ExplicitEmpty
}

// Check reports whether providerAS is explicitly authorized for customerAS.
func (t *ASPATable) Check(customerAS, providerAS uint32) Status {
	rec, ok := t.Get(customerAS)
	if !ok {
		return StatusUnknown
	}
	if providerAS == 0 || rec.ExplicitEmpty {
		return StatusInvalid
	}
	for _, candidate := range rec.Providers {
		if candidate == providerAS {
			return StatusValid
		}
	}
	return StatusInvalid
}

// ASPAHopCheck contains both directional provider checks for an adjacency.
type ASPAHopCheck struct {
	Index int    `json:"index"`
	Left  uint32 `json:"left"`
	Right uint32 `json:"right"`
	// RightIsProvider checks Right in ASPA[Left].
	RightIsProvider Status `json:"rightIsProvider"`
	// LeftIsProvider checks Left in ASPA[Right].
	LeftIsProvider Status `json:"leftIsProvider"`
	// KnownPeer is true when policy explicitly identifies this edge as peer.
	KnownPeer bool `json:"knownPeer,omitempty"`
}

// ASPARequest configures role-aware ASPA verification.
type ASPARequest struct {
	// ASPath is the conventional BGP AS_PATH: leftmost is the nearest
	// advertising AS and rightmost is the origin.
	ASPath []uint32 `json:"asPath,omitempty"`
	// Segments can represent AS_SET-bearing AS_PATH attributes.
	Segments []ASPathSegment `json:"segments,omitempty"`
	// HasASSet/ASSet are concise markers for fixtures that do not model
	// full AS_PATH segments.
	HasASSet bool     `json:"hasASSet,omitempty"`
	ASSet    []uint32 `json:"asSet,omitempty"`

	// Role is the receive perspective. By default it names the side from
	// which the route was received: customer, provider, peer, rs, or
	// rs-client. Set RoleIsLocal to interpret it as the local RFC 9234 role.
	Role        Role   `json:"role,omitempty"`
	LocalRole   Role   `json:"localRole,omitempty"`
	ReceiveSide Role   `json:"receiveSide,omitempty"`
	Perspective Role   `json:"perspective,omitempty"`
	RoleIsLocal bool   `json:"roleIsLocal,omitempty"`
	LocalASN    uint32 `json:"localASN,omitempty"`

	// KnownPeerEdges attests flat/peer apex edges. Pairs are unordered.
	KnownPeerEdges [][2]uint32 `json:"knownPeerEdges,omitempty"`
	// AllowUnattestedApex treats a geometrically adjacent apex gap as Valid
	// even when ASPA does not explicitly rule out a provider relationship.
	AllowUnattestedApex bool `json:"allowUnattestedApex,omitempty"`
	// StrictPeerExport enforces the RFC 9234-style rule that a route from a
	// peer must come from the peer's customer cone. The task's baseline ASPA
	// profile uses the more permissive two-ramp rule, so the default is false.
	StrictPeerExport bool `json:"strictPeerExport,omitempty"`
}

// ASPAResult is the complete ASPA evaluation.
type ASPAResult struct {
	Status          Status         `json:"status"`
	RequestedRole   Role           `json:"requestedRole,omitempty"`
	ReceiveSide     Role           `json:"receiveSide,omitempty"`
	Path            []uint32       `json:"path"`
	Normalized      NormalizedPath `json:"normalized"`
	Hops            []ASPAHopCheck `json:"hops,omitempty"`
	CustomerCone    Status         `json:"customerCone"`
	TwoRamp         Status         `json:"twoRamp"`
	UpRampEnd       int            `json:"upRampEnd"`
	UphillApex      int            `json:"uphillApex"`
	DownRampStart   int            `json:"downRampStart"`
	DownhillApex    int            `json:"downhillApex"`
	AdjacentApex    bool           `json:"adjacentApex,omitempty"`
	OverlappingApex bool           `json:"overlappingApex,omitempty"`
	Reasons         []Reason       `json:"reasons,omitempty"`
}

// VerifyASPA evaluates an ASPA request using the supplied table.
func VerifyASPA(table *ASPATable, req ASPARequest) ASPAResult {
	normalized := req.normalizedPath()
	side := req.receiveSide()
	result := ASPAResult{
		RequestedRole: req.Role,
		ReceiveSide:   side,
		Normalized:    normalized,
		Path:          normalized.ASNs,
		UpRampEnd:     0,
		UphillApex:    0,
		DownRampStart: maxInt(0, len(normalized.ASNs)-1),
		DownhillApex:  maxInt(0, len(normalized.ASNs)-1),
	}

	if normalized.HasASSet || normalized.HasConfedSet {
		result.Status = StatusInvalid
		result.TwoRamp = StatusInvalid
		result.CustomerCone = StatusInvalid
		result.Reasons = append(result.Reasons, Reason{Code: "as_set_rejected", Message: "AS_SET/confederation set must be rejected"})
		return result
	}
	if len(normalized.ASNs) == 0 {
		result.Status = StatusInvalid
		result.Reasons = append(result.Reasons, Reason{Code: "empty_as_path", Message: ErrEmptyPath.Error()})
		return result
	}
	if normalized.Loop {
		result.Status = StatusInvalid
		result.TwoRamp = StatusInvalid
		result.CustomerCone = StatusInvalid
		result.Reasons = append(result.Reasons, Reason{Code: "as_path_loop", Message: "non-adjacent repeated ASN creates a loop"})
		return result
	}
	if normalized.HasAS0 {
		result.Status = StatusInvalid
		result.Reasons = append(result.Reasons, Reason{Code: "as0_in_path", Message: "AS0 is reserved and cannot occur in AS_PATH"})
		return result
	}

	n := len(normalized.ASNs)
	peerEdges := make(map[[2]uint32]bool, len(req.KnownPeerEdges))
	for _, edge := range req.KnownPeerEdges {
		peerEdges[canonicalPair(edge[0], edge[1])] = true
	}
	for i := 0; i+1 < n; i++ {
		left, right := normalized.ASNs[i], normalized.ASNs[i+1]
		hop := ASPAHopCheck{
			Index:           i,
			Left:            left,
			Right:           right,
			RightIsProvider: table.Check(left, right),
			LeftIsProvider:  table.Check(right, left),
			KnownPeer:       peerEdges[canonicalPair(left, right)],
		}
		result.Hops = append(result.Hops, hop)
	}

	result.CustomerCone = classifyCustomerCone(result.Hops)
	result.TwoRamp, result.UphillApex, result.DownhillApex, result.AdjacentApex, result.OverlappingApex =
		classifyTwoRamp(result.Hops, req.AllowUnattestedApex)
	result.UpRampEnd = result.UphillApex
	result.DownRampStart = result.DownhillApex

	switch side {
	case RoleCustomer, RoleRS:
		result.Status = result.CustomerCone
		if result.Status != StatusValid {
			result.Reasons = append(result.Reasons, Reason{
				Code:    "customer_side_uphill_required",
				Message: "routes received from a customer/RS-client must form a complete origin-to-customer up-ramp",
			})
		}
	case RolePeer:
		if req.StrictPeerExport {
			result.Status = result.CustomerCone
			if result.Status != StatusValid {
				result.Reasons = append(result.Reasons, Reason{
					Code:    "peer_customer_cone_required",
					Message: "routes received from a peer must be limited to the peer's customer cone",
				})
			}
		} else {
			result.Status = result.TwoRamp
		}
	default:
		result.Status = result.TwoRamp
	}

	if result.Status == StatusInvalid {
		for _, hop := range result.Hops {
			if hop.RightIsProvider == StatusInvalid || hop.LeftIsProvider == StatusInvalid {
				result.Reasons = append(result.Reasons, Reason{
					Code:    "unauthorized_provider",
					Message: fmt.Sprintf("adjacency AS%d/AS%d contains an explicitly unauthorized provider direction", hop.Left, hop.Right),
					Index:   hop.Index,
				})
			}
		}
	}
	return result
}

func (r ASPARequest) normalizedPath() NormalizedPath {
	var np NormalizedPath
	if len(r.Segments) > 0 {
		np = NormalizeSegments(r.Segments)
		if len(r.ASPath) > 0 && len(np.ASNs) == 0 {
			return NormalizeASNs(r.ASPath)
		}
	} else {
		np = NormalizeASNs(r.ASPath)
	}
	if r.HasASSet || len(r.ASSet) > 0 {
		np.HasASSet = true
	}
	return np
}

func (r ASPARequest) receiveSide() Role {
	for _, candidate := range []Role{r.ReceiveSide, r.Perspective, r.LocalRole, r.Role} {
		if candidate == "" {
			continue
		}
		role := normalizeRole(candidate)
		if candidate == r.LocalRole || (candidate == r.Role && r.RoleIsLocal) {
			return localRoleToReceiveSide(role)
		}
		return role
	}
	return ""
}

func normalizeRole(role Role) Role {
	s := strings.ToLower(strings.TrimSpace(string(role)))
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case "customer", "cust", "c":
		return RoleCustomer
	case "provider", "prov", "p", "transit":
		return RoleProvider
	case "peer", "peering":
		return RolePeer
	case "rs", "route-server", "route_server":
		return RoleRS
	case "rs-client", "rsclient", "client":
		return RoleRSClient
	default:
		return Role(s)
	}
}

func localRoleToReceiveSide(role Role) Role {
	switch normalizeRole(role) {
	case RoleProvider, RoleRS:
		// Local provider/RS receives from a customer/RS-client.
		return RoleCustomer
	case RoleCustomer:
		return RoleProvider
	default:
		return normalizeRole(role)
	}
}

type edgeNeed int

const (
	needRightProvider edgeNeed = iota // A: Left customer, Right provider
	needLeftProvider                  // B: Right customer, Left provider
)

func classifyCustomerCone(hops []ASPAHopCheck) Status {
	status := StatusValid
	for _, hop := range hops {
		// Conventional path is nearest-to-origin. A customer-facing path is
		// all downhill in that order, so each right AS must be the customer
		// and each left AS the provider: Check(Right, Left).
		if hop.LeftIsProvider == StatusInvalid {
			// An explicit negative anywhere in the mandatory customer cone
			// dominates missing data earlier in the path.
			status = StatusInvalid
			continue
		}
		if hop.LeftIsProvider == StatusUnknown && status != StatusInvalid {
			status = StatusUnknown
		}
	}
	return status
}

func classifyTwoRamp(hops []ASPAHopCheck, allowUnattestedFlat bool) (status Status, upApex int, downApex int, adjacent bool, overlap bool) {
	n := len(hops) + 1
	if len(hops) == 0 {
		return StatusValid, 0, 0, false, true
	}

	foundValid := false
	foundUnknown := false
	var unknownUp, unknownDown int
	var unknownAdjacent, unknownOverlap bool

	consider := func(upTo int, flatEdge *int) {
		if foundValid {
			return
		}
		// Direct/overlap schema:
		// edges [0, upTo) require RightIsProvider (receiver-side up-ramp);
		// edges [upTo, m) require LeftIsProvider (origin-side up-ramp).
		candidate := StatusValid
		for i := 0; i < upTo; i++ {
			candidate = combineCandidate(candidate, hops[i].RightIsProvider)
		}
		startDown := upTo
		if flatEdge != nil {
			startDown = *flatEdge + 1
			flatStatus := classifyFlatEdge(hops[*flatEdge], allowUnattestedFlat)
			candidate = combineCandidate(candidate, flatStatus)
		}
		for i := startDown; i < len(hops); i++ {
			candidate = combineCandidate(candidate, hops[i].LeftIsProvider)
		}
		if candidate == StatusInvalid {
			return
		}

		up := upTo
		down := upTo
		isAdjacent := false
		isOverlap := true
		if flatEdge != nil {
			up = *flatEdge
			down = *flatEdge + 1
			isAdjacent = true
			isOverlap = false
		}

		if candidate == StatusValid {
			foundValid = true
			upApex, downApex = up, down
			adjacent, overlap = isAdjacent, isOverlap
			return
		}
		if candidate == StatusUnknown && !foundUnknown {
			foundUnknown = true
			unknownUp, unknownDown = up, down
			unknownAdjacent, unknownOverlap = isAdjacent, isOverlap
		}
	}

	for apex := 0; apex < n; apex++ {
		consider(apex, nil)
	}
	for edge := 0; edge < len(hops); edge++ {
		consider(edge, &edge)
	}

	if foundValid {
		return StatusValid, upApex, downApex, adjacent, overlap
	}
	if foundUnknown {
		return StatusUnknown, unknownUp, unknownDown, unknownAdjacent, unknownOverlap
	}

	// Preserve useful greedy apex positions even for Invalid results.
	leftTop := 0
	for i := 0; i < len(hops); i++ {
		if hops[i].RightIsProvider == StatusValid {
			leftTop = i + 1
		} else {
			break
		}
	}
	rightTop := n - 1
	for i := len(hops) - 1; i >= 0; i-- {
		if hops[i].LeftIsProvider == StatusValid {
			rightTop = i
		} else {
			break
		}
	}
	return StatusInvalid, leftTop, rightTop, rightTop == leftTop+1, rightTop <= leftTop
}

func classifyFlatEdge(hop ASPAHopCheck, allowUnattested bool) Status {
	if hop.KnownPeer {
		return StatusValid
	}
	// If both directional provider hypotheses are explicitly unauthorized,
	// the adjacency is attested as a flat/peer edge by ASPA data.
	if hop.RightIsProvider == StatusInvalid && hop.LeftIsProvider == StatusInvalid {
		return StatusValid
	}
	if allowUnattested {
		return StatusValid
	}
	// An explicit negative in only one direction rules out one orientation,
	// but the other direction may still be a missing provider authorization.
	if hop.RightIsProvider == StatusInvalid || hop.LeftIsProvider == StatusInvalid {
		return StatusUnknown
	}
	return StatusUnknown
}

func combineCandidate(current Status, need Status) Status {
	if current == StatusInvalid || need == StatusInvalid {
		return StatusInvalid
	}
	if current == StatusUnknown || need == StatusUnknown {
		return StatusUnknown
	}
	return StatusValid
}

func statusRank(s Status) int {
	switch s {
	case StatusValid:
		return 1
	case StatusUnknown, StatusNotFound:
		return 2
	case StatusInvalid:
		return 3
	default:
		return 0
	}
}

func canonicalPair(a, b uint32) [2]uint32 {
	if a <= b {
		return [2]uint32{a, b}
	}
	return [2]uint32{b, a}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// VerifyASPAConventional is a concise wrapper for common callers.
func VerifyASPAConventional(table *ASPATable, asPath []uint32, role Role) ASPAResult {
	return VerifyASPA(table, ASPARequest{ASPath: asPath, Role: role})
}
