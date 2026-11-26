package mailauth

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	cvNone = "none"
	cvPass = "pass"
	cvFail = "fail"
)

type arcSet struct {
	instance int
	aar      *Header
	ams      *Header
	as       *Header
}

// verifyARC validates the complete RFC 8617 ARC chain when one exists.
func (v *Verifier) verifyARC(m *Message, verdict *MessageVerdict, now time.Time) {
	sets, unknown := collectARCSets(m.Headers)
	if len(sets) == 0 && len(unknown) == 0 {
		return
	}

	arc := &ARCResult{Present: true, Hops: make(map[int]*ARCHopResult)}
	verdict.ARC = arc
	// Optimistically valid; any structural, state-machine or cryptographic
	// failure below flips it back to false.
	arc.Valid = true

	for _, h := range unknown {
		arc.addFinding(newFinding(FindingARCUnknownField, SeverityHigh, "message",
			fmt.Sprintf("ARC field %q is missing an instance suffix", h.Name)))
	}

	instances := make([]int, 0, len(sets))
	for inst := range sets {
		instances = append(instances, inst)
	}
	sort.Ints(instances)
	arc.Instances = instances

	v.checkARCSequence(arc, instances)

	// Chain validation proceeds in instance order; a fail cv immediately
	// invalidates every later hop.
	chainFailed := false
	for _, inst := range instances {
		set := sets[inst]
		hop := &ARCHopResult{Instance: inst}
		arc.Hops[inst] = hop
		location := fmt.Sprintf("arc[%d]", inst)

		if set.aar != nil {
			hop.AARPresent = true
		} else {
			hop.addFinding(newFinding(FindingARCMissingSet, SeverityHigh, location,
				"ARC-Authentication-Results header is missing for this instance"))
		}
		if set.ams == nil {
			hop.addFinding(newFinding(FindingARCMissingSet, SeverityHigh, location,
				"ARC-Message-Signature header is missing for this instance"))
		}
		if set.as == nil {
			hop.addFinding(newFinding(FindingARCMissingSet, SeverityHigh, location,
				"ARC-Seal header is missing for this instance"))
		}

		cv, cvOK := v.checkARCSealCV(arc, hop, set, inst, location)
		hop.CV = cv
		if chainFailed {
			arc.addFinding(newFinding(FindingARCCVFail, SeverityCritical, location,
				fmt.Sprintf("instance %d follows an earlier cv=fail hop; the chain is void", inst)))
			arc.Valid = false
		}

		if set.ams != nil {
			v.verifyARCAMS(m, hop, set.ams, inst, now, location)
		}
		if set.as != nil && cvOK {
			v.verifyARCAS(m, hop, set.as, inst, now, location)
		}

		// A cv=pass assertion must match actual cryptographic validation; a
		// seal that claims success while its AMS/AS fails is a forgery signal.
		if cv == cvPass && (!hop.AMSValid || (set.as != nil && !hop.SealValid)) {
			arc.Valid = false
			hop.addFinding(newFinding(FindingARCCVMismatch, SeverityCritical, location,
				fmt.Sprintf("instance %d claims cv=pass but signature validation failed", inst)))
		}

		if cv == cvFail {
			chainFailed = true
			arc.Valid = false
			arc.addFinding(newFinding(FindingARCCVFail, SeverityCritical, location,
				fmt.Sprintf("instance %d declares cv=fail; chain is void from this hop on", inst)))
		}
	}

	if arc.hasInvalidFinding() {
		arc.Valid = false
	}

	// A chain only authenticates when the final seal declares cv=pass and every
	// cryptographic check succeeded.
	if arc.Valid {
		last := instances[len(instances)-1]
		if arc.Hops[last].CV != cvPass {
			arc.Valid = false
			arc.addFinding(newFinding(FindingARCCVIllegal, SeverityHigh,
				fmt.Sprintf("arc[%d]", last),
				fmt.Sprintf("final ARC instance declares cv=%q instead of pass", arc.Hops[last].CV)))
		}
	}
}

// checkARCSequence enforces i=1..N continuity, uniqueness and the 50 hop cap.
func (v *Verifier) checkARCSequence(arc *ARCResult, instances []int) {
	limit := v.policy.MaxARCHops
	if limit <= 0 {
		limit = 50
	}

	if len(instances) > limit {
		arc.Valid = false
		arc.addFinding(newFinding(FindingARCHopLimit, SeverityCritical, "message",
			fmt.Sprintf("ARC chain has %d hops, exceeding the limit of %d",
				len(instances), limit)))
	}

	seen := make(map[int]bool)
	for _, inst := range instances {
		if seen[inst] {
			arc.Valid = false
			arc.addFinding(newFinding(FindingARCInstanceSequence, SeverityCritical,
				fmt.Sprintf("arc[%d]", inst), "duplicate ARC instance number"))
		}
		seen[inst] = true
		if inst < 1 {
			arc.Valid = false
			arc.addFinding(newFinding(FindingARCInstanceSequence, SeverityCritical,
				fmt.Sprintf("arc[%d]", inst), "ARC instance numbers must start at 1"))
		}
	}

	want := 1
	for _, inst := range instances {
		if inst != want {
			arc.Valid = false
			arc.addFinding(newFinding(FindingARCInstanceSequence, SeverityCritical,
				fmt.Sprintf("arc[%d]", inst),
				fmt.Sprintf("ARC instance sequence broken: expected i=%d, got i=%d", want, inst)))
			break
		}
		want++
	}
}

// checkARCSealCV validates the cv= state machine: instance 1 must be none,
// later instances may only be pass or fail, and fail ends the chain.
func (v *Verifier) checkARCSealCV(arc *ARCResult, hop *ARCHopResult,
	set *arcSet, inst int, location string) (string, bool) {

	if set.as == nil {
		return "", false
	}
	tags, err := parseSignature(set.as.Value)
	if err != nil {
		hop.addFinding(newFinding(FindingMalformedSignature, SeverityHigh, location,
			"ARC-Seal tag list is malformed: "+err.Error()))
		arc.Valid = false
		return "", false
	}
	cv := strings.ToLower(strings.TrimSpace(tags.cv))
	hop.CV = cv

	if inst == 1 {
		if cv != cvNone {
			arc.Valid = false
			hop.addFinding(newFinding(FindingARCInitialCV, SeverityCritical, location,
				fmt.Sprintf("first ARC instance must declare cv=none, got cv=%q", cv)))
			return cv, false
		}
		return cv, true
	}

	if cv != cvPass && cv != cvFail {
		arc.Valid = false
		hop.addFinding(newFinding(FindingARCCVIllegal, SeverityCritical, location,
			fmt.Sprintf("instance %d has illegal cv=%q (only pass or fail allowed)", inst, cv)))
		return cv, false
	}
	return cv, true
}

// verifyARCAMS verifies an ARC-Message-Signature with the header scope defined
// by RFC 8617 section 5: regular headers plus ARC sets up to this instance.
func (v *Verifier) verifyARCAMS(m *Message, hop *ARCHopResult,
	sigHeader *Header, inst int, now time.Time, location string) {

	tags, err := parseSignature(sigHeader.Value)
	if err != nil {
		hop.addFinding(newFinding(FindingMalformedSignature, SeverityHigh, location,
			"ARC-Message-Signature tag list is malformed: "+err.Error()))
		return
	}
	if !tags.hasInstance || tags.instance != inst {
		hop.addFinding(newFinding(FindingARCInstanceSequence, SeverityHigh, location,
			fmt.Sprintf("AMS i=%d does not match header instance %d", tags.instance, inst)))
	}

	scoped := m.scopedHeaders(inst, false)
	core := v.verifyCore(scoped, m.Body, *sigHeader, sigHeader.Name, tags, modeAMS, now, location)
	hop.AMSValid = core.valid
	hop.Findings = append(hop.Findings, core.findings...)
	if !core.valid {
		hop.addFinding(newFinding(FindingARCMessageSigInvalid, SeverityCritical, location,
			"ARC-Message-Signature verification failed"))
	}
}

// verifyARCAS verifies an ARC-Seal. Seals authenticate header sets rather than
// the message body, so body hashing is skipped (modeAS).
func (v *Verifier) verifyARCAS(m *Message, hop *ARCHopResult,
	sigHeader *Header, inst int, now time.Time, location string) {

	tags, err := parseSignature(sigHeader.Value)
	if err != nil {
		hop.addFinding(newFinding(FindingMalformedSignature, SeverityHigh, location,
			"ARC-Seal tag list is malformed: "+err.Error()))
		return
	}
	if !tags.hasInstance || tags.instance != inst {
		hop.addFinding(newFinding(FindingARCInstanceSequence, SeverityHigh, location,
			fmt.Sprintf("AS i=%d does not match header instance %d", tags.instance, inst)))
	}

	scoped := m.scopedHeaders(inst, true)
	core := v.verifyCore(scoped, m.Body, *sigHeader, sigHeader.Name, tags, modeAS, now, location)
	hop.SealValid = core.valid
	hop.Findings = append(hop.Findings, core.findings...)
	if !core.valid {
		hop.addFinding(newFinding(FindingARCSealInvalid, SeverityCritical, location,
			"ARC-Seal verification failed"))
	}
}

// collectARCSets groups ARC fields by instance number. Fields without a
// parseable instance are returned separately.
func collectARCSets(headers []Header) (map[int]*arcSet, []Header) {
	sets := make(map[int]*arcSet)
	var unknown []Header

	for _, h := range headers {
		lower := strings.ToLower(h.Name)
		inst, ok := arcInstance(lower)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(lower, "arc-authentication-results"):
			ensureSet(sets, inst).aar = headerPtr(h)
		case strings.HasPrefix(lower, "arc-message-signature"):
			ensureSet(sets, inst).ams = headerPtr(h)
		case strings.HasPrefix(lower, "arc-seal"):
			ensureSet(sets, inst).as = headerPtr(h)
		default:
			unknown = append(unknown, h)
		}
	}
	return sets, unknown
}

func ensureSet(sets map[int]*arcSet, inst int) *arcSet {
	s, ok := sets[inst]
	if !ok {
		s = &arcSet{instance: inst}
		sets[inst] = s
	}
	return s
}

func headerPtr(h Header) *Header {
	copy := h
	return &copy
}

func (r *ARCResult) addFinding(f Finding) {
	r.Findings = append(r.Findings, f)
}

func (h *ARCHopResult) addFinding(f Finding) {
	h.Findings = append(h.Findings, f)
}

// hasInvalidFinding reports whether any collected finding represents a hard
// chain failure (critical/high severity).
func (r *ARCResult) hasInvalidFinding() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			return true
		}
	}
	for _, hop := range r.Hops {
		for _, f := range hop.Findings {
			if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
				return true
			}
		}
	}
	return false
}
