package imgcodec

import (
	"bytes"
	"testing"
	"time"
)

func TestSolidImagePerformance(t *testing.T) {
	p := DefaultParams(320, 240)
	pix := bytes.Repeat([]byte{7}, p.Width*p.Height)
	start := time.Now()
	instrs, err := Encode(pix, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("solid 320x240: %d instructions, cost %d, %v", len(instrs), p.Cost(instrs), time.Since(start))
}
