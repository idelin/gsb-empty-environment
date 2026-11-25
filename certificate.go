package handoff

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"strings"
)

// Violation is one concrete address/egress/interleaving counterexample.
type Violation struct {
	Kind        string
	Trace       []EventID
	StateIndex  int
	Egress      EgressID
	Class       string
	Address     string
	Outcome     Outcome
	Tenant      TenantID
	Prefix      string
	Length      int
	RawConflict bool
	RawTenants  []TenantID
	Critical    bool
	Message     string
}

// Certificate is a replayable exhaustive safety proof for a small domain.
type Certificate struct {
	PlanID               string
	ProtocolDigest       Digest
	Policy               Policy
	RawRequired          bool
	ClassCount           int
	InterleavingCount    int
	VisitedStates        int64
	CheckCount           int64
	BlockedRawMismatches int64
	ReceiptsVerified     int
	Root                 Digest
	Safe                 bool
	CriticalReachable    bool
	ViolationCount       int
	Violations           []Violation
	RequiredRecoveryAcks []AckRequirement
}

// VerifyOptions controls independent certificate replay.
type VerifyOptions struct {
	LimitTraces          int
	MaxViolations        int
	Policy               *Policy
	RequireRawAuthorized bool
	Keys                 map[EgressID]ed25519.PublicKey
	Receipts             []SignedReceipt
}

// IssueCertificate exhaustively verifies protocol and returns its certificate.
func IssueCertificate(protocol *Protocol, opts VerifyOptions) (*Certificate, error) {
	if protocol == nil {
		return nil, fmt.Errorf("%w: nil protocol", ErrInvalidSpec)
	}
	if err := protocol.Spec.validate(); err != nil {
		return nil, err
	}
	policy := protocol.Spec.Policy
	if opts.Policy != nil {
		policy = *opts.Policy
	}
	maxViolations := opts.MaxViolations
	if maxViolations <= 0 {
		maxViolations = 100
	}

	classes, err := protocol.equivalenceClasses()
	if err != nil {
		return nil, err
	}
	criticalSet := make(map[string]bool)
	for _, cl := range classes {
		for _, crit := range protocol.Spec.Critical {
			if PrefixesOverlap(cl, crit) {
				criticalSet[cl.String()] = true
				break
			}
		}
	}

	cert := &Certificate{
		PlanID:            protocol.Spec.PlanID,
		ProtocolDigest:    protocol.Digest(),
		Policy:            policy,
		RawRequired:       opts.RequireRawAuthorized,
		ClassCount:        len(classes),
		Safe:              true,
		CriticalReachable: true,
	}

	receiptCount, err := verifyCertificateReceipts(opts.Receipts, opts.Keys)
	if err != nil {
		return nil, err
	}
	cert.ReceiptsVerified = receiptCount

	pred := protocol.predecessors()
	root := protocol.initialState()
	fingerprints := make([]string, 0, 1024)
	recoverySet := make(map[string]AckRequirement)
	limit := opts.LimitTraces
	if limit <= 0 {
		limit = 100000
	}
	traceLimitReached := false

	var walk func(*simState)
	walk = func(st *simState) {
		cert.VisitedStates++
		stateIndex := len(st.path)
		for _, e := range protocol.Spec.Egresses {
			view := st.views[e]
			for _, class := range classes {
				addr := class.Addr()
				decision := Select(view, addr, st.authz, policy)
				cert.CheckCount++
				critical := criticalSet[class.String()]
				rawAuthorized := true
				rawUnique := false
				if decision.Raw.Found && !decision.Raw.Conflict && len(decision.Raw.Tenants) == 1 {
					rawUnique = true
					rawAuthorized = st.authz.Authorized(decision.Raw.Tenants[0], addr)
				}
				rawMismatch := decision.Raw.Found && (decision.Raw.Conflict || (rawUnique && !rawAuthorized))
				if rawMismatch && !decision.Forward() {
					cert.BlockedRawMismatches++
				}

				var violation *Violation
				if decision.Forward() && !st.authz.Authorized(decision.Tenant, addr) {
					violation = &Violation{
						Kind: "unauthorized_forward", Critical: critical,
						Message: "admission returned Forward for a tenant without actual authorization",
					}
					cert.Safe = false
				}
				if opts.RequireRawAuthorized && decision.Raw.Found && rawUnique && !rawAuthorized {
					violation = &Violation{
						Kind: "raw_selection_unauthorized", Critical: critical,
						Message: "ordinary longest-prefix winner is unauthorized; a non-admission-aware egress would misdeliver",
					}
					cert.Safe = false
				}
				if opts.RequireRawAuthorized && decision.Raw.Conflict {
					violation = &Violation{
						Kind: "raw_equal_length_conflict", Critical: critical,
						Message: "equal-length prefixes identify different tenants",
					}
					// Conflict is not misdelivery, but strict raw mode treats it
					// as failure to provide an unambiguous selection.
					cert.Safe = false
				}
				if critical && !decision.Forward() {
					violation = &Violation{
						Kind: "critical_prefix_not_reachable", Critical: true,
						Message: "critical equivalence class does not have one authorized forward decision",
					}
					cert.CriticalReachable = false
				}
				if violation != nil {
					fillViolation(violation, st, stateIndex, e, class, decision)
					cert.ViolationCount++
					if len(cert.Violations) < maxViolations {
						cert.Violations = append(cert.Violations, *violation)
					}
					for _, need := range recoveryNeedsForDecision(protocol, st.path, e, class, st, decision) {
						mergeAckNeed(recoverySet, need, class.String())
					}
				}

				fingerprints = append(fingerprints, fingerprintCheck(st.path, stateIndex, e, class, decision, critical, violation == nil))
			}
		}

		next := protocol.available(st, pred)
		if len(next) == 0 {
			cert.InterleavingCount++
			if cert.InterleavingCount > limit {
				traceLimitReached = true
				return
			}
			return
		}
		for _, id := range next {
			if traceLimitReached {
				return
			}
			ev := protocol.Events[id]
			ns := st.cloneFor(id)
			protocol.apply(ns, ev)
			walk(ns)
		}
	}
	walk(root)
	if traceLimitReached {
		return cert, fmt.Errorf("%w: more than %d traces", ErrTracesExceeded, limit)
	}

	sort.Strings(fingerprints)
	h := sha256.New()
	for _, f := range fingerprints {
		h.Write([]byte(f))
		h.Write([]byte{'\n'})
	}
	copy(cert.Root[:], h.Sum(nil))
	cert.RequiredRecoveryAcks = sortedAckNeeds(recoverySet)
	return cert, nil
}

// VerifyCertificate independently replays protocol and compares all claimed
// certificate commitments. A returned certificate is the fresh replay result.
func VerifyCertificate(cert *Certificate, protocol *Protocol, opts VerifyOptions) (*Certificate, error) {
	if cert == nil {
		return nil, fmt.Errorf("%w: nil certificate", ErrCertificateMismatch)
	}
	fresh, err := IssueCertificate(protocol, opts)
	if err != nil && !errors.Is(err, ErrTracesExceeded) {
		return fresh, err
	}
	if fresh.ProtocolDigest != cert.ProtocolDigest {
		return fresh, fmt.Errorf("%w: protocol digest", ErrCertificateMismatch)
	}
	if fresh.Root != cert.Root ||
		fresh.ClassCount != cert.ClassCount ||
		fresh.InterleavingCount != cert.InterleavingCount ||
		fresh.VisitedStates != cert.VisitedStates ||
		fresh.CheckCount != cert.CheckCount ||
		fresh.BlockedRawMismatches != cert.BlockedRawMismatches ||
		fresh.ReceiptsVerified != cert.ReceiptsVerified ||
		fresh.Safe != cert.Safe ||
		fresh.CriticalReachable != cert.CriticalReachable ||
		fresh.ViolationCount != cert.ViolationCount ||
		!reflect.DeepEqual(fresh.RequiredRecoveryAcks, cert.RequiredRecoveryAcks) {
		return fresh, fmt.Errorf("%w: replay root/count/safety mismatch", ErrCertificateMismatch)
	}
	if len(fresh.Violations) != len(cert.Violations) {
		return fresh, fmt.Errorf("%w: embedded violation witness count", ErrCertificateMismatch)
	}
	for i := range fresh.Violations {
		if !violationEqual(fresh.Violations[i], cert.Violations[i]) {
			return fresh, fmt.Errorf("%w: violation witness %d differs", ErrCertificateMismatch, i)
		}
	}
	return fresh, err
}

// RequireSafe returns ErrUnsafePlan if the certificate contains any safety or
// critical-reachability failure.
func (c *Certificate) RequireSafe() error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", ErrUnsafePlan)
	}
	if !c.Safe {
		return fmt.Errorf("%w: %d safety violations", ErrUnsafePlan, c.ViolationCount)
	}
	if !c.CriticalReachable {
		return fmt.Errorf("%w: %d critical reachability violations", ErrUnsafePlan, c.ViolationCount)
	}
	return nil
}

func verifyCertificateReceipts(receipts []SignedReceipt, keys map[EgressID]ed25519.PublicKey) (int, error) {
	if len(receipts) == 0 {
		return 0, nil
	}
	book := NewReceiptBook(keys)
	verified := 0
	for _, sr := range receipts {
		// Replays are acceptable as evidence in a certificate bundle; every
		// unique signature still had to verify against the trusted key.
		key, ok := keys[sr.Receipt.Egress]
		if !ok {
			return verified, fmt.Errorf("%w: %s", ErrUnknownEgress, sr.Receipt.Egress)
		}
		if err := sr.Verify(key); err != nil {
			return verified, err
		}
		_, err := book.Observe(sr)
		if err != nil && !errors.Is(err, ErrReplay) {
			return verified, err
		}
		verified++
	}
	return verified, nil
}

func (p *Protocol) equivalenceClasses() ([]netip.Prefix, error) {
	var relevant []netip.Prefix
	add := func(prefixes ...netip.Prefix) {
		for _, prefix := range prefixes {
			if prefix.IsValid() && PrefixesContain(p.Spec.Domain, prefix) {
				relevant = append(relevant, prefix)
			}
		}
	}
	add(p.Spec.Transfer...)
	add(p.Spec.Critical...)
	for _, claims := range p.Spec.InitialViews {
		for _, c := range claims {
			add(c.Prefix)
			add(c.Exclusions...)
		}
	}
	for _, c := range p.Spec.InitialClaims {
		add(c.Prefix)
		add(c.Exclusions...)
	}
	for _, st := range p.Statements {
		for _, c := range st.Announces {
			add(c.Prefix)
			add(c.Exclusions...)
		}
	}
	if p.Spec.InitialAuthz != nil {
		for _, tenant := range p.Spec.InitialAuthz.Tenants() {
			add(p.Spec.InitialAuthz.GrantedPrefixes(tenant)...)
		}
	}
	for _, ev := range p.Events {
		for _, op := range ev.Ops {
			add(op.Prefix)
		}
	}
	return EquivalenceClasses(p.Spec.Domain, relevant...)
}

func fillViolation(v *Violation, st *simState, stateIndex int, e EgressID, class netip.Prefix, d Decision) {
	v.Trace = append([]EventID(nil), st.path...)
	v.StateIndex = stateIndex
	v.Egress = e
	v.Class = class.String()
	v.Address = d.Address.String()
	v.Outcome = d.Outcome
	v.Tenant = d.Tenant
	if d.Prefix.IsValid() {
		v.Prefix = d.Prefix.String()
	}
	v.Length = d.Length
	v.RawConflict = d.Raw.Conflict
	v.RawTenants = append([]TenantID(nil), d.Raw.Tenants...)
}

func fingerprintCheck(trace []EventID, idx int, e EgressID, class netip.Prefix, d Decision, critical, ok bool) string {
	parts := []string{
		"handoff-check-v1",
		eventPath(trace),
		fmt.Sprintf("%d", idx),
		string(e),
		class.String(),
		string(d.Outcome),
		string(d.Tenant),
	}
	if d.Prefix.IsValid() {
		parts = append(parts, d.Prefix.String())
	} else {
		parts = append(parts, "-")
	}
	parts = append(parts,
		fmt.Sprintf("%d", d.Length),
		fmt.Sprintf("%t", critical),
		fmt.Sprintf("%t", ok),
		fmt.Sprintf("%t", d.Raw.Conflict),
		strings.Join(tenantStrings(d.Raw.Tenants), ","),
	)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func eventPath(path []EventID) string {
	parts := make([]string, len(path))
	for i, id := range path {
		parts[i] = string(id)
	}
	return strings.Join(parts, ">")
}

func tenantStrings(ts []TenantID) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

func violationEqual(a, b Violation) bool {
	if a.Kind != b.Kind || a.StateIndex != b.StateIndex || a.Egress != b.Egress ||
		a.Class != b.Class || a.Address != b.Address || a.Outcome != b.Outcome ||
		a.Tenant != b.Tenant || a.Prefix != b.Prefix || a.Length != b.Length ||
		a.Critical != b.Critical || a.RawConflict != b.RawConflict {
		return false
	}
	if len(a.Trace) != len(b.Trace) {
		return false
	}
	for i := range a.Trace {
		if a.Trace[i] != b.Trace[i] {
			return false
		}
	}
	if len(a.RawTenants) != len(b.RawTenants) {
		return false
	}
	for i := range a.RawTenants {
		if a.RawTenants[i] != b.RawTenants[i] {
			return false
		}
	}
	return true
}

type protocolDigestDTO struct {
	PlanID     string   `json:"plan_id"`
	Domain     string   `json:"domain"`
	Egresses   []string `json:"egresses"`
	OldTenant  string   `json:"old_tenant"`
	NewTenant  string   `json:"new_tenant"`
	Transfer   []string `json:"transfer"`
	Critical   []string `json:"critical"`
	Window     int      `json:"window"`
	Policy     Policy   `json:"policy"`
	Statements []string `json:"statements"`
	Events     []string `json:"events"`
	Precedence []string `json:"precedence"`
}

// Digest returns a deterministic commitment to protocol structure.
func (p *Protocol) Digest() Digest {
	dto := protocolDigestDTO{
		PlanID:    p.Spec.PlanID,
		Domain:    p.Spec.Domain.String(),
		OldTenant: string(p.Spec.OldTenant),
		NewTenant: string(p.Spec.NewTenant),
		Window:    p.Spec.Window,
		Policy:    p.Spec.Policy,
	}
	for _, e := range p.Spec.Egresses {
		dto.Egresses = append(dto.Egresses, string(e))
	}
	for _, x := range p.Spec.Transfer {
		dto.Transfer = append(dto.Transfer, x.String())
	}
	for _, x := range p.Spec.Critical {
		dto.Critical = append(dto.Critical, x.String())
	}
	ids := make([]string, 0, len(p.Statements))
	for id := range p.Statements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		dto.Statements = append(dto.Statements, id+":"+p.Statements[id].Digest().String())
	}
	for _, id := range p.EventOrder {
		ev := p.Events[id]
		dto.Events = append(dto.Events, fmt.Sprintf("%s/%s/%s/%s/%d", ev.ID, ev.Kind, ev.Egress, ev.StatementID, ev.Seq))
	}
	for _, edge := range p.Precedence {
		dto.Precedence = append(dto.Precedence, string(edge[0])+"<"+string(edge[1]))
	}
	sort.Strings(dto.Precedence)
	b, _ := json.Marshal(dto)
	return sha256.Sum256(b)
}
