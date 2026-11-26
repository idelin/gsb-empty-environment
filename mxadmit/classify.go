package mxadmit

import (
	"fmt"
	"sort"
)

type deliveryAnalysis struct {
	Delivery Delivery
	Receipt  *Receipt
	Endpoint string

	Domain        string
	MX            string
	DNSGeneration int64
	KeyRestriction  string
	PathRestriction string

	OriginalSafe bool
	BaseOK       bool
	Pairs        map[[2]string]struct{}
	Reasons      []string
}

// Classify returns one verdict per delivery.  A delivery is classified as
// use-original only when its receipt is valid, its endpoint and domain match,
// and every compatible ordering has an active signed configuration that
// jointly authorizes the complete receipt commitment.
func Classify(b Batch) ([]Verdict, error) {
	m, err := buildModel(b, defaultOrderingLimit)
	if err != nil {
		return nil, err
	}
	return m.classifyAll(), nil
}

func (m *model) classifyAll() []Verdict {
	out := make([]Verdict, 0, len(m.batch.Deliveries))
	for _, d := range m.batch.Deliveries {
		a := m.analyze(d)
		out = append(out, a.verdict())
	}
	return out
}

func (m *model) analyze(d Delivery) deliveryAnalysis {
	a := deliveryAnalysis{
		Delivery: d,
		Receipt:  m.receipts[d.ReceiptID],
	}
	if d.ReceiptID != "" && a.Receipt == nil {
		a.Reasons = append(a.Reasons, fmt.Sprintf("unknown receipt %q", d.ReceiptID))
	}
	if a.Receipt != nil {
		var err error
		a.Endpoint, err = m.resolveEndpoint(d, a.Receipt)
		if err != nil {
			a.Reasons = append(a.Reasons, err.Error())
		}
	} else {
		// Without a receipt, the candidate determines the connection endpoint
		// indirectly through the delivery's own endpoint.
		var err error
		a.Endpoint, err = m.resolveEndpoint(d, nil)
		if err != nil {
			a.Reasons = append(a.Reasons, err.Error())
		}
	}

	a.OriginalSafe = m.originalSafe(&a)
	a.populateBase()
	if !a.BaseOK {
		return a
	}
	m.computeAnalysisPairs(&a)
	return a
}

func (m *model) originalSafe(a *deliveryAnalysis) bool {
	r := a.Receipt
	d := a.Delivery
	if r == nil {
		return false
	}
	domain := d.Domain
	if domain == "" {
		domain = r.Domain
	}
	if domain != r.Domain {
		return false
	}
	if !r.Valid.Contains(d.At) {
		return false
	}
	if r.Endpoint != "" && a.Endpoint != "" && r.Endpoint != a.Endpoint {
		return false
	}
	for wi := range m.worlds {
		cfg := m.authorityAt(&m.worlds[wi], a.Endpoint, domain, d.At)
		if cfg == nil || !cfg.AuthorizesAt(r.Commitment, d.At) {
			return false
		}
	}
	return true
}

func (a *deliveryAnalysis) populateBase() {
	d := a.Delivery
	r := a.Receipt

	var baseDomain, baseMX string
	var baseDNS int64
	baseSet := false
	if r != nil {
		baseDomain, baseMX, baseDNS, baseSet = r.Domain, r.MX, r.DNSGeneration, true
	}

	a.Domain = d.Domain
	if a.Domain == "" {
		a.Domain = baseDomain
	}
	if r != nil && d.Candidate == nil && a.Domain != r.Domain {
		a.Reasons = append(a.Reasons, "a receipt cannot be rebound to another message domain")
		return
	}
	if d.Candidate != nil {
		c := *d.Candidate
		if a.Domain == "" {
			a.Domain = c.Domain
		}
		if c.Domain != "" && a.Domain != c.Domain {
			a.Reasons = append(a.Reasons, "delivery domain does not match candidate domain")
			return
		}
		if c.MX == "" || c.PublicKey == "" {
			a.Reasons = append(a.Reasons, "candidate is missing an MX or public key")
			return
		}
		// A fresh handshake may observe new DNS facts, but those facts still
		// have to be jointly authorized below.  The old receipt's DNS
		// authorization is never used to fill a gap in the new configuration.
		a.MX = c.MX
		a.DNSGeneration = c.DNSGeneration
		baseSet = true
	} else if r != nil {
		a.MX = r.MX
		a.DNSGeneration = r.DNSGeneration
	}

	if a.Domain == "" || a.MX == "" || !baseSet {
		a.Reasons = append(a.Reasons, "no observed commitment (domain, MX and DNS generation are required)")
		return
	}

	a.KeyRestriction = d.ObservedPublicKey
	if a.KeyRestriction == "" && d.Candidate != nil {
		a.KeyRestriction = d.Candidate.PublicKey
	}
	if a.KeyRestriction == "" && r != nil {
		a.KeyRestriction = r.PublicKey
	}

	a.PathRestriction = d.ObservedPathDigest
	if a.PathRestriction == "" && d.Candidate != nil {
		a.PathRestriction = d.Candidate.PathDigest
	}
	// An empty path restriction means that the handshake may present any path
	// jointly authorized by the same configuration as the DNS facts.  The
	// eventual path is still committed below.

	if a.KeyRestriction == "" {
		a.Reasons = append(a.Reasons, "no observed public key and no receipt key to reuse")
		return
	}
	a.BaseOK = true
}

func (m *model) computeAnalysisPairs(a *deliveryAnalysis) {
	raw := m.certainPairs(a.Endpoint, a.Domain, a.MX, a.DNSGeneration, a.Delivery.At)
	pairs := make(map[[2]string]struct{}, len(raw))
	for pair := range raw {
		if a.KeyRestriction != "" && pair[0] != a.KeyRestriction {
			continue
		}
		if a.PathRestriction != "" && pair[1] != a.PathRestriction {
			continue
		}
		if pair[1] == "" {
			continue
		}
		pairs[pair] = struct{}{}
	}
	a.Pairs = pairs
}

func (a *deliveryAnalysis) verdict() Verdict {
	v := Verdict{
		DeliveryID: a.Delivery.ID,
		Endpoint:   a.Endpoint,
	}
	reasons := append([]string(nil), a.Reasons...)
	if a.Receipt != nil && !a.Receipt.Valid.Contains(a.Delivery.At) {
		reasons = append(reasons, fmt.Sprintf("original receipt expired at tick %d", int64(a.Receipt.Valid.To)))
	}
	if a.Receipt != nil && a.Delivery.Domain != "" && a.Delivery.Domain != a.Receipt.Domain {
		reasons = append(reasons, "message domain does not match receipt domain")
	}
	if a.Receipt != nil && a.Receipt.Endpoint != "" && a.Endpoint != "" && a.Receipt.Endpoint != a.Endpoint {
		reasons = append(reasons, "receipt endpoint does not match delivery endpoint")
	}

	if a.OriginalSafe {
		v.Action = ActionUseOriginal
		v.ReceiptID = a.Receipt.ID
		v.Commitment = a.Receipt.Commitment
		v.Reasons = reasons
		return v
	}

	if a.BaseOK && len(a.Pairs) > 0 {
		pair := choosePair(a.Pairs, a.KeyRestriction, a.PathRestriction)
		v.Action = ActionRehandshake
		v.Commitment = Commitment{
			Domain:        a.Domain,
			MX:            a.MX,
			PublicKey:     pair[0],
			PathDigest:    pair[1],
			DNSGeneration: a.DNSGeneration,
		}
		v.Reasons = reasons
		return v
	}

	if a.BaseOK {
		reasons = append(reasons, "no single signed configuration jointly authorizes MX, public key, path and DNS generation in every compatible ordering")
	}
	v.Action = ActionWait
	v.Reasons = reasons
	if a.Receipt != nil {
		v.ReceiptID = a.Receipt.ID
	}
	return v
}

func choosePair(pairs map[[2]string]struct{}, preferredKey, preferredPath string) [2]string {
	type pair = [2]string
	var all []pair
	for p := range pairs {
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i][0] != all[j][0] {
			return all[i][0] < all[j][0]
		}
		return all[i][1] < all[j][1]
	})
	for _, p := range all {
		if preferredKey != "" && p[0] != preferredKey {
			continue
		}
		if preferredPath != "" && p[1] != preferredPath {
			continue
		}
		return p
	}
	return all[0]
}
