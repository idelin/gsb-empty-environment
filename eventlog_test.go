package netsec

import (
	"errors"
	"testing"
)

// specIDHeaderEnd returns the byte offset at which agile events begin.
func specIDHeaderEnd(log []byte) int {
	// 4 (PCRIndex) + 4 (EventType) + 20 (digest); eventDataSize is at 28.
	const sizeField = 28
	size := uint32(log[sizeField])<<24 | uint32(log[sizeField+1])<<16 |
		uint32(log[sizeField+2])<<8 | uint32(log[sizeField+3])
	return sizeField + 4 + int(size)
}

func TestReplayExtendOrder(t *testing.T) {
	events := fixtureEvents()
	log := buildEventLog(fixtureBanks(), events)
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// Independently recompute PCR0 in both banks using the extend rule.
	for _, alg := range fixtureBanks() {
		spec, _ := hashSpecFor(alg)
		expected := make([]byte, spec.size)
		for _, ev := range []rawEvent{events[0], events[2]} {
			hasher := spec.crypto.New()
			hasher.Write(expected)
			h2 := spec.crypto.New()
			h2.Write(ev.data)
			hasher.Write(h2.Sum(nil))
			expected = hasher.Sum(nil)
		}
		got := replay.PCRs[alg][0]
		if !bytesEqual(got, expected) {
			t.Fatalf("PCR0 bank %v mismatch", alg)
		}
	}
}

func TestReplayTruncatedHeader(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	for _, cut := range []int{1, 8, 20, 31} {
		if _, err := ReplayEventLog(log[:cut]); !errors.Is(err, ErrTruncatedEvent) {
			t.Fatalf("cut=%d: err=%v, want ErrTruncatedEvent", cut, err)
		}
	}
}

func TestReplayTruncatedAgileEvent(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	headerEnd := specIDHeaderEnd(log)

	// Walk complete event boundaries; every cut strictly between two
	// boundaries must be rejected as a truncated entry.
	boundary := headerEnd
	for _, ev := range replay.Events {
		next := boundary + agileEventLength(ev)
		for cut := boundary + 1; cut < next; cut++ {
			if _, err := ReplayEventLog(log[:cut]); err == nil {
				t.Fatalf("cut=%d inside event accepted", cut)
			}
		}
		// The full prefix up to the boundary must remain replayable.
		if _, err := ReplayEventLog(log[:next]); err != nil {
			t.Fatalf("boundary cut=%d rejected: %v", next, err)
		}
		boundary = next
	}
	if boundary != len(log) {
		t.Fatalf("boundary=%d, log length=%d", boundary, len(log))
	}
}

func agileEventLength(ev LogEvent) int {
	length := 4 + 4 + 4 // PCRIndex, EventType, digestCount
	for _, d := range ev.Digests {
		spec, _ := hashSpecFor(d.Algorithm)
		length += 2 + spec.size
	}
	length += 2 + len(ev.EventData)
	return length
}

func TestReplayMissingDigestRejected(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	var tampered []byte
	tampered = append(tampered, log[:specIDHeaderEnd(log)]...)
	for _, ev := range replay.Events {
		tampered = append(tampered, u32(uint32(ev.PCRIndex))...)
		tampered = append(tampered, u32(ev.EventType)...)
		tampered = append(tampered, u32(1)...) // only one digest advertised
		for _, d := range ev.Digests {
			if d.Algorithm != AlgSHA256 {
				continue
			}
			tampered = append(tampered, u16(d.Algorithm)...)
			tampered = append(tampered, d.Digest...)
		}
		tampered = append(tampered, sized16(ev.EventData)...)
	}

	_, err = ReplayEventLog(tampered)
	if !errors.Is(err, ErrMissingDigest) {
		t.Fatalf("err = %v, want ErrMissingDigest", err)
	}
}

func TestReplayUnknownAlgorithmRejected(t *testing.T) {
	var specData []byte
	sig := make([]byte, 16)
	copy(sig, specIDSignature)
	specData = append(specData, sig...)
	specData = append(specData, 0, 0, 0, 0, 0, 2, 0, 2)
	specData = append(specData, 0, 1)       // one algorithm
	specData = append(specData, 0, 0)       // vendorInfoSize
	specData = append(specData, 0x00, 0x0C) // unknown algorithm id
	specData = append(specData, 0, 32)      // claimed digest size

	var log []byte
	log = append(log, u32(0)...)
	log = append(log, u32(evNoAction)...)
	log = append(log, make([]byte, 20)...)
	log = append(log, u32(uint32(len(specData)))...)
	log = append(log, specData...)

	_, err := ReplayEventLog(log)
	if !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestReplayBadSpecIDSignatureRejected(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	log[0] = 'X'

	_, err := ReplayEventLog(log)
	if !errors.Is(err, ErrMalformedEventLog) {
		t.Fatalf("err = %v, want ErrMalformedEventLog", err)
	}
}

func TestReplayBadDigestSizeRejected(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	// Spec ID starts after the 28-byte legacy header plus its 4-byte size
	// prefix; the first algorithm entry begins 28 bytes into the Spec ID
	// payload, so the SHA-1 digest size sits at offset 28+4+28+2.
	log[62] = 0x00
	log[63] = 0x13 // claim 19 bytes instead of 20

	_, err := ReplayEventLog(log)
	if !errors.Is(err, ErrMalformedEventLog) {
		t.Fatalf("err = %v, want ErrMalformedEventLog", err)
	}
}

func TestReplayWrongLegacyDigestRejected(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	log[10] = 0x01 // legacy Spec ID digest must be all zeros

	_, err := ReplayEventLog(log)
	if !errors.Is(err, ErrMalformedEventLog) {
		t.Fatalf("err = %v, want ErrMalformedEventLog", err)
	}
}

func TestReplaySHA256OnlyBank(t *testing.T) {
	log := buildEventLog([]AlgorithmID{AlgSHA256}, fixtureEvents())
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replay.PCRs) != 1 {
		t.Fatalf("banks = %d, want 1", len(replay.PCRs))
	}
	if len(replay.PCRs[AlgSHA256]) != maxPCRIndex+1 {
		t.Fatalf("PCR count = %d, want %d", len(replay.PCRs[AlgSHA256]), maxPCRIndex+1)
	}
}

func TestReplayNoActionEventNotExtended(t *testing.T) {
	// Append an agile EV_NO_ACTION event and ensure PCR values are unchanged.
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	replayBefore, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	var extra []byte
	extra = append(extra, u32(0)...)
	extra = append(extra, u32(evNoAction)...)
	extra = append(extra, u32(uint32(len(fixtureBanks())))...)
	for _, alg := range fixtureBanks() {
		spec, _ := hashSpecFor(alg)
		extra = append(extra, u16(alg)...)
		extra = append(extra, make([]byte, spec.size)...)
	}
	extra = append(extra, sized16([]byte("StartupLocality"))...)

	replayAfter, err := ReplayEventLog(append(log, extra...))
	if err != nil {
		t.Fatalf("replay with NO_ACTION: %v", err)
	}
	for _, alg := range fixtureBanks() {
		for i := range replayBefore.PCRs[alg] {
			if !bytesEqual(replayBefore.PCRs[alg][i], replayAfter.PCRs[alg][i]) {
				t.Fatalf("PCR %d/%d changed despite EV_NO_ACTION", alg, i)
			}
		}
	}
}
