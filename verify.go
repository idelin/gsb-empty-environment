package netsec

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
)

// Verify checks the supplied transcript and returns a detailed Report. It
// never panics on malformed input: every structural or cryptographic failure
// is recorded as a Finding with severity SevError, and capabilities observed
// only in unauthenticated records are preserved as non-authoritative clues.
func (v *Verifier) Verify(records []Record) *Report {
	report := &Report{
		SessionID: append([]byte(nil), v.policy.SessionID...),
		Findings:  make([]Finding, 0),
		Clues:     make([]CapabilityEvidence, 0),
		Records:   make([]RecordEvidence, 0, len(records)),
	}
	add := func(sev Severity, code FindingCode, index int, format string, args ...any) {
		report.Findings = append(report.Findings, Finding{
			Severity:    sev,
			Code:        code,
			RecordIndex: index,
			Message:     fmt.Sprintf(format, args...),
		})
	}

	expectedTypes := []RecordType{
		TypeCapabilitiesA,
		TypeCapabilitiesB,
		TypeSelection,
		TypeConfirmation,
	}

	if len(records) > len(expectedTypes) {
		add(SevError, FindingUnexpectedRecord, len(expectedTypes),
			"transcript has %d records, expected exactly %d", len(records), len(expectedTypes))
	}

	type parsedCaps struct {
		authenticated bool
		party         Party
		algorithms    []string
		raw           []byte
	}
	capsBySlot := map[int]*parsedCaps{}
	var selectedString string
	var selectionAuthenticated, confirmAuthenticated bool

	for slot := 0; slot < len(expectedTypes); slot++ {
		if slot >= len(records) {
			add(SevError, FindingMissingRecord, slot,
				"missing required record at slot %d (type %d)", slot, expectedTypes[slot])
			continue
		}
		rec := records[slot]
		evidence := RecordEvidence{
			Index: slot,
			Type:  rec.Type,
			Raw:   append([]byte(nil), rec.Raw...),
			Marks: append([]Mark(nil), rec.Marks...),
		}

		if rec.Type != expectedTypes[slot] {
			add(SevError, FindingReordered, slot,
				"record type %d at slot %d, expected %d", rec.Type, slot, expectedTypes[slot])
		}

		headerType, decodeErr := decodeHeader(rec.Raw)
		if decodeErr != nil {
			add(SevError, FindingMalformed, slot, "decode header: %v", decodeErr)
			report.Records = append(report.Records, evidence)
			continue
		}
		if headerType != rec.Type {
			add(SevError, FindingMalformed, slot,
				"raw header type %d does not match record type %d", headerType, rec.Type)
		}

		authenticated, markFindings := v.verifyMarks(rec, slot)
		report.Findings = append(report.Findings, markFindings...)
		evidence.Authenticated = authenticated

		switch rec.Type {
		case TypeCapabilitiesA, TypeCapabilitiesB:
			_, algorithms, parseErr := decodeCapsRaw(rec.Raw)
			party := PartyA
			if rec.Type == TypeCapabilitiesB {
				party = PartyB
			}
			if parseErr != nil {
				add(SevError, FindingMalformed, slot, "decode capabilities: %v", parseErr)
				break
			}
			evidence.Algorithms = algorithms
			if duplicated(algorithms) {
				add(SevError, FindingDuplicateAlgorithm, slot,
					"capability list contains duplicate algorithms")
				authenticated = false
				evidence.Authenticated = false
			}
			capsBySlot[slot] = &parsedCaps{
				authenticated: authenticated,
				party:         party,
				algorithms:    algorithms,
				raw:           append([]byte(nil), rec.Raw...),
			}
			if !authenticated {
				for _, alg := range algorithms {
					report.Clues = append(report.Clues, CapabilityEvidence{
						Algorithm:     alg,
						Party:         party,
						RecordIndex:   slot,
						Raw:           []byte(alg),
						Authenticated: false,
					})
				}
			}

		case TypeSelection:
			chosen, hashA, hashB, parseErr := decodeSelectionRaw(rec.Raw)
			if parseErr != nil {
				add(SevError, FindingMalformed, slot, "decode selection: %v", parseErr)
				break
			}
			evidence.Selected = chosen
			selectedString = chosen
			selectionAuthenticated = authenticated
			if capsA, ok := capsBySlot[0]; ok {
				if !bytes.Equal(hashA[:], sha256Sum(capsA.raw)) {
					add(SevError, FindingHashMismatch, slot,
						"selection does not bind the observed party A capability bytes")
				}
			}
			if capsB, ok := capsBySlot[1]; ok {
				if !bytes.Equal(hashB[:], sha256Sum(capsB.raw)) {
					add(SevError, FindingHashMismatch, slot,
						"selection does not bind the observed party B capability bytes")
				}
			}

		case TypeConfirmation:
			chainHash, parseErr := decodeConfirmRaw(rec.Raw)
			if parseErr != nil {
				add(SevError, FindingMalformed, slot, "decode confirmation: %v", parseErr)
				break
			}
			confirmAuthenticated = authenticated
			raw0, ok0 := rawAt(records, 0)
			raw1, ok1 := rawAt(records, 1)
			raw2, ok2 := rawAt(records, 2)
			if ok0 && ok1 && ok2 {
				want := chainHashOf(raw0, raw1, raw2)
				if !bytes.Equal(chainHash[:], want[:]) {
					add(SevError, FindingChainMismatch, slot,
						"confirmation transcript hash does not match the observed transcript")
				}
			}

		default:
			add(SevError, FindingMalformed, slot, "unknown record type %d", rec.Type)
		}

		report.Records = append(report.Records, evidence)
	}

	capsA := capsBySlot[0]
	capsB := capsBySlot[1]
	if capsA != nil && capsA.authenticated {
		report.AuthenticatedCapabilitiesA = append([]string(nil), capsA.algorithms...)
	}
	if capsB != nil && capsB.authenticated {
		report.AuthenticatedCapabilitiesB = append([]string(nil), capsB.algorithms...)
	}

	capsAuthentic := capsA != nil && capsA.authenticated && capsB != nil && capsB.authenticated
	if capsAuthentic {
		report.Intersection = intersection(capsA.algorithms, capsB.algorithms)

		preference := v.policy.PreferenceOrder
		if preference == nil {
			preference = capsB.algorithms
		}
		expected := firstInOrder(preference, report.Intersection)

		if selectedString == "" {
			add(SevError, FindingSelectionNotInIntersection, 2,
				"no selected algorithm present in transcript")
		} else if !contains(report.Intersection, selectedString) {
			add(SevError, FindingSelectionNotInIntersection, 2,
				"selected algorithm %q is not in the authenticated intersection %v",
				selectedString, report.Intersection)
		} else if expected != "" && selectedString != expected {
			add(SevError, FindingPreferenceViolation, 2,
				"selected %q but preference order requires %q from intersection %v",
				selectedString, expected, report.Intersection)
		}

		if selectionAuthenticated && contains(report.Intersection, selectedString) {
			report.Selected = selectedString
			report.SelectedAuthenticated = true
			if v.policy.Acceptable != nil && !v.policy.Acceptable[selectedString] {
				add(SevWarning, FindingWeakSelection, 2,
					"selected algorithm %q is outside the host acceptable policy", selectedString)
			}
		}
	}

	report.Authentic = !hasSeverity(report.Findings, SevError) &&
		len(records) == 4 &&
		confirmAuthenticated &&
		selectionAuthenticated

	report.Secure = report.Authentic &&
		report.SelectedAuthenticated &&
		!hasCode(report.Findings, FindingWeakSelection)

	return report
}

// verifyMarks checks that every mark verifies under a trusted key for the
// bound session and position, and that every required signer is present.
func (v *Verifier) verifyMarks(rec Record, index int) (bool, []Finding) {
	findings := make([]Finding, 0)
	required := requiredSigners(rec.Type)
	present := map[Party]bool{}

	for _, mark := range rec.Marks {
		key, trusted := v.trustedKey(mark.Signer)
		if !trusted {
			findings = append(findings, Finding{
				Severity:    SevError,
				Code:        FindingUnknownSigner,
				RecordIndex: index,
				Message:     fmt.Sprintf("mark from untrusted party %d", mark.Signer),
			})
			continue
		}
		if len(mark.Signature) != ed25519.SignatureSize {
			findings = append(findings, Finding{
				Severity:    SevError,
				Code:        FindingBadSignature,
				RecordIndex: index,
				Message:     fmt.Sprintf("party %d signature has wrong size", mark.Signer),
			})
			continue
		}
		payload := signingPayload(v.policy.SessionID, index, rec.Raw, rec.Type, mark.Signer)
		if !ed25519.Verify(key, payload, mark.Signature) {
			findings = append(findings, Finding{
				Severity:    SevError,
				Code:        FindingBadSignature,
				RecordIndex: index,
				Message: fmt.Sprintf("party %d signature does not verify (tampered, reordered, "+
					"deleted or foreign-session bytes)", mark.Signer),
			})
			continue
		}
		present[mark.Signer] = true
	}

	for _, party := range required {
		if !present[party] {
			findings = append(findings, Finding{
				Severity:    SevError,
				Code:        FindingMissingSignature,
				RecordIndex: index,
				Message:     fmt.Sprintf("required signature from party %d is absent", party),
			})
		}
	}

	return len(findings) == 0, findings
}

func rawAt(records []Record, index int) ([]byte, bool) {
	if index < 0 || index >= len(records) {
		return nil, false
	}
	return records[index].Raw, true
}

func duplicated(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// intersection preserves the order of party A's authenticated list and only
// includes algorithms authenticated by both parties.
func intersection(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, alg := range b {
		inB[alg] = true
	}
	out := make([]string, 0)
	seen := make(map[string]bool)
	for _, alg := range a {
		if inB[alg] && !seen[alg] {
			out = append(out, alg)
			seen[alg] = true
		}
	}
	return out
}

// firstInOrder returns the first element of preference that appears in
// candidates, implementing the chooser's input preference order.
func firstInOrder(preference, candidates []string) string {
	candidateSet := make(map[string]bool, len(candidates))
	for _, alg := range candidates {
		candidateSet[alg] = true
	}
	for _, alg := range preference {
		if candidateSet[alg] {
			return alg
		}
	}
	return ""
}

func hasSeverity(findings []Finding, severity Severity) bool {
	for _, finding := range findings {
		if finding.Severity == severity {
			return true
		}
	}
	return false
}

func hasCode(findings []Finding, code FindingCode) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
