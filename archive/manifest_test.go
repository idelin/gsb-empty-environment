package archive

import (
	"errors"
	"testing"
)

func TestKitchenResolveMatchesReference(t *testing.T) {
	for root := NodeID(0); root < 20; root++ {
		m := kitchenManifest(t, root, 1)
		full := refExpand(t, m, root, 1<<20)
		if uint64(len(full)) != m.TotalLength {
			t.Fatalf("root %d reference length %d != manifest %d", root, len(full), m.TotalLength)
		}
		max := uint64(len(full))
		starts := []uint64{0}
		ends := []uint64{0, 1}
		if max > 1 {
			starts = append(starts, 1, 2, max/2, max-1)
			ends = append(ends, max/2, max-1, max)
		}
		for _, a := range starts {
			for _, b := range ends {
				if a > b || b > max {
					continue
				}
				ps := mustResolve(t, m, a, b)
				got := materializeResolved(t, ps, kitchenData())
				want := full[a:b]
				if !bytesEqual(got, want) {
					t.Fatalf("root %d [%d,%d): got %q want %q", root, a, b, got, want)
				}
			}
		}
	}
}

func TestReverseAcrossBlockAndRepeatBoundaries(t *testing.T) {
	m := kitchenManifest(t, 8, 1) // reverse of raw|dict|dict concat
	for _, tc := range []struct{ a, b uint64 }{
		{0, 1}, {0, 2}, {2, 6}, {3, 11}, {0, m.TotalLength}, {m.TotalLength - 1, m.TotalLength},
	} {
		ps := mustResolve(t, m, tc.a, tc.b)
		got := materializeResolved(t, ps, kitchenData())
		full := refExpand(t, m, 8, 1<<20)
		if !bytesEqual(got, full[tc.a:tc.b]) {
			t.Fatalf("[%d,%d): got %q want %q", tc.a, tc.b, got, full[tc.a:tc.b])
		}
	}

	m = kitchenManifest(t, 12, 1) // reverse of repeated slice
	full := refExpand(t, m, 12, 1<<20)
	for a := uint64(0); a <= m.TotalLength; a++ {
		for b := a; b <= m.TotalLength; b++ {
			ps := mustResolve(t, m, a, b)
			got := materializeResolved(t, ps, kitchenData())
			if !bytesEqual(got, full[a:b]) {
				t.Fatalf("repeat/reverse [%d,%d): got %q want %q", a, b, got, full[a:b])
			}
		}
	}
}

func TestRepeatAtTwoToThe48IsCompact(t *testing.T) {
	const total = uint64(1 << 48)
	m := &Manifest{
		Version:        1,
		Strategy:       "rle",
		StrategyDigest: strategyDigest("rle"),
		Root:           1,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewRepeatNode(1, 0, total),
		},
		Blocks: []Block{{ID: 1, Size: 1, Codec: "rle"}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if m.TotalLength != total {
		t.Fatalf("total = %d", m.TotalLength)
	}
	ps := mustResolve(t, m, 0, total)
	if len(ps) != 1 || ps[0].IsLeaf() {
		t.Fatalf("expected one compact repeat piece, got %d pieces (%d nodes)", len(ps), countPieces(ps))
	}
	if ps[0].Count != total || ps[0].Pattern != 1 {
		t.Fatalf("repeat piece count=%d pattern=%d", ps[0].Count, ps[0].Pattern)
	}
	if got := SourceBlocks(ps); len(got) != 1 || got[0] != 1 {
		t.Fatalf("source blocks %v", got)
	}

	tail := mustResolve(t, m, total-3, total)
	got := materializeResolved(t, tail, map[BlockID][]byte{1: []byte("A")})
	if string(got) != "AAA" {
		t.Fatalf("tail = %q", got)
	}
}

func TestRepeatOverflowRejected(t *testing.T) {
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           1,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewRepeatNode(1, 0, 1<<47+1),
		},
		Blocks: []Block{{ID: 1, Size: 2}},
	}
	if err := m.SealFromRoot(); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestRepeatedMultiBlockSubtreeStaysCompact(t *testing.T) {
	const childCount = uint64(1 << 47) // child is two bytes, total = 2^48
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("rle"),
		Root:           3,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewRawNode(1, 2),
			NewConcatNode(2, 0, 1),
			NewRepeatNode(3, 2, childCount),
		},
		Blocks: []Block{{ID: 1, Size: 1}, {ID: 2, Size: 1}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if m.TotalLength != 1<<48 {
		t.Fatalf("total = %d", m.TotalLength)
	}
	ps := mustResolve(t, m, 0, m.TotalLength)
	if len(ps) != 1 || ps[0].Count != childCount || len(ps[0].Parts) != 2 {
		t.Fatalf("resolved %d pieces (%d nodes)", len(ps), countPieces(ps))
	}
	if got := SourceBlocks(ps); len(got) != 2 {
		t.Fatalf("source blocks = %v", got)
	}
}

func TestManifestSignatureBindsRequiredFields(t *testing.T) {
	base := kitchenManifest(t, 7, 1)
	if base.ContentRoot.IsZero() || base.Signature.IsZero() {
		t.Fatal("seal did not populate digests")
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("fresh manifest should verify: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"version", func(m *Manifest) { m.Version = 2 }},
		{"root", func(m *Manifest) { m.Root = 8 }}, // same total length, different root
		{"strategy digest", func(m *Manifest) { m.StrategyDigest = strategyDigest("other") }},
		{"block content", func(m *Manifest) { m.Blocks[0].Codec = "zip" }},
		{"node offset", func(m *Manifest) { m.Nodes[6].Off = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base.Clone()
			tc.mutate(m)
			err := m.Validate()
			if err == nil {
				t.Fatal("tampering was not detected")
			}
			if tc.name == "version" || tc.name == "root" || tc.name == "strategy digest" || tc.name == "block content" || tc.name == "node offset" {
				if !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrInvalidManifest) {
					t.Fatalf("unexpected error kind: %v", err)
				}
			}
		})
	}

	m := base.Clone()
	m.Version = 2
	if err := m.Seal(); err != nil {
		t.Fatalf("reseal: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("resealed manifest should verify: %v", err)
	}
}

func TestMalformedManifestsRejected(t *testing.T) {
	mk := func(nodes []Node) *Manifest {
		return &Manifest{
			Version:        1,
			StrategyDigest: strategyDigest("x"),
			Root:           0,
			Nodes:          nodes,
			Blocks:         []Block{{ID: 1, Size: 4}},
		}
	}
	cases := map[string]*Manifest{
		"missing block":      mk([]Node{NewRawNode(0, 9)}),
		"dict out of range":  mk([]Node{NewDictNode(0, 1, 2, 3)}),
		"slice out of range": mk([]Node{NewRawNode(0, 1), NewSliceNode(1, 0, 3, 2)}),
		"cycle":              mk([]Node{NewConcatNode(0, 0)}),
		"zero repeat":        mk([]Node{NewRawNode(0, 1), NewRepeatNode(1, 0, 0)}),
		"empty child repeat": mk([]Node{NewDictNode(0, 1, 0, 0), NewRepeatNode(1, 0, 3)}),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "slice out of range" {
				m.Root = 1
			}
			if name == "zero repeat" || name == "empty child repeat" {
				m.Root = 1
			}
			if err := m.SealFromRoot(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	m := mk([]Node{NewRawNode(0, 1)})
	m.TotalLength = 3
	if err := m.Seal(); err == nil {
		t.Fatal("expected total-length mismatch")
	}
}
