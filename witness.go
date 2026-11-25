package evidence

import (
	"fmt"
	netip "net/netip"
	"sort"
	"strings"
)

// Witness is one signed receipt returned by a probe.  The receipt binds the
// order through OrderDigest and reports the management domain/route origin it
// claims, the observation span, the address actually connected to, and the
// digest of the returned proof.
//
// IdentityList may carry a versioned identity list supplied with the receipt.
// AttributionID pins the receipt to one attribution.  If it is empty, the
// selection search may choose exactly one authenticated attribution from the
// list.
type Witness struct {
	ID                 string         `json:"id"`
	ProbeID            string         `json:"probe_id"`
	IdentityVersion    string         `json:"identity_version,omitempty"`
	IdentityList       *IdentityList  `json:"identity_list,omitempty"`
	IdentityListDigest Digest         `json:"identity_list_digest,omitempty"`
	IdentityProof      Digest         `json:"identity_proof,omitempty"`
	AttributionID      string         `json:"attribution_id,omitempty"`
	Domain             string         `json:"domain,omitempty"`
	RouteOrigin        string         `json:"route_origin,omitempty"`
	RouteProof         Digest         `json:"route_proof,omitempty"`
	Observed           TimeSpan       `json:"observed"`
	ConnectedAddr      netip.AddrPort `json:"connected_addr"`
	ProofDigest        Digest         `json:"proof_digest"`
	OrderDigest        Digest         `json:"order_digest"`
	Cost               int64          `json:"cost"`
	KeyID              string         `json:"key_id,omitempty"`
	Signature          []byte         `json:"signature,omitempty"`
}

// Validate performs receipt-local and order-binding checks.  It does not
// resolve identity attributions; that is done by the identity resolver.
func (w Witness) Validate(order Order) error {
	normalizedOrder, err := order.Normalize()
	if err != nil {
		return err
	}
	order = normalizedOrder
	if !validIdentifier(w.ID) {
		return fmt.Errorf("%w: invalid receipt id %q", ErrInvalidWitness, w.ID)
	}
	if !validIdentifier(w.ProbeID) {
		return fmt.Errorf("%w: receipt %q has invalid probe id", ErrInvalidWitness, w.ID)
	}
	if w.IdentityVersion != "" && (strings.TrimSpace(w.IdentityVersion) != w.IdentityVersion ||
		strings.TrimSpace(w.IdentityVersion) == "") {
		return fmt.Errorf("%w: receipt %q has invalid identity version", ErrInvalidWitness, w.ID)
	}
	if w.AttributionID != "" && !validIdentifier(w.AttributionID) {
		return fmt.Errorf("%w: receipt %q has invalid attribution id", ErrInvalidWitness, w.ID)
	}
	if w.Domain != "" && NormalizeDomain(w.Domain) == "" {
		return fmt.Errorf("%w: receipt %q has empty domain", ErrInvalidWitness, w.ID)
	}
	if w.RouteOrigin != "" && NormalizeRouteOrigin(w.RouteOrigin) == "" {
		return fmt.Errorf("%w: receipt %q has empty route origin", ErrInvalidWitness, w.ID)
	}
	if _, err := w.Observed.Normalize(); err != nil || w.Observed.Empty() {
		return fmt.Errorf("%w: receipt %q has invalid observation span: %v",
			ErrInvalidWitness, w.ID, err)
	}
	if !w.ConnectedAddr.IsValid() || w.ConnectedAddr.Port() == 0 {
		return fmt.Errorf("%w: receipt %q has invalid connected address", ErrInvalidWitness, w.ID)
	}
	connAddr, err := NormalizePublicAddr(w.ConnectedAddr.Addr())
	if err != nil {
		return fmt.Errorf("%w: receipt %q connected address: %v", ErrInvalidWitness, w.ID, err)
	}
	if w.ProofDigest.IsZero() {
		return fmt.Errorf("%w: receipt %q has zero proof digest", ErrInvalidWitness, w.ID)
	}
	if w.OrderDigest.IsZero() {
		return fmt.Errorf("%w: receipt %q has zero order digest", ErrInvalidWitness, w.ID)
	}
	if w.Cost < 0 {
		return fmt.Errorf("%w: receipt %q has negative cost", ErrInvalidWitness, w.ID)
	}
	if w.IdentityList != nil {
		if err := w.IdentityList.Validate(); err != nil {
			return fmt.Errorf("%w: receipt %q identity list: %v", ErrInvalidWitness, w.ID, err)
		}
	}

	orderDigest := order.Digest()
	if w.OrderDigest != orderDigest {
		return fmt.Errorf("%w: receipt %q order digest mismatch", ErrOrderMismatch, w.ID)
	}
	if connAddr != order.IP {
		return fmt.Errorf("%w: receipt %q connected to %s, order target is %s",
			ErrAddressMismatch, w.ID, connAddr, order.IP)
	}
	if w.ConnectedAddr.Port() != order.Port {
		return fmt.Errorf("%w: receipt %q connected to port %d, order port is %d",
			ErrAddressMismatch, w.ID, w.ConnectedAddr.Port(), order.Port)
	}
	return nil
}

// Fingerprint returns a deterministic digest of the receipt's evidence
// content.  Signatures are excluded because ECDSA/RSA-PSS signatures are not
// deterministic.  An embedded identity list is represented by its content
// digest so a verifier can bind the supplied list to the raw receipt; external
// supplements are kept outside the fingerprint.
func (w Witness) Fingerprint() Digest {
	return w.fingerprint(true)
}

func (w Witness) coreFingerprint() Digest {
	return w.fingerprint(false)
}

func (w Witness) fingerprint(includeEmbeddedIdentity bool) Digest {
	observed, err := w.Observed.Normalize()
	if err != nil {
		observed = TimeSpan{}
	}
	c := newCanonical("ip-cert-witness/v1")
	c.str(w.ID)
	c.str(w.ProbeID)
	c.str(strings.TrimSpace(w.IdentityVersion))
	c.digestMaybe(w.IdentityListDigest)
	if includeEmbeddedIdentity {
		if w.IdentityList != nil {
			c.boolean(true)
			c.digest(w.IdentityList.ContentDigest())
		} else {
			c.boolean(false)
		}
	}
	c.digestMaybe(w.IdentityProof)
	c.str(strings.TrimSpace(w.AttributionID))
	c.str(NormalizeDomain(w.Domain))
	c.str(NormalizeRouteOrigin(w.RouteOrigin))
	c.digestMaybe(w.RouteProof)
	c.span(observed)
	connected := w.ConnectedAddr.String()
	if addr, err := NormalizePublicAddr(w.ConnectedAddr.Addr()); err == nil {
		connected = netip.AddrPortFrom(addr, w.ConnectedAddr.Port()).String()
	}
	c.str(connected)
	c.digest(w.ProofDigest)
	c.digest(w.OrderDigest)
	c.i64(w.Cost)
	c.str(w.KeyID)
	return c.sum()
}

// ForkKind classifies receipt fork/conflict handling.
type ForkKind string

const (
	ForkDifferentProofDigest ForkKind = "different_proof_digest"
	ForkInconsistentReceipt  ForkKind = "inconsistent_receipt"
)

// Fork records a receipt identifier that was isolated because it had more than
// one incompatible version.  All receipts with a forked id are excluded from
// selection.
type Fork struct {
	WitnessID    string   `json:"witness_id"`
	Kind         ForkKind `json:"kind"`
	ProofDigests []Digest `json:"proof_digests,omitempty"`
	Detail       string   `json:"detail,omitempty"`
}

type dedupResult struct {
	unique     []Witness
	duplicates int
	forks      []Fork
	forked     map[string]struct{}
}

func dedupeWitnesses(input []Witness) dedupResult {
	type group struct {
		items       []Witness
		digests     map[Digest]struct{}
		cores       map[Digest]struct{}
		listDigests map[Digest]struct{}
	}
	groups := make(map[string]*group)
	var order []string
	result := dedupResult{forked: make(map[string]struct{})}

	for _, w := range input {
		if strings.TrimSpace(w.ID) == "" {
			// Invalid receipts are rejected later; keeping them untrusted here
			// avoids merging malformed ids.
			result.unique = append(result.unique, w)
			continue
		}
		g, ok := groups[w.ID]
		if !ok {
			g = &group{
				digests:     make(map[Digest]struct{}),
				cores:       make(map[Digest]struct{}),
				listDigests: make(map[Digest]struct{}),
			}
			groups[w.ID] = g
			order = append(order, w.ID)
		}
		g.items = append(g.items, w)
		g.digests[w.ProofDigest] = struct{}{}
		g.cores[w.coreFingerprint()] = struct{}{}
		if w.IdentityList != nil {
			g.listDigests[w.IdentityList.ContentDigest()] = struct{}{}
		}
	}

	sort.Strings(order)
	for _, id := range order {
		g := groups[id]
		if len(g.digests) > 1 {
			digests := make([]Digest, 0, len(g.digests))
			for d := range g.digests {
				digests = append(digests, d)
			}
			sort.Slice(digests, func(i, j int) bool { return digests[i].Hex() < digests[j].Hex() })
			result.forks = append(result.forks, Fork{
				WitnessID:    id,
				Kind:         ForkDifferentProofDigest,
				ProofDigests: digests,
				Detail:       "same receipt id with different proof digests",
			})
			result.forked[id] = struct{}{}
			continue
		}
		if len(g.cores) > 1 || len(g.listDigests) > 1 {
			result.forks = append(result.forks, Fork{
				WitnessID: id,
				Kind:      ForkInconsistentReceipt,
				Detail:    "same receipt id and proof digest with inconsistent fields",
			})
			result.forked[id] = struct{}{}
			continue
		}
		result.duplicates += len(g.items) - 1
		// Prefer a duplicate that carries an embedded identity list or
		// signature; the evidence fingerprint is identical in all other
		// respects.
		chosen := g.items[0]
		for _, item := range g.items[1:] {
			if chosen.IdentityList == nil && item.IdentityList != nil {
				chosen = item
				continue
			}
			if len(chosen.Signature) == 0 && len(item.Signature) != 0 {
				chosen = item
			}
		}
		result.unique = append(result.unique, chosen)
	}
	sort.Slice(result.unique, func(i, j int) bool {
		if result.unique[i].ID != result.unique[j].ID {
			return result.unique[i].ID < result.unique[j].ID
		}
		return result.unique[i].Fingerprint().Hex() < result.unique[j].Fingerprint().Hex()
	})
	return result
}
