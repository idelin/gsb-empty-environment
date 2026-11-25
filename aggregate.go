package evidence

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SelectionMode controls the primary optimization objective.
//
// SelectFewest chooses the smallest valid evidence set (which is the order's
// MinDomains when a solution exists), then the lowest total cost, then the
// lexicographically smallest receipt-id sequence.  SelectMost prefers larger
// valid evidence sets, then the lowest cost, then ids.  Regardless of mode the
// route-origin uniqueness and common-observation-instant constraints apply.
type SelectionMode int

const (
	SelectFewest SelectionMode = iota
	SelectMost
)

// SignatureVerifier verifies a receipt signature.  A nil verifier means the
// calling issuance host has already authenticated the receipt.
type SignatureVerifier func(Order, Witness) error

// ProofVerifier verifies the challenge proof digest against the order.  A nil
// verifier means the proof was authenticated by the signing host.
type ProofVerifier func(Order, Witness) error

// IdentityVerifier authenticates a versioned identity list (for example by
// checking its detached signature).  A nil verifier means the identity lists
// supplied by the caller are already trusted.
type IdentityVerifier func(IdentityList) error

// SupplementVerifier authenticates an identity supplement.  A nil verifier
// means the caller has already authenticated the supplement.
type SupplementVerifier func(IdentitySupplement) error

// Request is the complete input to aggregation.  Witnesses are the raw signed
// receipts; Identities and Supplements are the trusted identity material.
type Request struct {
	Order       Order
	Witnesses   []Witness
	Identities  []IdentityList
	Supplements []IdentitySupplement
	Revocations []Revocation

	// Now is the issuance/decision time.  If zero, Clock is used, and failing
	// that time.Now is used.
	Now   time.Time
	Clock func() time.Time

	Selection        SelectionMode
	SearchLimit      int
	VerifySignature  SignatureVerifier
	VerifyProof      ProofVerifier
	VerifyIdentity   IdentityVerifier
	VerifySupplement SupplementVerifier

	// TrustInlineAttribution is a compatibility escape hatch for callers that
	// deliberately treat Evidence.Attribution as already-authenticated signed
	// evidence when no identity list is available.  It is false by default;
	// independent verification should normally require an identity list.
	TrustInlineAttribution bool
}

// Rejection explains why a raw receipt was not considered.
type Rejection struct {
	WitnessID string `json:"witness_id"`
	Reason    string `json:"reason"`
}

// Evidence is one selected receipt plus the one authenticated attribution the
// aggregator chose for it.  Accept re-resolves the attribution independently
// and ignores the denormalized Domain/RouteOrigin fields for policy decisions.
type Evidence struct {
	Witness       Witness     `json:"witness"`
	AttributionID string      `json:"attribution_id"`
	Attribution   Attribution `json:"attribution"`
	Domain        string      `json:"domain"`
	RouteOrigin   string      `json:"route_origin"`
}

// Bundle is the aggregator's output.  The Claimed* fields are diagnostics and
// are never trusted by Accept/Audit; the verifier recomputes everything from
// Evidence and the raw witness records.
type Bundle struct {
	OrderDigest Digest     `json:"order_digest"`
	IssuedAt    time.Time  `json:"issued_at"`
	Evidence    []Evidence `json:"evidence"`

	Identities  []IdentityList       `json:"identities,omitempty"`
	Supplements []IdentitySupplement `json:"supplements,omitempty"`

	CommonObservation TimeSpan `json:"common_observation"`

	ClaimedDomains      int      `json:"claimed_domains"`
	ClaimedWitnessCount int      `json:"claimed_witness_count"`
	ClaimedTotalCost    int64    `json:"claimed_total_cost"`
	ClaimedPublicIP     bool     `json:"claimed_public_ip"`
	ClaimedDomainsList  []string `json:"claimed_domains_list,omitempty"`

	Forks      []Fork      `json:"forks,omitempty"`
	Duplicates int         `json:"duplicates"`
	Rejections []Rejection `json:"rejections,omitempty"`
}

type candidate struct {
	witness Witness
	attr    Attribution
	domain  string
	origin  string
}

func (c candidate) attributionKey() string { return attributionKey(c.attr) }

type preparedWitness struct {
	witness Witness
	options []candidate
}

// Aggregate selects evidence for the order.  It returns a bundle containing
// the raw selected witnesses.  On failure the returned bundle still contains
// diagnostics (forks, duplicates and rejection reasons).
func Aggregate(req Request) (Bundle, error) {
	order, err := req.Order.Normalize()
	if err != nil {
		return Bundle{}, err
	}
	req.Order = order
	asOf := req.Now
	if asOf.IsZero() {
		if req.Clock != nil {
			asOf = req.Clock()
		} else {
			asOf = time.Now()
		}
	}
	asOf = asOf.UTC()
	if asOf.IsZero() {
		return Bundle{}, fmt.Errorf("%w: zero decision time", ErrInvalidOrder)
	}

	bundle := Bundle{
		OrderDigest:     order.Digest(),
		IssuedAt:        asOf,
		ClaimedPublicIP: order.Validate() == nil,
		Supplements:     append([]IdentitySupplement(nil), req.Supplements...),
	}

	dedup := dedupeWitnesses(req.Witnesses)
	bundle.Forks = append(bundle.Forks, dedup.forks...)
	bundle.Duplicates = dedup.duplicates
	forkedIDs := make([]string, 0, len(dedup.forked))
	for id := range dedup.forked {
		forkedIDs = append(forkedIDs, id)
	}
	sort.Strings(forkedIDs)
	for _, id := range forkedIDs {
		bundle.Rejections = append(bundle.Rejections, Rejection{
			WitnessID: id,
			Reason:    ErrForkedReceipt.Error(),
		})
	}

	ids, err := buildIdentitySet(req.Identities, req.Supplements)
	if err != nil {
		return bundle, err
	}
	if req.VerifyIdentity != nil {
		for _, list := range req.Identities {
			if err := req.VerifyIdentity(list); err != nil {
				return bundle, fmt.Errorf("%w: identity list %q: %v",
					ErrIdentityConflict, list.Version, err)
			}
		}
	}
	if req.VerifySupplement != nil {
		for _, sup := range req.Supplements {
			if err := req.VerifySupplement(sup); err != nil {
				return bundle, fmt.Errorf("%w: identity supplement: %v",
					ErrIdentityConflict, err)
			}
		}
	}

	prepared, rejections := prepareWitnesses(order, dedup, ids, req.Revocations, asOf,
		req.VerifySignature, req.VerifyProof, req.VerifyIdentity)
	bundle.Rejections = append(bundle.Rejections, rejections...)
	bundle.Identities = usedIdentityLists(req.Identities, prepared)

	searcher := &searchState{
		order:    order,
		prepared: prepared,
		mode:     req.Selection,
		limit:    req.SearchLimit,
	}
	searcher.buildSuffixDomains()

	best, ok := searcher.run()
	if searcher.limitExceeded {
		return bundle, fmt.Errorf("%w: explored more than %d nodes",
			ErrSearchLimit, req.SearchLimit)
	}
	if !ok {
		return bundle, fmt.Errorf("%w: %d receipt(s) considered, %d rejected",
			ErrNoValidEvidence, len(prepared), len(rejections))
	}

	bundle.Evidence = make([]Evidence, 0, len(best))
	domains := make(map[string]struct{}, len(best))
	origins := make(map[string]struct{}, len(best))
	var totalCost int64
	for _, c := range best {
		bundle.Evidence = append(bundle.Evidence, Evidence{
			Witness:       c.witness,
			AttributionID: c.attr.ID,
			Attribution:   c.attr,
			Domain:        c.domain,
			RouteOrigin:   c.origin,
		})
		domains[c.domain] = struct{}{}
		origins[c.origin] = struct{}{}
		sum, ok := checkedAdd64(totalCost, c.witness.Cost)
		if !ok {
			return bundle, fmt.Errorf("%w: total cost overflow", ErrInvalidBundle)
		}
		totalCost = sum
	}
	bundle.CommonObservation = commonSpanFor(order, best)
	bundle.ClaimedDomains = len(domains)
	bundle.ClaimedWitnessCount = len(best)
	bundle.ClaimedTotalCost = totalCost
	bundle.ClaimedDomainsList = mapKeys(domains)
	return bundle, nil
}

func prepareWitnesses(order Order, dedup dedupResult, ids *identitySet,
	revocations []Revocation, asOf time.Time,
	verifySig SignatureVerifier, verifyProof ProofVerifier,
	verifyIdentity IdentityVerifier) ([]preparedWitness, []Rejection) {

	var prepared []preparedWitness
	var rejections []Rejection
	reject := func(id, reason string) {
		rejections = append(rejections, Rejection{WitnessID: id, Reason: reason})
	}
	authSpan := TimeSpan{Segments: []Interval{order.AuthorizationWindow}}
	for _, w := range dedup.unique {
		if _, forked := dedup.forked[w.ID]; forked {
			continue
		}
		if err := w.Validate(order); err != nil {
			reject(w.ID, err.Error())
			continue
		}
		if verifySig != nil {
			if err := verifySig(order, w); err != nil {
				reject(w.ID, fmt.Sprintf("signature: %v", err))
				continue
			}
		}
		if verifyProof != nil {
			if err := verifyProof(order, w); err != nil {
				reject(w.ID, fmt.Sprintf("proof: %v", err))
				continue
			}
		}
		if verifyIdentity != nil && w.IdentityList != nil {
			if err := verifyIdentity(*w.IdentityList); err != nil {
				reject(w.ID, fmt.Sprintf("identity list: %v", err))
				continue
			}
		}
		observed, err := w.Observed.Normalize()
		if err != nil {
			reject(w.ID, err.Error())
			continue
		}
		baseSpan := authSpan.Intersect(observed)
		if baseSpan.Empty() {
			reject(w.ID, ErrNoCommonInstant.Error())
			continue
		}
		attrs, err := ids.resolve(w)
		if err != nil {
			reject(w.ID, err.Error())
			continue
		}
		options := make([]candidate, 0, len(attrs))
		var lastErr error
		for _, resolved := range attrs {
			attr := resolved.Attribution.Canonical()
			if err := attr.Validate(); err != nil {
				lastErr = err
				continue
			}
			if attr.RouteProof.IsZero() && !w.RouteProof.IsZero() {
				attr.RouteProof = w.RouteProof
			}
			if attr.Proof.IsZero() && !w.IdentityProof.IsZero() {
				attr.Proof = w.IdentityProof
			}
			if r, revoked := revocationFor(w, attr, revocations, asOf); revoked {
				lastErr = fmt.Errorf("%w: %s %q since %s", ErrRevoked, r.Kind, r.Subject,
					r.RevokedAt.UTC().Format(time.RFC3339Nano))
				continue
			}
			span := baseSpan.Clamp(attr.ValidFrom, attr.ValidUntil)
			if span.Empty() {
				lastErr = fmt.Errorf("%w: attribution %q not valid during observation",
					ErrNoCommonInstant, attr.ID)
				continue
			}
			options = append(options, candidate{
				witness: w,
				attr:    attr,
				domain:  resolved.Domain,
				origin:  resolved.Origin,
			})
		}
		if len(options) == 0 {
			if lastErr == nil {
				lastErr = ErrMissingIdentity
			}
			reject(w.ID, lastErr.Error())
			continue
		}
		prepared = append(prepared, preparedWitness{witness: w, options: options})
	}
	return prepared, rejections
}

type searchState struct {
	order    Order
	prepared []preparedWitness
	mode     SelectionMode
	limit    int
	nodes    int

	suffixDomains []map[string]struct{}

	best          []candidate
	found         bool
	limitExceeded bool
}

func (s *searchState) buildSuffixDomains() {
	n := len(s.prepared)
	s.suffixDomains = make([]map[string]struct{}, n+1)
	s.suffixDomains[n] = map[string]struct{}{}
	for i := n - 1; i >= 0; i-- {
		next := make(map[string]struct{}, len(s.suffixDomains[i+1])+len(s.prepared[i].options))
		for d := range s.suffixDomains[i+1] {
			next[d] = struct{}{}
		}
		for _, opt := range s.prepared[i].options {
			next[opt.domain] = struct{}{}
		}
		s.suffixDomains[i] = next
	}
}

func (s *searchState) run() ([]candidate, bool) {
	k := s.order.MinDomains
	n := len(s.prepared)
	if k < 1 || n < k {
		return nil, false
	}
	switch s.mode {
	case SelectMost:
		for size := n; size >= k; size-- {
			s.best = nil
			s.found = false
			s.searchFixed(size)
			if s.limitExceeded {
				return nil, false
			}
			if s.found {
				return s.best, true
			}
		}
	default:
		s.searchFixed(k)
		if s.limitExceeded {
			return nil, false
		}
		return s.best, s.found
	}
	return nil, false
}

func (s *searchState) searchFixed(size int) {
	n := len(s.prepared)
	k := s.order.MinDomains
	chosen := make([]candidate, 0, size)
	domains := make(map[string]int, size)
	origins := make(map[string]struct{}, size)
	initialSpan := TimeSpan{Segments: []Interval{s.order.AuthorizationWindow}}

	var dfs func(idx int, span TimeSpan, cost int64)
	dfs = func(idx int, span TimeSpan, cost int64) {
		s.nodes++
		if s.limit > 0 && s.nodes > s.limit {
			s.limitExceeded = true
			return
		}
		if len(chosen) == size {
			if len(domains) >= k && !span.Empty() {
				s.consider(chosen, cost, span)
			}
			return
		}
		if idx >= n || len(chosen)+(n-idx) < size {
			return
		}
		if len(domains)+len(s.suffixDomains[idx]) < k {
			return
		}
		if s.found && cost > s.bestCost() {
			return
		}
		// Select this witness with one attribution.
		for _, opt := range s.prepared[idx].options {
			if _, exists := origins[opt.origin]; exists {
				continue
			}
			nextCost, ok := checkedAdd64(cost, opt.witness.Cost)
			if !ok {
				continue
			}
			nextSpan := span.Intersect(opt.witness.Observed).Clamp(opt.attr.ValidFrom, opt.attr.ValidUntil)
			if nextSpan.Empty() {
				continue
			}
			domains[opt.domain]++
			origins[opt.origin] = struct{}{}
			chosen = append(chosen, opt)

			dfs(idx+1, nextSpan, nextCost)

			chosen = chosen[:len(chosen)-1]
			delete(origins, opt.origin)
			domains[opt.domain]--
			if domains[opt.domain] == 0 {
				delete(domains, opt.domain)
			}
			if s.limitExceeded {
				return
			}
		}
		// Skip this witness.
		dfs(idx+1, span, cost)
	}
	dfs(0, initialSpan, 0)
}

func (s *searchState) bestCost() int64 {
	var total int64
	for _, c := range s.best {
		sum, ok := checkedAdd64(total, c.witness.Cost)
		if !ok {
			return int64(^uint64(0) >> 1)
		}
		total = sum
	}
	return total
}

func (s *searchState) consider(chosen []candidate, cost int64, span TimeSpan) {
	if !s.found {
		s.found = true
		s.best = append([]candidate(nil), chosen...)
		return
	}
	bestCost := s.bestCost()
	if cost < bestCost {
		s.best = append([]candidate(nil), chosen...)
		return
	}
	if cost > bestCost {
		return
	}
	if lessCandidateIDs(chosen, s.best) {
		s.best = append([]candidate(nil), chosen...)
		return
	}
	if equalCandidateIDs(chosen, s.best) && lessCandidateAttrs(chosen, s.best) {
		s.best = append([]candidate(nil), chosen...)
	}
}

func lessCandidateIDs(a, b []candidate) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].witness.ID != b[i].witness.ID {
			return a[i].witness.ID < b[i].witness.ID
		}
	}
	return len(a) < len(b)
}

func equalCandidateIDs(a, b []candidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].witness.ID != b[i].witness.ID {
			return false
		}
	}
	return true
}

func lessCandidateAttrs(a, b []candidate) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		ka, kb := a[i].attributionKey(), b[i].attributionKey()
		if ka != kb {
			return ka < kb
		}
	}
	return len(a) < len(b)
}

func commonSpanFor(order Order, chosen []candidate) TimeSpan {
	span := TimeSpan{Segments: []Interval{order.AuthorizationWindow}}
	for _, c := range chosen {
		span = span.Intersect(c.witness.Observed).Clamp(c.attr.ValidFrom, c.attr.ValidUntil)
	}
	return span
}

func usedIdentityLists(input []IdentityList, prepared []preparedWitness) []IdentityList {
	versions := make(map[string]struct{})
	for _, p := range prepared {
		if v := p.witness.IdentityVersion; v != "" {
			versions[v] = struct{}{}
		}
		if p.witness.IdentityList != nil && p.witness.IdentityList.Version != "" {
			versions[p.witness.IdentityList.Version] = struct{}{}
		}
	}
	var out []IdentityList
	for _, list := range input {
		version := strings.TrimSpace(list.Version)
		if _, ok := versions[version]; !ok {
			continue
		}
		clone := IdentityList{
			Version:   version,
			IssuedAt:  list.IssuedAt,
			Entries:   append([]Attribution(nil), list.Entries...),
			Digest:    list.Digest,
			Signature: append([]byte(nil), list.Signature...),
		}
		out = append(out, clone)
	}
	for _, p := range prepared {
		if p.witness.IdentityList == nil {
			continue
		}
		found := false
		for _, list := range out {
			if list.Version == p.witness.IdentityList.Version {
				found = true
				break
			}
		}
		if !found {
			clone := *p.witness.IdentityList
			clone.Entries = append([]Attribution(nil), clone.Entries...)
			clone.Signature = append([]byte(nil), clone.Signature...)
			out = append(out, clone)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
