package migration

import (
	"fmt"
	"reflect"
)

// VerifyCertificate independently recomputes and checks the complete certificate.
func VerifyCertificate(cert Certificate, verifier SignatureVerifier) (bool, []Finding) {
	findings := make([]Finding, 0)
	if cert.NewDeviceID == "" || !cert.Domain.Valid() {
		findings = append(findings, blockingFinding("certificate_header_invalid", "certificate header is invalid", "", ""))
		return false, findings
	}

	recomputed, err := BuildCertificate(cert.Input, verifier)
	if err != nil {
		findings = append(findings, blockingFinding("certificate_rebuild_failed", err.Error(), "", ""))
		return false, findings
	}

	domain := NewRangeSet([]Interval{cert.Domain})
	findings = checkNormalized(findings, cert.Coverage, "coverage", "")
	findings = checkNormalized(findings, cert.Conflicts, "conflicts", "")
	findings = checkNormalized(findings, cert.Blocked, "blocked", "")
	findings = checkNormalized(findings, cert.Consumed, "consumed", "")
	findings = checkNormalized(findings, cert.Unknown, "unknown", "")
	findings = checkNormalized(findings, cert.Safe, "safe", "")

	if !cert.Conflicts.Intersect(domain).Equal(cert.Conflicts) {
		findings = append(findings, blockingFinding("certificate_conflicts_outside_domain", "conflict range is outside domain", "", ""))
	}
	if !cert.Blocked.Intersect(domain).Equal(cert.Blocked) {
		findings = append(findings, blockingFinding("certificate_blocked_outside_domain", "blocked range is outside domain", "", ""))
	}
	if overlaps(cert.Consumed, cert.Unknown) ||
		overlaps(cert.Consumed, cert.Blocked) ||
		overlaps(cert.Unknown, cert.Blocked) {
		findings = append(findings, blockingFinding("certificate_decomposition_overlap", "consumed, unknown and blocked ranges must be disjoint", "", ""))
	}
	known := cert.Consumed.Union(cert.Unknown).Union(cert.Blocked)
	if !cert.Safe.Union(known).Equal(domain) {
		findings = append(findings, blockingFinding("certificate_decomposition_incomplete", "decomposition does not cover the complete domain", "", ""))
	}

	expected := expectedCertificate(recomputed)
	actual := expectedCertificate(cert)
	if !reflect.DeepEqual(actual, expected) {
		findings = append(findings, blockingFinding("certificate_recomputation_mismatch",
			fmt.Sprintf("certificate does not match recomputation from embedded evidence: got %#v want %#v", actual, expected), "", ""))
	}
	return len(findings) == 0, findings
}

// Acceptable reports whether a valid certificate exists and has no blocking finding.
func (c Certificate) Acceptable() bool {
	if c.Status != StatusValid {
		return false
	}
	for _, finding := range c.Findings {
		if finding.Severity == Blocking {
			return false
		}
	}
	return true
}

type comparableCertificate struct {
	NewDeviceID        string
	Domain             Interval
	Status             Status
	Findings           []Finding
	RevokedSegmentIDs  []string
	RejectedSegmentIDs []string
	Mapping            []MappingClaim
	Coverage           RangeSet
	Conflicts          RangeSet
	Blocked            RangeSet
	Requests           []RequestClaim
	Consumed           RangeSet
	Unknown            RangeSet
	Safe               RangeSet
}

func expectedCertificate(cert Certificate) comparableCertificate {
	return comparableCertificate{
		NewDeviceID:        cert.NewDeviceID,
		Domain:             cert.Domain,
		Status:             cert.Status,
		Findings:           normalizedFindings(cert.Findings),
		RevokedSegmentIDs:  normalizedStrings(cert.RevokedSegmentIDs),
		RejectedSegmentIDs: normalizedStrings(cert.RejectedSegmentIDs),
		Mapping:            normalizedMapping(cert.Mapping),
		Coverage:           normalizedRange(cert.Coverage),
		Conflicts:          normalizedRange(cert.Conflicts),
		Blocked:            normalizedRange(cert.Blocked),
		Requests:           normalizedRequests(cert.Requests),
		Consumed:           normalizedRange(cert.Consumed),
		Unknown:            normalizedRange(cert.Unknown),
		Safe:               normalizedRange(cert.Safe),
	}
}

func checkNormalized(findings []Finding, set RangeSet, code, requestID string) []Finding {
	if !reflect.DeepEqual(set, NewRangeSet(set.Intervals)) {
		return append(findings, blockingFinding("certificate_"+code+"_not_normalized", code+" range set is not normalized", "", requestID))
	}
	return findings
}

func overlaps(a, b RangeSet) bool {
	return !a.Intersect(b).IsEmpty()
}

func normalizedRange(set RangeSet) RangeSet {
	if set.Intervals == nil {
		return RangeSet{Intervals: []Interval{}}
	}
	return NewRangeSet(append([]Interval(nil), set.Intervals...))
}

func normalizedStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func normalizedFindings(findings []Finding) []Finding {
	if findings == nil {
		return []Finding{}
	}
	return append([]Finding(nil), findings...)
}

func normalizedMapping(mapping []MappingClaim) []MappingClaim {
	if mapping == nil {
		return []MappingClaim{}
	}
	return append([]MappingClaim(nil), mapping...)
}

func normalizedRequests(requests []RequestClaim) []RequestClaim {
	if requests == nil {
		return []RequestClaim{}
	}
	out := make([]RequestClaim, len(requests))
	for index, request := range requests {
		out[index] = request
		out[index].OldReceipt = normalizedRange(request.OldReceipt)
		out[index].NewReceipt = normalizedRange(request.NewReceipt)
		out[index].MappedNew = normalizedRange(request.MappedNew)
		out[index].PossibleNew = normalizedRange(request.PossibleNew)
	}
	return out
}
