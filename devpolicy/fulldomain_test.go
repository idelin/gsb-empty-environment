package devpolicy

import "testing"

// TestFullDomainNoEnumeration exercises the real 12-bit device-number space.
// The diagram must stay a small rectangle partition even though the universe
// contains 2*4096*4096 devices; we assert the cell count is bounded by the
// rule-induced slab grid rather than by the universe size.
func TestFullDomainNoEnumeration(t *testing.T) {
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitR | BitM, Name: "all-rm"},
		{Layer: 0, Order: 1, Effect: EffectAllow, Type: TypeBlock, MajorMin: 8, MajorMax: 255, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitW, Name: "blk-w"},
		{Layer: 0, Order: 2, Effect: EffectDeny, Type: TypeBlock, MajorMin: 10, MajorMax: 10, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: MaskAll, Name: "host-blk"},
		{Layer: 1, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitM, Name: "child-m"},
		{Layer: 1, Order: 1, Effect: EffectDeny, Type: TypeChar, MajorMin: 100, MajorMax: 200, MinorMin: 50, MinorMax: 60, Perm: BitM, Name: "child-m-deny"},
	}
	compiled, err := (&Policy{Rules: rules}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	for idx, typ := range orderedTypes() {
		cells := len(compiled.Effective[idx].Partition)
		if cells == 0 || cells > 256 {
			t.Fatalf("type %c diagram has %d cells, want a small symbolic partition", typ, cells)
		}
		assertCover(t, compiled.Effective[idx].Partition, MaxDeviceNo)
	}
	// Host block major 10 must be fully denied despite the child's m grant.
	if got := compiled.EffectiveMask(TypeBlock, 10, 0); got != 0 {
		t.Fatalf("host block leaked: 0b%03b", got)
	}
	// Root grants r+m while the child keeps only m; nesting must suppress r and
	// the standalone m must never imply r/w. Pick a point outside the child's
	// m-deny window.
	if got := compiled.EffectiveMask(TypeChar, 300, 300); got != BitM {
		t.Fatalf("unexpected char mask: 0b%03b want m only", got)
	}
	// Inside the child's m-deny window even m disappears.
	if got := compiled.EffectiveMask(TypeChar, 150, 55); got != 0 {
		t.Fatalf("child m-deny not honored: 0b%03b", got)
	}
}

func BenchmarkCompileFullDomain(b *testing.B) {
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 0, MajorMax: MaxDeviceNo, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitR | BitW, Name: "base"},
		{Layer: 0, Order: 1, Effect: EffectDeny, Type: TypeBlock, MajorMin: 10, MajorMax: 20, MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitW, Name: "d1"},
		{Layer: 1, Order: 0, Effect: EffectAllow, Type: TypeAll, MajorMin: 100, MajorMax: 4000, MinorMin: 5, MinorMax: 4090, Perm: BitM, Name: "c1"},
	}
	policy := &Policy{Rules: rules}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := policy.Compile(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestUpperBoundNoWraparound places rectangles exactly on the 4095 edge to
// guard against uint16 overflow when forming exclusive interval edges.
func TestUpperBoundNoWraparound(t *testing.T) {
	rules := []Rule{
		{Layer: 0, Order: 0, Effect: EffectAllow, Type: TypeChar,
			MajorMin: MaxDeviceNo, MajorMax: MaxDeviceNo,
			MinorMin: MaxDeviceNo, MinorMax: MaxDeviceNo, Perm: BitR, Name: "corner"},
		{Layer: 0, Order: 1, Effect: EffectAllow, Type: TypeChar,
			MajorMin: 0, MajorMax: MaxDeviceNo,
			MinorMin: 0, MinorMax: MaxDeviceNo, Perm: BitM, Name: "all-m"},
	}
	compiled, err := (&Policy{Rules: rules}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	assertCover(t, compiled.Effective[typeIndex(TypeChar)].Partition, MaxDeviceNo)
	if got := compiled.EffectiveMask(TypeChar, MaxDeviceNo, MaxDeviceNo); got != BitR|BitM {
		t.Fatalf("upper corner mask 0b%03b want r|m", got)
	}
	if got := compiled.EffectiveMask(TypeChar, 0, 0); got != BitM {
		t.Fatalf("origin mask 0b%03b want m", got)
	}
}
