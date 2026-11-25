package migration

import (
	"math/big"
	"sort"
)

type receiptGroup struct {
	key          string
	requestID    string
	oldDeviceID  string
	count        int
	oldIntervals []Interval
	newIntervals []Interval
	oldSigned    bool
	newSigned    bool
}

// BuildCertificate evaluates all compatible histories and returns a certificate.
func BuildCertificate(input Input, verifier SignatureVerifier) (Certificate, error) {
	if verifier == nil {
		return Certificate{}, errInvalid("verifier must not be nil")
	}
	if input.NewDeviceID == "" {
		return Certificate{}, errInvalid("new device id must not be empty")
	}
	if !input.Domain.Valid() {
		return Certificate{}, errInvalid("domain interval is invalid")
	}

	cert := Certificate{
		NewDeviceID: input.NewDeviceID,
		Domain:      input.Domain,
		Status:      StatusValid,
		Input:       cloneInput(input),
	}

	findings := make([]Finding, 0)
	revoked := uniqueStrings(input.RevokedSegmentIDs)
	cert.RevokedSegmentIDs = revoked

	active, rejectedSegmentIDs, segmentFindings := validateSegments(input, verifier, revoked)
	findings = append(findings, segmentFindings...)

	mapping := buildUsableMapping(input, active, &findings)
	cert.Mapping = mapping.claims
	cert.Coverage = normalizedRange(mapping.coverage)
	cert.Conflicts = normalizedRange(mapping.conflicts)
	cert.Blocked = normalizedRange(mapping.blocked)

	rejected := make([]string, 0, len(mapping.rejected))
	for id := range mapping.rejected {
		rejected = append(rejected, id)
	}
	rejected = append(rejected, rejectedSegmentIDs...)
	sort.Strings(rejected)
	cert.RejectedSegmentIDs = rejected

	groups, receiptFindings := groupReceipts(input, verifier, revoked)
	findings = append(findings, receiptFindings...)

	requests := make([]RequestClaim, 0, len(groups))
	for _, group := range groups {
		claim := buildRequestClaim(group, input, mapping, &findings)
		requests = append(requests, claim)
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].RequestID != requests[j].RequestID {
			return requests[i].RequestID < requests[j].RequestID
		}
		return requests[i].OldDeviceID < requests[j].OldDeviceID
	})
	cert.Requests = requests

	sortFindings(findings)
	cert.Findings = findings
	for _, finding := range findings {
		if finding.Severity == Blocking {
			cert.Status = StatusInvalid
			break
		}
	}

	if cert.Status == StatusValid {
		classifySteps(&cert)
	} else {
		cert.Consumed = RangeSet{Intervals: []Interval{}}
		cert.Unknown = RangeSet{Intervals: []Interval{}}
		cert.Safe = RangeSet{Intervals: []Interval{}}
	}
	return cert, nil
}

func validateSegments(input Input, verifier SignatureVerifier, revoked []string) (map[string]Segment, []string, []Finding) {
	active := make(map[string]Segment)
	findings := make([]Finding, 0)
	rejected := make([]string, 0)
	seen := make(map[string]bool)
	revokedSet := stringSet(revoked)

	for _, segment := range input.Segments {
		valid := true
		switch {
		case segment.ID == "":
			findings = append(findings, blockingFinding("segment_id_empty", "migration segment id must not be empty", segment.ID, ""))
			continue
		case seen[segment.ID]:
			findings = append(findings, blockingFinding("segment_id_duplicate", "migration segment id is repeated", segment.ID, ""))
			continue
		case segment.OldDeviceID == "" || segment.NewDeviceID == "":
			findings = append(findings, blockingFinding("segment_device_empty", "migration segment must identify both devices", segment.ID, ""))
			valid = false
		case segment.NewDeviceID != input.NewDeviceID:
			findings = append(findings, blockingFinding("segment_device_mismatch", "migration segment does not target the requested new device", segment.ID, ""))
			valid = false
		case !segment.Old.Valid() || !segment.New.Valid():
			findings = append(findings, blockingFinding("segment_interval_invalid", "migration segment interval is invalid", segment.ID, ""))
			valid = false
		case segment.Orientation != Forward && segment.Orientation != Reverse:
			findings = append(findings, blockingFinding("segment_orientation_invalid", "migration segment orientation must be forward or reverse", segment.ID, ""))
			valid = false
		case !intervalsHaveEqualLength(segment.Old, segment.New):
			findings = append(findings, blockingFinding("segment_length_mismatch", "old and new segment intervals must have equal length", segment.ID, ""))
			valid = false
		case segment.Signature.KeyID == "" || len(segment.Signature.Value) == 0 ||
			!verifier.Verify(segment.Signature.KeyID, SegmentPayload(segment), segment.Signature.Value):
			findings = append(findings, blockingFinding("segment_signature_invalid", "migration segment signature is missing or invalid", segment.ID, ""))
			valid = false
		}
		seen[segment.ID] = true
		if valid && !revokedSet[segment.ID] {
			active[segment.ID] = segment
		} else if segment.ID != "" {
			rejected = append(rejected, segment.ID)
		}
	}
	return active, uniqueStrings(rejected), findings
}

func groupReceipts(input Input, verifier SignatureVerifier, revoked []string) (map[string]*receiptGroup, []Finding) {
	groups := make(map[string]*receiptGroup)
	findings := make([]Finding, 0)
	revokedSet := stringSet(revoked)

	for _, receipt := range input.Receipts {
		validReceipt := receipt.RequestID != "" && receipt.DeviceID != "" && receipt.Steps.Valid() &&
			(receipt.Side == OldSide || receipt.Side == NewSide) &&
			receipt.Signature.KeyID != "" && len(receipt.Signature.Value) > 0 &&
			verifier.Verify(receipt.Signature.KeyID, ReceiptPayload(receipt), receipt.Signature.Value)
		if !validReceipt {
			findings = append(findings, blockingFinding("receipt_signature_invalid", "receipt is malformed or has an invalid signature", "", receipt.RequestID))
			continue
		}
		if receipt.Side == NewSide && receipt.DeviceID != input.NewDeviceID {
			findings = append(findings, blockingFinding("receipt_device_mismatch", "new-side receipt does not belong to the requested device", "", receipt.RequestID))
			continue
		}

		oldDeviceID := ""
		if receipt.Side == OldSide {
			oldDeviceID = receipt.DeviceID
		}
		key := receipt.RequestID
		group, ok := groups[key]
		if !ok {
			group = &receiptGroup{
				key:         key,
				requestID:   receipt.RequestID,
				oldDeviceID: oldDeviceID,
			}
			groups[key] = group
		}
		group.count++
		if receipt.Side == OldSide {
			if group.oldDeviceID != "" && group.oldDeviceID != oldDeviceID {
				findings = append(findings, blockingFinding("request_old_device_conflict", "same request id was emitted by multiple old devices", "", receipt.RequestID))
			}
			group.oldDeviceID = oldDeviceID
			group.oldSigned = true
			group.oldIntervals = append(group.oldIntervals, receipt.Steps)
		} else {
			group.newSigned = true
			group.newIntervals = append(group.newIntervals, receipt.Steps)
		}
	}

	for _, segment := range input.Segments {
		if !revokedSet[segment.ID] {
			continue
		}
		segmentOld := NewRangeSet([]Interval{segment.Old})
		for _, group := range groups {
			if group.oldDeviceID == segment.OldDeviceID && !segmentOld.Intersect(NewRangeSet(group.oldIntervals)).IsEmpty() {
				findings = append(findings, blockingFinding("revoked_segment_receipt", "receipt may rely on a revoked migration segment", segment.ID, group.requestID))
			}
		}
	}

	return groups, findings
}

func buildRequestClaim(group *receiptGroup, input Input, mapping usableMapping, findings *[]Finding) RequestClaim {
	oldSet := NewRangeSet(group.oldIntervals)
	rawNewSet := NewRangeSet(group.newIntervals)
	newSet := rawNewSet.Intersect(NewRangeSet([]Interval{input.Domain}))
	claim := RequestClaim{
		RequestID:    group.requestID,
		OldDeviceID:  group.oldDeviceID,
		ReceiptCount: group.count,
		OldReceipt:   oldSet,
		NewReceipt:   newSet,
	}

	if group.oldSigned {
		coveredOld := RangeSet{}
		for _, piece := range mapping.claims {
			if piece.OldDeviceID != group.oldDeviceID {
				continue
			}
			overlap := oldSet.Intersect(NewRangeSet([]Interval{piece.Old}))
			if overlap.IsEmpty() {
				continue
			}
			mapped := mapOldRange(Segment{
				Old:         piece.Old,
				New:         piece.New,
				Orientation: piece.Orientation,
			}, overlap)
			coveredOld = coveredOld.Union(overlap)
			claim.MappedNew = claim.MappedNew.Union(mapped)
		}
		if !coveredOld.Equal(oldSet) {
			*findings = append(*findings, blockingFinding("migration_missing", "old receipt cannot be explained by an active signed mapping", "", group.requestID))
		}
	}

	if group.newSigned {
		if !rawNewSet.Equal(newSet) {
			*findings = append(*findings, blockingFinding("receipt_outside_domain", "new-side receipt lies outside the requested domain", "", group.requestID))
		}
		if !mapping.coverage.Intersect(newSet).Equal(newSet) {
			*findings = append(*findings, blockingFinding("new_receipt_unmapped", "new-side receipt lies outside signed migration coverage", "", group.requestID))
		}
		if group.oldSigned {
			claim.PossibleNew = claim.MappedNew.Intersect(newSet)
		} else {
			claim.PossibleNew = newSet
		}
	} else if group.oldSigned {
		claim.PossibleNew = claim.MappedNew
	}

	if claim.PossibleNew.IsEmpty() {
		*findings = append(*findings, blockingFinding("request_no_compatible_step", "request has no compatible new-device step", "", group.requestID))
	}
	return claim
}

func classifySteps(cert *Certificate) {
	domain := NewRangeSet([]Interval{cert.Domain})
	possibleRanges := make([]RangeSet, 0, len(cert.Requests))
	for _, request := range cert.Requests {
		possibleRanges = append(possibleRanges, request.PossibleNew)
	}

	cells := PartitionRanges(possibleRanges)
	consumed := RangeSet{}
	unknown := RangeSet{}
	for cellIndex, cell := range cells {
		cellSet := NewRangeSet([]Interval{cell})
		canAvoidStep := feasibleAssignment(cert.Requests, cells, cellIndex, true)
		if !canAvoidStep {
			consumed = consumed.Union(cellSet)
			continue
		}
		if feasibleAssignment(cert.Requests, cells, cellIndex, false) {
			unknown = unknown.Union(cellSet)
		}
	}

	known := consumed.Union(unknown).Union(cert.Blocked)
	cert.Consumed = normalizedRange(consumed)
	cert.Unknown = normalizedRange(unknown)
	cert.Safe = normalizedRange(domain.Subtract(known))
}

func feasibleAssignment(requests []RequestClaim, cells []Interval, avoidedCell int, avoid bool) bool {
	capacities := make([]int, len(cells))
	requestLimit := big.NewInt(int64(len(requests)))
	for index, cell := range cells {
		cellCapacity := NewRangeSet([]Interval{cell}).Length()
		if avoid {
			if index == avoidedCell {
				cellCapacity.Sub(cellCapacity, big.NewInt(1))
			}
			if cellCapacity.Cmp(requestLimit) > 0 {
				cellCapacity = new(big.Int).Set(requestLimit)
			}
		} else if cellCapacity.Cmp(requestLimit) > 0 {
			cellCapacity = new(big.Int).Set(requestLimit)
		}
		if cellCapacity.Sign() < 0 {
			return false
		}
		if !cellCapacity.IsInt64() || cellCapacity.Int64() > int64(maxInt()) {
			return false
		}
		capacities[index] = int(cellCapacity.Int64())
	}

	dinic := newDinic(len(requests), len(cells), capacities)
	for requestIndex, request := range requests {
		dinic.addRequestEdges(requestIndex, request.PossibleNew, cells)
	}
	return dinic.maxFlow() == len(requests)
}

func blockingFinding(code, message, segmentID, requestID string) Finding {
	return Finding{
		Code:      code,
		Severity:  Blocking,
		Message:   message,
		SegmentID: segmentID,
		RequestID: requestID,
	}
}

func uniqueStrings(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		if findings[i].SegmentID != findings[j].SegmentID {
			return findings[i].SegmentID < findings[j].SegmentID
		}
		return findings[i].RequestID < findings[j].RequestID
	})
}

func cloneInput(input Input) Input {
	clone := input
	clone.Segments = append([]Segment(nil), input.Segments...)
	clone.Receipts = append([]Receipt(nil), input.Receipts...)
	clone.RevokedSegmentIDs = append([]string(nil), input.RevokedSegmentIDs...)
	for index := range clone.Segments {
		clone.Segments[index].Signature.Value = append([]byte(nil), input.Segments[index].Signature.Value...)
	}
	for index := range clone.Receipts {
		clone.Receipts[index].Signature.Value = append([]byte(nil), input.Receipts[index].Signature.Value...)
	}
	return clone
}

type migrationError string

func (e migrationError) Error() string {
	return string(e)
}

func errInvalid(message string) error {
	return migrationError(message)
}
