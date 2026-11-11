//go:build darwin || linux

package netsec

import (
	"errors"
	"testing"
)

func TestCodePageRetiresThroughRealMprotect(t *testing.T) {
	code := make([]byte, 16)
	page, err := NewCodePage(code)
	if err != nil {
		t.Skipf("anonymous executable mapping unavailable in sandbox: %v", err)
	}
	defer func() { _ = page.Release() }()

	target := page.Interval()
	if !target.Valid() {
		t.Fatalf("invalid page interval: %+v", target)
	}

	prot := page
	c := NewCoordinator(prot)
	_ = c.RegisterThread(1)
	gen, err := c.BeginRetirement(target)
	if err != nil {
		t.Fatal(err)
	}

	// A report showing execution inside the page blocks the downgrade.
	inPage := Report{
		ThreadID:         1,
		Generation:       gen,
		InstructionRange: Interval{Start: target.Start, End: target.Start + 4},
	}
	if err := c.ReportAtSafepoint(inPage); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); !errors.Is(err, ErrPageReferenced) {
		t.Fatalf("in-page report err = %v, want ErrPageReferenced", err)
	}

	// A clean same-generation report allows the real mprotect to RW.
	clean := safeReport(1, gen)
	clean.InstructionRange = Interval{Start: target.End + 0x1000, End: target.End + 0x2000}
	if err := c.ReportAtSafepoint(clean); err != nil {
		t.Fatal(err)
	}
	if err := c.TryRetire(); err != nil {
		t.Fatalf("downgrade through mprotect: %v", err)
	}

	// Writing now succeeds because the mapping is writable again.
	if err := page.Write(make([]byte, 8)); err != nil {
		t.Fatalf("write after retirement: %v", err)
	}
}
