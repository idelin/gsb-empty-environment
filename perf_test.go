package imgcodec

import (
	"math/rand"
	"testing"
)

func TestMediumImagePerformance(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	p := DefaultParams(320, 240)
	pix := randomImage(rng, p.Width, p.Height)
	instrs, err := Encode(pix, p)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(instrs)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data, p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pix {
		if back[i] != pix[i] {
			t.Fatalf("mismatch at %d", i)
		}
	}
	t.Logf("320x240: %d pixels -> %d bytes in %d instructions", len(pix), len(data), len(instrs))
}
