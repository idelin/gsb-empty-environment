package imgcodec

import (
	"math/rand"
	"testing"
)

func TestKnownOptima(t *testing.T) {
	tests := []struct {
		name string
		pix  []byte
		p    Params
		cost int
	}{
		{
			name: "solid image is one repeat",
			pix:  []byte{1, 1, 1, 1, 1, 1, 1, 1},
			p:    Params{Width: 4, Height: 2, LitHeader: 3, RepHeader: 3, RefHeader: 3, MaxRefLen: 4},
			cost: 4, // repeat(8)
		},
		{
			name: "identical rows use one reference",
			pix:  []byte{1, 2, 3, 4, 1, 2, 3, 4},
			p:    Params{Width: 4, Height: 2, LitHeader: 3, RepHeader: 3, RefHeader: 3, MaxRefLen: 4},
			cost: 10, // literal(4) + ref(4) = 7 + 3
		},
		{
			name: "cheap ref beats repeat mid-run",
			pix:  []byte{9, 9, 9, 9, 9, 9, 9, 9},
			p:    Params{Width: 4, Height: 2, LitHeader: 3, RepHeader: 10, RefHeader: 1, MaxRefLen: 4},
			cost: 8, // literal(4) + ref(4) = 7 + 1
		},
		{
			name: "single-pixel repeats beat an expensive literal",
			pix:  []byte{5, 6, 7},
			p:    Params{Width: 3, Height: 1, LitHeader: 5, RepHeader: 0, RefHeader: 0, MaxRefLen: 2},
			cost: 3, // repeat(1) x 3
		},
		{
			name: "max ref length forces a split",
			pix:  []byte{1, 2, 1, 2},
			p:    Params{Width: 2, Height: 2, LitHeader: 10, RepHeader: 10, RefHeader: 1, MaxRefLen: 1},
			cost: 14, // literal(2) + ref(1) + ref(1), tied with literal(4)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrs, err := Encode(tt.pix, tt.p)
			if err != nil {
				t.Fatal(err)
			}
			if got := tt.p.Cost(instrs); got != tt.cost {
				t.Fatalf("cost = %d, want %d (instrs %v)", got, tt.cost, instrs)
			}
			if got := bruteForceCost(tt.pix, tt.p); got != tt.cost {
				t.Fatalf("brute force = %d, want %d", got, tt.cost)
			}
		})
	}
}

func TestReferenceInstructionShape(t *testing.T) {
	pix := []byte{1, 2, 3, 4, 1, 2, 3, 4}
	p := DefaultParams(4, 2)
	instrs, err := Encode(pix, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(instrs) != 2 || instrs[0].Op != OpLiteral || instrs[0].Len != 4 ||
		instrs[1].Op != OpRef || instrs[1].Len != 4 {
		t.Fatalf("unexpected instructions %v", instrs)
	}
}

func TestWireLengthMatchesCost(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 500; trial++ {
		w, h := 1+rng.Intn(8), 1+rng.Intn(6)
		p := DefaultParams(w, h)
		pix := randomImage(rng, w, h)
		instrs, err := Encode(pix, p)
		if err != nil {
			t.Fatal(err)
		}
		data, err := Marshal(instrs)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != p.Cost(instrs) {
			t.Fatalf("trial %d: wire length %d != cost %d", trial, len(data), p.Cost(instrs))
		}
	}
}

func TestEncodeRejectsBadInput(t *testing.T) {
	p := DefaultParams(2, 2)
	if _, err := Encode([]byte{1, 2, 3}, p); err == nil {
		t.Fatal("expected pixel-count error")
	}
	if _, err := Encode([]byte{1, 2, 3, 4}, Params{Width: 0, Height: 2, MaxRefLen: 1}); err == nil {
		t.Fatal("expected dimension error")
	}
	if _, err := Encode([]byte{1, 2, 3, 4}, Params{Width: 2, Height: 2, MaxRefLen: 0}); err == nil {
		t.Fatal("expected MaxRefLen error")
	}
	if _, err := Encode([]byte{1, 2, 3, 4}, Params{Width: 2, Height: 2, LitHeader: -1, MaxRefLen: 1}); err == nil {
		t.Fatal("expected negative header error")
	}
}
