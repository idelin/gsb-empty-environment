// Package proofagg aggregates signed self-test results from a device into a
// single pass/fail verdict for the security monitor.
//
// A verdict of "pass" is produced only when every required sub-test is
// covered by exactly one result that (a) carries a valid signature from the
// trusted measurement callback, (b) is bound to the current boot challenge,
// and (c) is bound to the expected firmware digest. Results that are
// unknown, duplicated, replayed from another boot, bound to different
// firmware, failed, or badly signed are never counted toward a pass; any
// such anomaly fails the verdict closed.
package proofagg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Verifier is the trusted measurement callback. It reports whether
// signature is valid over the canonical result payload. The host supplies
// the implementation (HMAC, Ed25519, TPM quote check, ...); the aggregator
// never sees key material directly.
type Verifier func(payload, signature []byte) bool

// Status is the reported outcome of a single sub-test.
type Status uint8

const (
	StatusFail Status = iota
	StatusPass
)

// Result is one signed sub-test attestation emitted by the device.
type Result struct {
	TestID         string
	Challenge      [32]byte
	FirmwareDigest [32]byte
	Status         Status
	Signature      []byte
}

// CanonicalPayload is the deterministic byte encoding that the trusted
// measurement callback signs and verifies. Changing any field, including
// the status bit, changes the payload and therefore the signature.
func CanonicalPayload(testID string, challenge, firmwareDigest [32]byte, status Status) []byte {
	var buf bytes.Buffer
	buf.WriteString("proofagg/v1\x00")
	buf.Write(challenge[:])
	buf.Write(firmwareDigest[:])
	binary.Write(&buf, binary.BigEndian, uint32(len(testID)))
	buf.WriteString(testID)
	buf.WriteByte(byte(status))
	return buf.Bytes()
}

// Rejection records why a submitted result was excluded from the verdict.
type Rejection struct {
	TestID string
	Reason string
}

// Evidence is a result that was accepted into the verdict. A passing
// verdict references only evidence bound to the aggregator's own challenge
// and firmware digest.
type Evidence struct {
	TestID         string
	Challenge      [32]byte
	FirmwareDigest [32]byte
}

// Verdict is the aggregated conclusion for one boot.
type Verdict struct {
	Passed   bool
	Evidence []Evidence
	Missing  []string
	Rejected []Rejection
}

// Aggregator binds one boot challenge, one firmware digest, and the set of
// required sub-test IDs.
type Aggregator struct {
	challenge [32]byte
	firmware  [32]byte
	required  map[string]struct{}
	verify    Verifier
}

// NewAggregator builds an Aggregator. required must be non-empty and free
// of duplicates; verify must be non-nil.
func NewAggregator(challenge, firmwareDigest [32]byte, required []string, verify Verifier) (*Aggregator, error) {
	if verify == nil {
		return nil, errors.New("proofagg: nil verifier")
	}
	if len(required) == 0 {
		return nil, errors.New("proofagg: empty required test set")
	}
	req := make(map[string]struct{}, len(required))
	for _, id := range required {
		if id == "" {
			return nil, errors.New("proofagg: empty required test id")
		}
		if _, dup := req[id]; dup {
			return nil, fmt.Errorf("proofagg: duplicate required test id %q", id)
		}
		req[id] = struct{}{}
	}
	return &Aggregator{
		challenge: challenge,
		firmware:  firmwareDigest,
		required:  req,
		verify:    verify,
	}, nil
}

// Aggregate folds a batch of results into a verdict. It is fail-closed:
// any rejected result or any missing required test yields Passed == false.
func (a *Aggregator) Aggregate(results []Result) Verdict {
	var v Verdict
	covered := make(map[string]struct{}, len(a.required))

	for i := range results {
		r := &results[i]
		if reason := a.admit(r, covered); reason != "" {
			v.Rejected = append(v.Rejected, Rejection{TestID: r.TestID, Reason: reason})
			continue
		}
		covered[r.TestID] = struct{}{}
		v.Evidence = append(v.Evidence, Evidence{
			TestID:         r.TestID,
			Challenge:      r.Challenge,
			FirmwareDigest: r.FirmwareDigest,
		})
	}

	for id := range a.required {
		if _, ok := covered[id]; !ok {
			v.Missing = append(v.Missing, id)
		}
	}
	sort.Strings(v.Missing)

	v.Passed = len(v.Rejected) == 0 && len(v.Missing) == 0
	return v
}

// admit returns an empty string if r may be counted, else the reason it is
// excluded. A result is counted only if it is signature-valid, bound to
// this boot's challenge and firmware, a known required test, not a
// duplicate, and reports StatusPass.
func (a *Aggregator) admit(r *Result, covered map[string]struct{}) string {
	if _, ok := a.required[r.TestID]; !ok {
		return "unknown test id"
	}
	if _, dup := covered[r.TestID]; dup {
		return "duplicate test id"
	}
	if r.Challenge != a.challenge {
		return "challenge mismatch"
	}
	if r.FirmwareDigest != a.firmware {
		return "firmware digest mismatch"
	}
	payload := CanonicalPayload(r.TestID, r.Challenge, r.FirmwareDigest, r.Status)
	if !a.verify(payload, r.Signature) {
		return "invalid signature"
	}
	if r.Status != StatusPass {
		return "sub-test reported failure"
	}
	return ""
}
