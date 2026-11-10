package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"testing"
)

func ctrKeystream(t *testing.T, keyHex, nonceHex string, startBlock uint64, blockOffset, length int) []byte {
	t.Helper()
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	prefixLen := startBlock*blockSize + uint64(blockOffset)
	stream := make([]byte, prefixLen+uint64(length))
	counter := make([]byte, block.BlockSize())
	copy(counter, nonce)
	cipher.NewCTR(block, counter).XORKeyStream(stream, make([]byte, len(stream)))
	return stream[prefixLen:]
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func mustHex(t *testing.T, b []byte) string {
	t.Helper()
	return hex.EncodeToString(b)
}

const (
	keyA  = "000102030405060708090a0b0c0d0e0f"
	keyB  = "101112131415161718191a1b1c1d1e1f"
	nonce = "aa000000000000000000000000000000"
)

func findFragment(rep *report, keyHex, nonceHex, id string) *fragmentReport {
	for g := range rep.Groups {
		if rep.Groups[g].KeyID == keyHex && rep.Groups[g].Nonce == nonceHex {
			for f := range rep.Groups[g].Fragments {
				if rep.Groups[g].Fragments[f].FragmentID == id {
					return &rep.Groups[g].Fragments[f]
				}
			}
		}
	}
	return nil
}

func findRecovered(fr *fragmentReport, offset int) *recoveredByte {
	for i := range fr.Recovered {
		if fr.Recovered[i].Offset == offset && !fr.Recovered[i].Conflict {
			return &fr.Recovered[i]
		}
	}
	return nil
}

func TestPropagationAcrossOverlap(t *testing.T) {
	ks := ctrKeystream(t, keyA, nonce, 0, 0, 48)
	plainA := []byte("HELLO-CTR-MODE!!")
	plainB := []byte("second-record!!!")
	cipherA := xorBytes(ks[0:16], plainA)
	cipherB := xorBytes(ks[8:24], plainB)

	rep, err := analyze([]fragment{
		{
			ID: "a", KeyID: keyA, Nonce: nonce,
			StartBlock: 0, BlockOffset: 0,
			Ciphertext:     mustHex(t, cipherA),
			KnownPlaintext: []knownPlain{{Offset: 0, Data: mustHex(t, plainA)}},
		},
		{
			ID: "b", KeyID: keyA, Nonce: nonce,
			StartBlock: 0, BlockOffset: 8,
			Ciphertext: mustHex(t, cipherB),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(rep.Groups))
	}
	group := rep.Groups[0]
	if len(group.Overlaps) != 1 || group.Overlaps[0].OverlapBytes != 8 {
		t.Fatalf("unexpected overlaps: %+v", group.Overlaps)
	}
	if group.ConflictCount != 0 {
		t.Fatalf("unexpected conflicts: %d", group.ConflictCount)
	}

	frB := findFragment(rep, keyA, nonce, "b")
	if frB == nil {
		t.Fatal("fragment b missing")
	}
	if frB.KnownCount != 0 || frB.RecoveredCount != 8 {
		t.Fatalf("b known=%d recovered=%d, want 0/8", frB.KnownCount, frB.RecoveredCount)
	}
	for off := 0; off < 8; off++ {
		rb := findRecovered(frB, off)
		if rb == nil {
			t.Fatalf("missing recovered byte at b offset %d", off)
		}
		if rb.FromKnownPlaintext || rb.Source.FragmentID != "a" {
			t.Fatalf("b offset %d not propagated from a: %+v", off, rb.Source)
		}
		want := hex.EncodeToString(plainB[off : off+1])
		if rb.PlaintextByte != want {
			t.Fatalf("b offset %d plaintext=%s want %s", off, rb.PlaintextByte, want)
		}
	}
}

func TestTransitivePropagation(t *testing.T) {
	ks := ctrKeystream(t, keyA, nonce, 0, 0, 48)
	plainA := []byte("AAAA")
	cipherA := xorBytes(ks[0:4], plainA)
	cipherB := xorBytes(ks[2:10], []byte("BBBBBBBB"))
	cipherC := xorBytes(ks[8:12], []byte("CCCC"))

	rep, err := analyze([]fragment{
		{ID: "a", KeyID: keyA, Nonce: nonce, StartBlock: 0, BlockOffset: 0,
			Ciphertext: mustHex(t, cipherA), KnownPlaintext: []knownPlain{{Offset: 0, Data: mustHex(t, plainA)}}},
		{ID: "b", KeyID: keyA, Nonce: nonce, StartBlock: 0, BlockOffset: 2, Ciphertext: mustHex(t, cipherB)},
		{ID: "c", KeyID: keyA, Nonce: nonce, StartBlock: 0, BlockOffset: 8, Ciphertext: mustHex(t, cipherC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	frB := findFragment(rep, keyA, nonce, "b")
	if frB.RecoveredCount != 2 {
		t.Fatalf("b recovered=%d want 2 (positions 2-3)", frB.RecoveredCount)
	}
	frC := findFragment(rep, keyA, nonce, "c")
	if frC.RecoveredCount != 0 {
		t.Fatalf("c recovered=%d want 0, no shared position with known keystream", frC.RecoveredCount)
	}
}

func TestNoPropagationAcrossKeyOrNonce(t *testing.T) {
	ksA := ctrKeystream(t, keyA, nonce, 1, 4, 16)
	ksOther := ctrKeystream(t, keyB, nonce, 1, 4, 16)
	plainA := []byte("same-plaintext!!")
	cipherA := xorBytes(ksA, plainA)
	cipherOther := xorBytes(ksOther, []byte("other-record-xyz"))

	rep, err := analyze([]fragment{
		{ID: "a", KeyID: keyA, Nonce: nonce, StartBlock: 1, BlockOffset: 4,
			Ciphertext: mustHex(t, cipherA), KnownPlaintext: []knownPlain{{Offset: 0, Data: mustHex(t, plainA)}}},
		{ID: "b", KeyID: keyB, Nonce: nonce, StartBlock: 1, BlockOffset: 4, Ciphertext: mustHex(t, cipherOther)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 2 {
		t.Fatalf("want 2 isolated groups, got %d", len(rep.Groups))
	}
	frB := findFragment(rep, keyB, nonce, "b")
	if frB == nil {
		t.Fatal("fragment b missing")
	}
	if frB.RecoveredCount != 0 || len(frB.Recovered) != 0 {
		t.Fatalf("cross-key propagation leaked: %+v", frB.Recovered)
	}
}

func TestConflictKeepsBothEvidences(t *testing.T) {
	ks := ctrKeystream(t, keyA, nonce, 0, 0, 16)
	realPlain := make([]byte, 4)
	wrongPlain := []byte{0x01, 0x02, 0x03, 0x04}
	cipherA := xorBytes(ks[0:4], realPlain)
	cipherB := xorBytes(ks[0:4], realPlain)

	rep, err := analyze([]fragment{
		{ID: "a", KeyID: keyA, Nonce: nonce, StartBlock: 0, BlockOffset: 0,
			Ciphertext: mustHex(t, cipherA), KnownPlaintext: []knownPlain{{Offset: 0, Data: mustHex(t, realPlain)}}},
		{ID: "b", KeyID: keyA, Nonce: nonce, StartBlock: 0, BlockOffset: 0,
			Ciphertext: mustHex(t, cipherB), KnownPlaintext: []knownPlain{{Offset: 0, Data: mustHex(t, wrongPlain)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	group := rep.Groups[0]
	if group.ConflictCount == 0 {
		t.Fatal("expected conflict positions")
	}
	conflicting := 0
	for _, entry := range group.Keystream {
		if entry.Conflict {
			conflicting++
			if len(entry.Candidates) != 2 {
				t.Fatalf("position %d keeps %d candidates, want 2", entry.Position, len(entry.Candidates))
			}
			sources := map[string]bool{}
			for _, c := range entry.Candidates {
				sources[c.Evidence.FragmentID] = true
			}
			if !sources["a"] || !sources["b"] {
				t.Fatalf("conflict missing evidence: %v", sources)
			}
		}
	}
	if conflicting != 4 {
		t.Fatalf("want 4 conflict positions, got %d", conflicting)
	}
}

func TestBlockOffsetPositionMapping(t *testing.T) {
	rep, err := analyze([]fragment{
		{ID: "x", KeyID: keyA, Nonce: nonce, StartBlock: 3, BlockOffset: 5,
			Ciphertext: mustHex(t, make([]byte, 20))},
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := findFragment(rep, keyA, nonce, "x")
	if fr.AbsStart != 3*16+5 || fr.AbsEnd != 3*16+5+20 {
		t.Fatalf("unexpected range [%d..%d)", fr.AbsStart, fr.AbsEnd)
	}
}
