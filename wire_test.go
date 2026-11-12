package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestAuthMessageRoundTrip(t *testing.T) {
	msg := AuthMessage{
		Command: CmdAuthET,
		Status:  StatusInProgress,
		Proto:   ProtoDHCHAP,
		Params: []Param{
			{Type: ParamHashFunction, Value: []byte{byte(HashSHA256), byte(HashSHA512)}},
			{Type: ParamDHGroup, Value: []byte{byte(DHGroup2048)}},
			{Type: ParamChallenge, Value: bytes.Repeat([]byte{0xAB}, 18)}, // exercises padding
		},
	}
	raw, err := MarshalAuthMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAuthMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Command != msg.Command || parsed.Status != msg.Status || parsed.Proto != msg.Proto {
		t.Fatal("fixed header mismatch")
	}
	hashes, ok := parsed.HashOffers()
	if !ok || len(hashes) != 2 || hashes[0] != HashSHA256 {
		t.Fatalf("hash offers mismatch: %v", hashes)
	}
	groups, ok := parsed.DHGroupOffers()
	if !ok || groups[0] != DHGroup2048 {
		t.Fatalf("group offers mismatch: %v", groups)
	}
	ch, ok := parsed.GetChallenge()
	if !ok || len(ch) != 18 {
		t.Fatalf("challenge mismatch: %v", ch)
	}

	// Re-encoding must be byte-identical.
	raw2, _ := MarshalAuthMessage(parsed)
	if !bytes.Equal(raw, raw2) {
		t.Fatal("marshalling is not stable across parse round-trip")
	}
}

func TestParseAuthMessageRejectsGarbage(t *testing.T) {
	good, err := MarshalAuthMessage(AuthMessage{
		Command: CmdAuthET,
		Status:  StatusInProgress,
		Proto:   ProtoDHCHAP,
		Params:  []Param{{Type: ParamChallenge, Value: bytes.Repeat([]byte{1}, 17)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	bad := make([][]byte, 0)
	bad = append(bad, []byte{1, 2, 3}) // shorter than header

	declared := make([]byte, len(good))
	copy(declared, good)
	declared[4] ^= 0xFF // corrupt TLV length field
	bad = append(bad, declared)

	// Non-zero padding byte.
	padded := make([]byte, len(good))
	copy(padded, good)
	padded[len(padded)-1] = 0x01
	bad = append(bad, padded)

	// Truncated frame.
	bad = append(bad, good[:len(good)-2])

	for i, frame := range bad {
		if _, err := ParseAuthMessage(frame); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("case %d: expected ErrInvalidMessage, got %v", i, err)
		}
	}
}
