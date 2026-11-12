package netsec

import (
	"bytes"
	"reflect"
	"testing"
)

var testKey = []byte("scheduler-hmac-key-0123456789")

// key returns an independent copy of testKey so tests that mutate a derived
// key cannot corrupt the shared global slice.
func key() []byte {
	return append([]byte(nil), testKey...)
}

func testParent() ParentAuth {
	return ParentAuth{
		Accessible: Box{Lo: []int64{0, 0}, Hi: []int64{8, 8}},
		Denied: []Box{
			{Lo: []int64{3, 3}, Hi: []int64{5, 5}},
		},
	}
}

func TestParentDigestStableAndOrderIndependent(t *testing.T) {
	parent := testParent()
	d1, err := parent.Digest()
	if err != nil {
		t.Fatal(err)
	}

	reordered := parent
	reordered.Denied = []Box{
		parent.Denied[0],
		{Lo: []int64{6, 6}, Hi: []int64{7, 7}},
	}
	// Add an unrelated extra denied box and ensure both orderings hash equal.
	parentWithExtra := parent
	parentWithExtra.Denied = []Box{
		{Lo: []int64{6, 6}, Hi: []int64{7, 7}},
		parent.Denied[0],
	}
	d2, err := reordered.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d3, err := parentWithExtra.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d2 != d3 {
		t.Fatal("digest must not depend on denied listing order")
	}

	changed := parent
	changed.Accessible = Box{Lo: []int64{0, 0}, Hi: []int64{9, 8}}
	d4, err := changed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d4 == d1 {
		t.Fatal("different geometry must produce different digest")
	}
	_ = d2
}

func TestMintVerifyRoundTrip(t *testing.T) {
	parent := testParent()
	region := Box{Lo: []int64{0, 0}, Hi: []int64{2, 2}}

	token, err := Mint(key(), parent, region)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !reflect.DeepEqual(token.Region(), region) {
		t.Fatalf("token region = %v; want %v", token.Region(), region)
	}
	if err := token.Verify(key(), parent); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	inside := Box{Lo: []int64{1, 1}, Hi: []int64{2, 2}}
	if err := token.VerifyFor(key(), parent, inside); err != nil {
		t.Fatalf("VerifyFor(inside): %v", err)
	}

	outside := Box{Lo: []int64{1, 1}, Hi: []int64{3, 2}}
	if err := token.VerifyFor(key(), parent, outside); err != ErrRegionOutside {
		t.Fatalf("VerifyFor(outside) = %v; want ErrRegionOutside", err)
	}

	disjoint := Box{Lo: []int64{5, 5}, Hi: []int64{6, 6}}
	if err := token.VerifyFor(key(), parent, disjoint); err != ErrRegionOutside {
		t.Fatalf("VerifyFor(disjoint) = %v; want ErrRegionOutside", err)
	}
}

func TestVerifyRejectsWrongParentAndTampering(t *testing.T) {
	parent := testParent()
	region := Box{Lo: []int64{0, 0}, Hi: []int64{2, 2}}
	token, err := Mint(key(), parent, region)
	if err != nil {
		t.Fatal(err)
	}

	other := parent
	other.Accessible = Box{Lo: []int64{0, 0}, Hi: []int64{7, 8}}
	if err := token.Verify(key(), other); err != ErrWrongParent {
		t.Fatalf("wrong parent: %v; want ErrWrongParent", err)
	}

	if err := token.Verify([]byte("a-different-key"), parent); err != ErrInvalidToken {
		t.Fatalf("wrong key: %v; want ErrInvalidToken", err)
	}

	// Tamper with the carried region and confirm the tag check fires.
	tampered := token
	tampered.region.Hi[0] = 8
	if err := tampered.Verify(key(), parent); err != ErrInvalidToken {
		t.Fatalf("tampered region: %v; want ErrInvalidToken", err)
	}

	// Tamper with the parent digest.
	tamperedDigest := token
	tamperedDigest.parentDigest[0] ^= 0xFF
	if err := tamperedDigest.Verify(key(), parent); err != ErrWrongParent {
		t.Fatalf("tampered digest: %v; want ErrWrongParent", err)
	}

	// Flip a tag bit.
	tamperedTag := token
	tamperedTag.tag = append([]byte(nil), token.tag...)
	tamperedTag.tag[0] ^= 0x01
	if err := tamperedTag.Verify(key(), parent); err != ErrInvalidToken {
		t.Fatalf("tampered tag: %v; want ErrInvalidToken", err)
	}
}

func TestTokenSerializationRoundTrip(t *testing.T) {
	parent := testParent()
	region := Box{Lo: []int64{-3, 0}, Hi: []int64{2, 4}}
	token, err := Mint(key(), parent, region)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := token.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var fromBinary Token
	if err := fromBinary.UnmarshalBinary(raw); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if err := fromBinary.Verify(key(), parent); err != nil {
		t.Fatalf("decoded binary token Verify: %v", err)
	}
	if !reflect.DeepEqual(fromBinary.Region(), region) {
		t.Fatalf("binary round trip region = %v; want %v", fromBinary.Region(), region)
	}

	text, err := token.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var fromText Token
	if err := fromText.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if err := fromText.Verify(key(), parent); err != nil {
		t.Fatalf("decoded text token Verify: %v", err)
	}

	for _, garbage := range [][]byte{nil, {}, []byte("short"), bytes.Repeat([]byte{0xFF}, 40)} {
		var bad Token
		if err := bad.UnmarshalBinary(garbage); err != ErrInvalidToken {
			t.Fatalf("UnmarshalBinary(%v) = %v; want ErrInvalidToken", garbage, err)
		}
	}
	if err := (&Token{}).UnmarshalText([]byte("!!!not-base64!!!")); err != ErrInvalidToken {
		t.Fatalf("bad base64: %v; want ErrInvalidToken", err)
	}
}

func TestMintRejectsBadInputs(t *testing.T) {
	parent := testParent()
	region := Box{Lo: []int64{0, 0}, Hi: []int64{2, 2}}

	if _, err := Mint(nil, parent, region); err == nil {
		t.Fatal("empty key must fail")
	}
	if _, err := Mint(key(), parent, Box{Lo: []int64{0}, Hi: []int64{2}}); err == nil {
		t.Fatal("dimension mismatch must fail")
	}
	if _, err := Mint(key(), parent, Box{Lo: []int64{0, 0}, Hi: []int64{0, 2}}); err == nil {
		t.Fatal("empty region must fail")
	}

	badParent := ParentAuth{
		Accessible: Box{Lo: []int64{0, 0}, Hi: []int64{8, 8}},
		Denied:     []Box{{Lo: []int64{0}, Hi: []int64{2}}},
	}
	if _, err := Mint(key(), badParent, region); err == nil {
		t.Fatal("denied box dimension mismatch must fail")
	}
}

func TestMintShardsEndToEnd(t *testing.T) {
	parent := ParentAuth{
		Accessible: Box{Lo: []int64{0, 0, 0}, Hi: []int64{6, 5, 4}},
		Denied: []Box{
			{Lo: []int64{2, 1, 1}, Hi: []int64{4, 3, 2}},
			{Lo: []int64{5, 4, 3}, Hi: []int64{6, 5, 4}},
		},
	}
	const maxVolume = 7

	tokens, err := MintShards(key(), parent, maxVolume)
	if err != nil {
		t.Fatalf("MintShards: %v", err)
	}
	if len(tokens) == 0 {
		t.Fatal("expected shards")
	}

	// Rebuild the partition from tokens and brute-force the full grid.
	shards := make([]Shard, len(tokens))
	coverage := map[string]int{}
	for i, token := range tokens {
		if err := token.Verify(key(), parent); err != nil {
			t.Fatalf("token %d Verify: %v", i, err)
		}
		region := token.Region()
		vol, err := region.Volume()
		if err != nil {
			t.Fatal(err)
		}
		if vol > maxVolume {
			t.Fatalf("token %d volume %d exceeds %d", i, vol, maxVolume)
		}
		shards[i] = Shard{Region: region}
		for _, p := range enumeratePoints(region) {
			key := pointKey(p)
			if prev, dup := coverage[key]; dup {
				t.Fatalf("point %v covered by tokens %d and %d", p, prev, i)
			}
			coverage[key] = i
		}
	}

	assertShardPartition(t, parent.Accessible, parent.Denied, shards, maxVolume)

	// A retry that asks for exactly the original shard must pass, while a
	// widened retry request must be rejected without minting a new token.
	first := tokens[0].Region()
	if err := tokens[0].VerifyFor(key(), parent, first); err != nil {
		t.Fatalf("retry of original shard: %v", err)
	}
	widened := Box{Lo: append([]int64(nil), first.Lo...), Hi: append([]int64(nil), first.Hi...)}
	widened.Hi[0]++
	if err := tokens[0].VerifyFor(key(), parent, widened); err != ErrRegionOutside {
		t.Fatalf("widened retry: %v; want ErrRegionOutside", err)
	}

	// Tokens are deterministic for the same inputs.
	fresh := tokens[0].Region()
	again, err := Mint(key(), parent, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.tag, tokens[0].tag) {
		t.Fatal("minting the same shard twice must yield the same tag")
	}
}
