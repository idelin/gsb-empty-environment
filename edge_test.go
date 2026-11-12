package netsec

import "testing"

func TestVolumeOverflow(t *testing.T) {
	// 2^40 * 2^40 = 2^80, far beyond int64.
	big := Box{
		Lo: []int64{0, 0},
		Hi: []int64{1 << 40, 1 << 40},
	}
	if _, err := big.Volume(); err == nil {
		t.Fatal("overflowing volume must error")
	}
	if _, err := Subtract(big); err == nil {
		t.Fatal("Subtract must reject overflowing accessible box")
	}

	parts := []Box{{Lo: []int64{0, 0}, Hi: []int64{1 << 40, 1 << 40}}}
	if _, err := Split(parts, 10); err == nil {
		t.Fatal("Split must reject overflowing input volume")
	}
}

func TestMalformedBoxes(t *testing.T) {
	bad := []Box{
		{Lo: nil, Hi: []int64{1}},
		{Lo: []int64{1}, Hi: []int64{1, 2}},
		{Lo: []int64{5}, Hi: []int64{4}},
		{Lo: []int64{}, Hi: []int64{}},
	}
	for _, b := range bad {
		if _, err := b.Volume(); err == nil {
			t.Errorf("malformed box %v must fail Volume", b)
		}
		if _, err := Subtract(b); err == nil {
			t.Errorf("malformed box %v must fail Subtract", b)
		}
		if _, err := Split([]Box{b}, 1); err == nil {
			t.Errorf("malformed box %v must fail Split", b)
		}
	}

	access := Box{Lo: []int64{0}, Hi: []int64{4}}
	if _, err := Subtract(access, Box{Lo: []int64{0, 0}, Hi: []int64{1, 1}}); err == nil {
		t.Fatal("dimension-mismatched denied box must fail")
	}
}

func TestSubtractOneDimensional(t *testing.T) {
	access := Box{Lo: []int64{-5}, Hi: []int64{10}}
	denied := []Box{
		{Lo: []int64{-2}, Hi: []int64{3}},
		{Lo: []int64{8}, Hi: []int64{10}}, // touches the upper boundary
		{Lo: []int64{-100}, Hi: []int64{-90}},
	}
	parts, err := Subtract(access, denied...)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, access, denied, parts)

	// The 1D result must tile contiguously around the denied interval.
	want := []Box{
		{Lo: []int64{-5}, Hi: []int64{-2}},
		{Lo: []int64{3}, Hi: []int64{8}},
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts; want %d (%v)", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i].Lo[0] != want[i].Lo[0] || parts[i].Hi[0] != want[i].Hi[0] {
			t.Fatalf("part %d = %v; want %v", i, parts[i], want[i])
		}
	}
}

func TestTokenParentDigestAccessor(t *testing.T) {
	parent := testParent()
	region := Box{Lo: []int64{0, 0}, Hi: []int64{1, 1}}
	token, err := Mint(key(), parent, region)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := parent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if token.ParentDigest() != digest {
		t.Fatal("ParentDigest accessor must match the parent digest")
	}

	// Region returns an independent copy.
	got := token.Region()
	got.Lo[0] = 999
	if token.Region().Lo[0] != region.Lo[0] {
		t.Fatal("Region must return a defensive copy")
	}
}

func TestMarshalRejectsZeroValueToken(t *testing.T) {
	var zero Token
	if _, err := zero.MarshalBinary(); err != ErrInvalidToken {
		t.Fatalf("zero token MarshalBinary = %v; want ErrInvalidToken", err)
	}
}
