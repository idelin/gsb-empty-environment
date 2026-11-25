package evidence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Problem is one independent verification failure.
type Problem struct {
	WitnessID string `json:"witness_id,omitempty"`
	Code      string `json:"code"`
	Err       error  `json:"-"`
}

// Acceptance is the result of an independent audit.  Domains, Origins and
// CommonObservation are recomputed from the raw evidence, never read from the
// aggregator's summary fields.
type Acceptance struct {
	Valid             bool
	Domains           []string
	Origins           []string
	CommonObservation TimeSpan
	TotalCost         int64
	Duplicates        int
	Problems          []Problem
}

// Err joins the individual verification failures.
func (a Acceptance) Err() error {
	if len(a.Problems) == 0 {
		return nil
	}
	errs := make([]error, 0, len(a.Problems))
	for _, p := range a.Problems {
		if p.WitnessID != "" {
			errs = append(errs, fmt.Errorf("%s (%s): %w", p.Code, p.WitnessID, p.Err))
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", p.Code, p.Err))
		}
	}
	return errors.Join(errs...)
}

// Accept independently re-validates a bundle at its recorded issuance time.
// It ignores Bundle.ClaimedDomains, Bundle.ClaimedPublicIP and
// Bundle.CommonObservation.  Revocations effective after IssuedAt are ignored,
// so a later revocation cannot retroactively invalidate an earlier issuance.
func Accept(req Request, bundle Bundle) error {
	return Audit(req, bundle).Err()
}

// Audit returns the detailed independent acceptance result.  req.Witnesses is
// optional; when supplied, every bundle witness must be byte-for-byte
// equivalent (by evidence fingerprint) to one of the supplied raw receipts.
func Audit(req Request, bundle Bundle) Acceptance {
	var out Acceptance
	var problems []Problem
	add := func(id, code string, err error) {
		problems = append(problems, Problem{WitnessID: id, Code: code, Err: err})
	}

	order, err := req.Order.Normalize()
	if err != nil {
		add("", "order", err)
		out.Problems = problems
		return out
	}
	if bundle.OrderDigest.IsZero() || bundle.OrderDigest != order.Digest() {
		add("", "order_digest", fmt.Errorf("%w: bundle order digest does not match order",
			ErrInvalidBundle))
	}
	if bundle.IssuedAt.IsZero() {
		add("", "issued_at", fmt.Errorf("%w: zero issuance time", ErrInvalidBundle))
	}
	asOf := bundle.IssuedAt.UTC()
	if !req.Now.IsZero() && !bundle.IssuedAt.IsZero() && bundle.IssuedAt.After(req.Now) {
		add("", "issued_at", fmt.Errorf("%w: issuance time is in the future",
			ErrInvalidBundle))
	}

	identityInputs := append([]IdentityList(nil), req.Identities...)
	identityInputs = append(identityInputs, bundle.Identities...)
	supplements := append([]IdentitySupplement(nil), req.Supplements...)
	supplements = append(supplements, bundle.Supplements...)
	if req.VerifyIdentity != nil {
		for _, list := range identityInputs {
			if err := req.VerifyIdentity(list); err != nil {
				add("", "identity", fmt.Errorf("%w: identity list %q: %v",
					ErrIdentityConflict, list.Version, err))
			}
		}
	}
	if req.VerifySupplement != nil {
		for _, sup := range supplements {
			if err := req.VerifySupplement(sup); err != nil {
				add("", "identity_supplement", fmt.Errorf("%w: %v",
					ErrIdentityConflict, err))
			}
		}
	}
	ids, err := buildIdentitySet(identityInputs, supplements)
	if err != nil {
		add("", "identity", err)
		out.Problems = problems
		return out
	}

	original := indexOriginalWitnesses(req.Witnesses)
	evidence, duplicates, dedupProblems := dedupeEvidence(bundle.Evidence)
	for _, p := range dedupProblems {
		add(p.WitnessID, p.Code, p.Err)
	}
	out.Duplicates = duplicates

	authSpan := TimeSpan{Segments: []Interval{order.AuthorizationWindow}}
	common := authSpan
	domains := make(map[string]struct{})
	origins := make(map[string]struct{})
	var totalCost int64

	for _, ev := range evidence {
		w := ev.Witness
		if original != nil {
			group, ok := original[w.ID]
			if !ok {
				add(w.ID, "original", fmt.Errorf("%w: witness not present in supplied receipts",
					ErrInvalidBundle))
				continue
			}
			if group.forked {
				add(w.ID, "fork", fmt.Errorf("%w: supplied receipts fork receipt id %q",
					ErrForkedReceipt, w.ID))
				continue
			}
			if _, ok := group.fingerprints[w.Fingerprint()]; !ok {
				add(w.ID, "original",
					fmt.Errorf("%w: bundle witness differs from supplied receipt",
						ErrInvalidBundle))
				continue
			}
		}

		if err := w.Validate(order); err != nil {
			add(w.ID, "witness", err)
			continue
		}
		if req.VerifySignature != nil {
			if err := req.VerifySignature(order, w); err != nil {
				add(w.ID, "signature", err)
				continue
			}
		}
		if req.VerifyProof != nil {
			if err := req.VerifyProof(order, w); err != nil {
				add(w.ID, "proof", err)
				continue
			}
		}
		if req.VerifyIdentity != nil && w.IdentityList != nil {
			if err := req.VerifyIdentity(*w.IdentityList); err != nil {
				add(w.ID, "identity_list", err)
				continue
			}
		}

		options, err := resolveForAudit(ids, w, ev, req.TrustInlineAttribution)
		if err != nil {
			add(w.ID, "identity", err)
			continue
		}
		attr, err := chooseAuditAttribution(w, ev, options)
		if err != nil {
			add(w.ID, "attribution", err)
			continue
		}
		if r, revoked := revocationFor(w, attr, req.Revocations, asOf); revoked {
			add(w.ID, "revoked", fmt.Errorf("%w: %s %q since %s", ErrRevoked, r.Kind,
				r.Subject, r.RevokedAt.UTC()))
			continue
		}
		span := authSpan.Intersect(w.Observed).Clamp(attr.ValidFrom, attr.ValidUntil)
		if span.Empty() {
			add(w.ID, "common_instant",
				fmt.Errorf("%w: selected attribution %q", ErrNoCommonInstant, attr.ID))
			common = TimeSpan{}
			continue
		}
		common = common.Intersect(span)
		domains[attr.Domain] = struct{}{}
		// A route origin contributes at most one vote.  Redundant receipts
		// remain valid evidence but do not increase the origin count.
		origins[attr.RouteOrigin] = struct{}{}
		sum, ok := checkedAdd64(totalCost, w.Cost)
		if !ok {
			add(w.ID, "cost", fmt.Errorf("%w: total cost overflow", ErrInvalidBundle))
			continue
		}
		totalCost = sum
	}

	if len(evidence) == 0 {
		add("", "evidence", ErrNoValidEvidence)
	} else if len(domains) < order.MinDomains {
		add("", "domains", fmt.Errorf("%w: have %d, need %d",
			ErrInsufficientDomains, len(domains), order.MinDomains))
	} else if len(origins) < order.MinDomains {
		add("", "route_origins", fmt.Errorf("%w: have %d distinct origins, need %d",
			ErrRouteOriginConflict, len(origins), order.MinDomains))
	}
	if common.Empty() && len(evidence) > 0 {
		// If an individual witness already reported an empty common instant,
		// avoid a duplicate diagnostic.
		already := false
		for _, p := range problems {
			if p.Code == "common_instant" {
				already = true
				break
			}
		}
		if !already {
			add("", "common_instant", ErrNoCommonInstant)
		}
	}

	out.Valid = len(problems) == 0
	out.Domains = mapKeys(domains)
	out.Origins = mapKeys(origins)
	out.CommonObservation = common
	out.TotalCost = totalCost
	out.Problems = problems
	return out
}

type originalGroup struct {
	digests      map[Digest]struct{}
	fingerprints map[Digest]struct{}
	cores        map[Digest]struct{}
	listDigests  map[Digest]struct{}
	forked       bool
}

func indexOriginalWitnesses(witnesses []Witness) map[string]*originalGroup {
	if len(witnesses) == 0 {
		return nil
	}
	out := make(map[string]*originalGroup)
	for _, w := range witnesses {
		if strings.TrimSpace(w.ID) == "" {
			continue
		}
		g := out[w.ID]
		if g == nil {
			g = &originalGroup{
				digests:      make(map[Digest]struct{}),
				fingerprints: make(map[Digest]struct{}),
				cores:        make(map[Digest]struct{}),
				listDigests:  make(map[Digest]struct{}),
			}
			out[w.ID] = g
		}
		g.digests[w.ProofDigest] = struct{}{}
		g.fingerprints[w.Fingerprint()] = struct{}{}
		g.cores[w.coreFingerprint()] = struct{}{}
		if w.IdentityList != nil {
			g.listDigests[w.IdentityList.ContentDigest()] = struct{}{}
		}
	}
	for _, g := range out {
		if len(g.digests) > 1 || len(g.cores) > 1 || len(g.listDigests) > 1 {
			g.forked = true
		}
	}
	return out
}

type evidenceProblem struct {
	WitnessID string
	Code      string
	Err       error
}

func dedupeEvidence(input []Evidence) ([]Evidence, int, []evidenceProblem) {
	type group struct {
		items        []Evidence
		digests      map[Digest]struct{}
		cores        map[Digest]struct{}
		listDigests  map[Digest]struct{}
		attributions map[string]struct{}
	}
	groups := make(map[string]*group)
	var order []string
	var plain []Evidence
	var problems []evidenceProblem

	for _, ev := range input {
		id := strings.TrimSpace(ev.Witness.ID)
		if id == "" {
			plain = append(plain, ev)
			continue
		}
		g := groups[id]
		if g == nil {
			g = &group{
				digests:      make(map[Digest]struct{}),
				cores:        make(map[Digest]struct{}),
				listDigests:  make(map[Digest]struct{}),
				attributions: make(map[string]struct{}),
			}
			groups[id] = g
			order = append(order, id)
		}
		g.items = append(g.items, ev)
		g.digests[ev.Witness.ProofDigest] = struct{}{}
		g.cores[ev.Witness.coreFingerprint()] = struct{}{}
		if ev.Witness.IdentityList != nil {
			g.listDigests[ev.Witness.IdentityList.ContentDigest()] = struct{}{}
		}
		attributionID := strings.TrimSpace(ev.AttributionID)
		if attributionID == "" {
			attributionID = strings.TrimSpace(ev.Attribution.ID)
		}
		g.attributions[attributionID] = struct{}{}
	}

	var out []Evidence
	duplicates := 0
	sort.Strings(order)
	for _, id := range order {
		g := groups[id]
		if len(g.digests) > 1 {
			problems = append(problems, evidenceProblem{
				WitnessID: id,
				Code:      "fork",
				Err:       fmt.Errorf("%w: different proof digests", ErrForkedReceipt),
			})
			continue
		}
		if len(g.cores) > 1 || len(g.listDigests) > 1 || len(g.attributions) > 1 {
			problems = append(problems, evidenceProblem{
				WitnessID: id,
				Code:      "fork",
				Err:       fmt.Errorf("%w: inconsistent receipt or attribution", ErrForkedReceipt),
			})
			continue
		}
		duplicates += len(g.items) - 1
		chosen := g.items[0]
		for _, item := range g.items[1:] {
			if chosen.Witness.IdentityList == nil && item.Witness.IdentityList != nil {
				chosen = item
			}
		}
		out = append(out, chosen)
	}
	out = append(out, plain...)
	return out, duplicates, problems
}

func resolveForAudit(ids *identitySet, w Witness, ev Evidence, allowInline bool) ([]resolvedAttribution, error) {
	options, err := ids.resolve(w)
	if err == nil || !allowInline {
		return options, err
	}
	if !errors.Is(err, ErrMissingIdentity) || ev.Attribution.ID == "" {
		return nil, err
	}
	// Last-resort compatibility for callers that only retain the signed
	// evidence.  The inline attribution is still checked against the witness's
	// probe, claims and validity; it is not used to override an identity list.
	version := w.IdentityVersion
	if version == "" {
		version = ev.Attribution.ID
	}
	inline := IdentityList{
		Version: version,
		Entries: []Attribution{ev.Attribution},
	}
	ids2, err2 := buildIdentitySet([]IdentityList{inline}, nil)
	if err2 != nil {
		return nil, err2
	}
	return ids2.resolve(w)
}

func chooseAuditAttribution(w Witness, ev Evidence, options []resolvedAttribution) (Attribution, error) {
	id := strings.TrimSpace(ev.AttributionID)
	if id == "" {
		id = strings.TrimSpace(ev.Attribution.ID)
	}
	if id == "" {
		if len(options) == 1 {
			return options[0].Attribution, nil
		}
		return Attribution{}, fmt.Errorf("%w: receipt %q did not choose one attribution",
			ErrAmbiguousIdentity, w.ID)
	}
	for _, opt := range options {
		if opt.Attribution.ID == id {
			return opt.Attribution, nil
		}
	}
	return Attribution{}, fmt.Errorf("%w: receipt %q chose attribution %q which is not resolvable",
		ErrMissingIdentity, w.ID, id)
}
