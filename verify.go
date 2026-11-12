package netsec

import "fmt"

// PCRBank selects one PCR bank (hash algorithm) and the PCR indices within it.
type PCRBank struct {
	Hash AlgorithmID
	// PCRs lists PCR indices, e.g. 0..7 for the standard boot measurements.
	PCRs []int
}

// Measurement is one expected PCR extend in boot order.
type Measurement struct {
	PCRIndex  int
	EventType uint32
	// Digests must contain the digest recorded for each advertised bank.
	Digests []EventDigest
}

// AllowedSequence is one caller-authorized boot measurement sequence and the
// device identity released when the replayed log matches it exactly.
type AllowedSequence struct {
	DeviceID     string
	Measurements []Measurement
}

// AttestationRequest bundles every input to the consistency verifier.
type AttestationRequest struct {
	// Quote is the TPM2B_ATTEST blob returned by TPM2_Quote.
	Quote []byte
	// Signature is the TPMT_SIGNATURE over Quote.
	Signature []byte
	// AKPublic is the TPM2B_PUBLIC encoding of the attestation key.
	AKPublic []byte
	// Nonce is the freshness value supplied by the verifier.
	Nonce []byte
	// RequestedPCRs is the selection the verifier asked the TPM to quote.
	RequestedPCRs []PCRBank
	// EventLog is the raw TCG PC Client binary event log.
	EventLog []byte
	// AllowedSequences is the caller-defined allowlist of boot measurements.
	AllowedSequences []AllowedSequence
}

// AttestationResult is returned after successful verification.
type AttestationResult struct {
	// DeviceID comes from the matching AllowedSequence.
	DeviceID string
	// Replay is the parsed and replayed event log.
	Replay *ReplayedLog
}

// Verify performs the full remote attestation consistency check:
//
//  1. The AK public key and Quote structures are parsed and the Quote
//     signature is verified against the AK.
//  2. The Quote nonce and PCR selection must match the verifier inputs.
//  3. The TCG event log is replayed; the Spec ID event only defines the
//     digest layout and is never extended. Missing digests, unknown
//     algorithms and truncated entries are rejected.
//  4. Replayed PCR values must reproduce the Quote PCR digest exactly.
//  5. The measurement sequence must match an entry of AllowedSequences,
//     after which the associated device identity is released.
func Verify(req AttestationRequest) (*AttestationResult, error) {
	ak, err := parseAKPublic(req.AKPublic)
	if err != nil {
		return nil, fmt.Errorf("parse AK public key: %w", err)
	}
	sig, err := parseTPMTSignature(req.Signature)
	if err != nil {
		return nil, fmt.Errorf("parse quote signature: %w", err)
	}
	quote, err := parseQuote(req.Quote)
	if err != nil {
		return nil, fmt.Errorf("parse quote: %w", err)
	}

	if !bytesEqual(quote.nonce, req.Nonce) {
		return nil, ErrNonceMismatch
	}
	if !equalSelection(quote.selection, req.RequestedPCRs) {
		return nil, ErrPCRSelectionMismatch
	}
	if err := verifyQuoteSignature(ak, sig, req.Quote); err != nil {
		return nil, err
	}

	replay, err := ReplayEventLog(req.EventLog)
	if err != nil {
		return nil, fmt.Errorf("replay event log: %w", err)
	}

	if err := checkQuotePCRDigest(quote, replay, sig); err != nil {
		return nil, err
	}

	deviceID, err := matchIdentity(replay, req.AllowedSequences)
	if err != nil {
		return nil, err
	}
	return &AttestationResult{DeviceID: deviceID, Replay: replay}, nil
}

// checkQuotePCRDigest recomputes the TPM2_Quote pcrDigest from the replayed
// PCR values. Following the TPM reference implementation, every selected PCR
// value is concatenated in bank/PCR order and hashed once with the signature
// hash, then compared byte-for-byte with the digest embedded in the Quote.
func checkQuotePCRDigest(quote parsedQuote, replay *ReplayedLog, sig parsedSignature) error {
	hashAlg, err := algorithmForHash(sig.hash)
	if err != nil {
		return err
	}
	spec, err := hashSpecFor(hashAlg)
	if err != nil {
		return err
	}
	hasher := spec.crypto.New()
	for _, bank := range quote.selection {
		bankPCRs, ok := replay.PCRs[bank.Hash]
		if !ok {
			return ErrPCRDigestMismatch
		}
		for _, index := range bank.PCRs {
			if index < 0 || index >= len(bankPCRs) {
				return ErrPCRDigestMismatch
			}
			hasher.Write(bankPCRs[index])
		}
	}
	computed := hasher.Sum(nil)
	if !bytesEqual(computed, quote.pcrDigest) {
		return ErrPCRDigestMismatch
	}
	return nil
}

// matchIdentity compares the extended events in log order against every
// allowed sequence and returns the first exact match.
func matchIdentity(replay *ReplayedLog, allowed []AllowedSequence) (string, error) {
	actual := replayedMeasurements(replay)
	for _, candidate := range allowed {
		if sequenceEqual(actual, candidate.Measurements) {
			return candidate.DeviceID, nil
		}
	}
	return "", ErrNoMatchingIdentity
}

func replayedMeasurements(replay *ReplayedLog) []Measurement {
	out := make([]Measurement, 0, len(replay.Events))
	for _, event := range replay.Events {
		if event.EventType == evNoAction {
			continue
		}
		out = append(out, Measurement{
			PCRIndex:  event.PCRIndex,
			EventType: event.EventType,
			Digests:   event.Digests,
		})
	}
	return out
}

func sequenceEqual(actual, expected []Measurement) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i].PCRIndex != expected[i].PCRIndex ||
			actual[i].EventType != expected[i].EventType ||
			!digestsEqual(actual[i].Digests, expected[i].Digests) {
			return false
		}
	}
	return true
}

func digestsEqual(a, b []EventDigest) bool {
	if len(a) != len(b) {
		return false
	}
	lookup := make(map[AlgorithmID][]byte, len(a))
	for _, d := range a {
		lookup[d.Algorithm] = d.Digest
	}
	for _, d := range b {
		other, ok := lookup[d.Algorithm]
		if !ok || !bytesEqual(other, d.Digest) {
			return false
		}
	}
	return true
}
