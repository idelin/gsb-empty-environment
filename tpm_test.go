package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"testing"
)

func TestParseAKPublicRejectsNonSigningKey(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), testReader())
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw := encodeECCAKPublic(priv)
	// Attributes occupy overall offsets 6..9; TPMA_OBJECT.sign is bit 16,
	// which is the lowest bit of the second attribute byte in big-endian.
	raw[7] &^= 0x01

	if _, err := parseAKPublic(raw); !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("err = %v, want ErrMalformedInput", err)
	}
}

func TestParseQuoteRejectsBadMagic(t *testing.T) {
	req := validRequest(t)
	// The magic is the first field of the TPM2B body, at offset 2.
	req.Quote[2] ^= 0xFF

	if _, err := parseQuote(req.Quote); !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("err = %v, want ErrMalformedInput", err)
	}
}

func TestParseQuoteRejectsTrailingBytes(t *testing.T) {
	req := validRequest(t)
	req.Quote = append(req.Quote, 0xAA)

	if _, err := parseQuote(req.Quote); !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("err = %v, want ErrMalformedInput", err)
	}
}

func TestReplayRejectsOutOfRangePCR(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	var bad []byte
	bad = append(bad, log[:specIDHeaderEnd(log)]...)
	ev := replay.Events[0]
	bad = append(bad, u32(uint32(maxPCRIndex+1))...) // invalid PCR index
	bad = append(bad, u32(ev.EventType)...)
	bad = append(bad, u32(uint32(len(ev.Digests)))...)
	for _, d := range ev.Digests {
		bad = append(bad, u16(d.Algorithm)...)
		bad = append(bad, d.Digest...)
	}
	bad = append(bad, sized16(ev.EventData)...)

	if _, err := ReplayEventLog(bad); !errors.Is(err, ErrMalformedEventLog) {
		t.Fatalf("err = %v, want ErrMalformedEventLog", err)
	}
}

func TestReplayRejectsSpecIDTruncation(t *testing.T) {
	log := buildEventLog(fixtureBanks(), fixtureEvents())
	// Cut within the Spec ID event data itself.
	if _, err := ReplayEventLog(log[:specIDHeaderEnd(log)-5]); !errors.Is(err, ErrTruncatedEvent) {
		t.Fatalf("err = %v, want ErrTruncatedEvent", err)
	}
}

func TestSelectionEquality(t *testing.T) {
	a := []PCRBank{
		{Hash: AlgSHA256, PCRs: []int{0, 1, 2}},
		{Hash: AlgSHA1, PCRs: []int{7}},
	}
	b := []PCRBank{
		{Hash: AlgSHA1, PCRs: []int{7}},
		{Hash: AlgSHA256, PCRs: []int{2, 0, 1}},
	}
	if !equalSelection(a, b) {
		t.Fatal("bank order and PCR order should not affect equality")
	}
	c := []PCRBank{{Hash: AlgSHA256, PCRs: []int{0, 1}}}
	if equalSelection(a, c) {
		t.Fatal("selections with different PCRs must differ")
	}
}
