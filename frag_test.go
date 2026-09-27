package mtusim

import "testing"

func TestFragmentBasic(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 128, Align: 8}
	frags, err := p.Fragmentize(7, 1, 300)
	if err != nil {
		t.Fatalf("Fragmentize: %v", err)
	}
	wantLens := []int{104, 104, 92}
	if len(frags) != len(wantLens) {
		t.Fatalf("got %d fragments, want %d", len(frags), len(wantLens))
	}
	off := 0
	for i, f := range frags {
		if f.Offset != off || f.Len != wantLens[i] {
			t.Errorf("frag %d: range [%d,%d), want offset %d len %d",
				i, f.Offset, f.Offset+f.Len, off, wantLens[i])
		}
		if f.PacketID != 7 || f.Epoch != 1 {
			t.Errorf("frag %d: identity (%d,%d), want (7,1)", i, f.PacketID, f.Epoch)
		}
		if f.WireSize() > p.MTU {
			t.Errorf("frag %d: wire size %d exceeds mtu", i, f.WireSize())
		}
		if f.Last != (i == len(frags)-1) {
			t.Errorf("frag %d: Last=%v", i, f.Last)
		}
		if !f.Last && f.Len%p.Align != 0 {
			t.Errorf("frag %d: non-final payload %d not aligned to %d", i, f.Len, p.Align)
		}
		off += f.Len
	}
	// The last fragment may be short of the alignment unit.
	if frags[2].Len%p.Align == 0 {
		t.Errorf("expected final fragment to demonstrate sub-alignment length, got %d", frags[2].Len)
	}
	if err := VerifyCoverage(p, 300, frags); err != nil {
		t.Errorf("VerifyCoverage: %v", err)
	}
}

func TestFragmentZeroLength(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 64, Align: 8}
	frags, err := p.Fragmentize(1, 1, 0)
	if err != nil {
		t.Fatalf("Fragmentize: %v", err)
	}
	if len(frags) != 1 || frags[0].Len != 0 || !frags[0].Last {
		t.Errorf("zero-length datagram: got %+v", frags)
	}
	if err := VerifyCoverage(p, 0, frags); err != nil {
		t.Errorf("VerifyCoverage: %v", err)
	}
}

func TestMTUBelowHeaderFailsDirectly(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 10, Align: 8}
	if _, err := p.FragmentCount(5); KindOf(err) != ErrKindMTUBelowHeader {
		t.Errorf("FragmentCount: got %v, want ErrKindMTUBelowHeader", err)
	}
	if _, err := p.Fragmentize(1, 1, 5); KindOf(err) != ErrKindMTUBelowHeader {
		t.Errorf("Fragmentize: got %v, want ErrKindMTUBelowHeader", err)
	}
}

func TestTinyMTUInfeasible(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 26, Align: 8} // payload budget 6 < align 8
	// Fits in a single frame: no alignment needed, legal.
	if n, err := p.FragmentCount(5); err != nil || n != 1 {
		t.Errorf("FragmentCount(5): got %d, %v; want 1, nil", n, err)
	}
	// Needs two frames: aligned unit is 0, explicitly infeasible.
	if _, err := p.FragmentCount(7); KindOf(err) != ErrKindInfeasible {
		t.Errorf("FragmentCount(7): got %v, want ErrKindInfeasible", err)
	}
	if _, err := p.Fragmentize(1, 1, 7); KindOf(err) != ErrKindInfeasible {
		t.Errorf("Fragmentize(7): got %v, want ErrKindInfeasible", err)
	}
}

func TestNegativeLengthForbidden(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 128, Align: 8}
	if _, err := p.FragmentCount(-1); KindOf(err) != ErrKindNegativeLength {
		t.Errorf("FragmentCount(-1): got %v, want ErrKindNegativeLength", err)
	}
	if _, err := p.Fragmentize(1, 1, -5); KindOf(err) != ErrKindNegativeLength {
		t.Errorf("Fragmentize(-5): got %v, want ErrKindNegativeLength", err)
	}
}

// The same datagram is re-planned across two successive MTU corrections;
// every epoch's fragments must tile the original byte range and carry
// distinct epochs so ranges stay traceable.
func TestRefragmentAcrossMultipleMTUCorrections(t *testing.T) {
	const total = 300
	steps := []struct {
		mtu  int
		want int // fragment count
	}{
		{128, 3}, // 104+104+92
		{68, 7},  // 48*6+12
		{60, 8},  // 40*7+20
	}
	for epoch, st := range steps {
		p := FragParams{HeaderLen: 20, MTU: st.mtu, Align: 8}
		frags, err := p.Fragmentize(42, epoch+1, total)
		if err != nil {
			t.Fatalf("epoch %d: %v", epoch+1, err)
		}
		if len(frags) != st.want {
			t.Errorf("epoch %d (mtu %d): %d fragments, want %d", epoch+1, st.mtu, len(frags), st.want)
		}
		cover := make([]bool, total)
		for _, f := range frags {
			if f.Epoch != epoch+1 {
				t.Errorf("fragment carries epoch %d, want %d", f.Epoch, epoch+1)
			}
			for i := f.Offset; i < f.Offset+f.Len; i++ {
				if cover[i] {
					t.Errorf("epoch %d: byte %d covered twice", epoch+1, i)
				}
				cover[i] = true
			}
		}
		for i, c := range cover {
			if !c {
				t.Errorf("epoch %d: byte %d of original datagram not covered", epoch+1, i)
			}
		}
		if err := VerifyCoverage(p, total, frags); err != nil {
			t.Errorf("epoch %d: %v", epoch+1, err)
		}
	}
}

func TestVerifyCoverageRejectsTampering(t *testing.T) {
	p := FragParams{HeaderLen: 20, MTU: 128, Align: 8}
	good, err := p.Fragmentize(1, 1, 300)
	if err != nil {
		t.Fatalf("Fragmentize: %v", err)
	}
	cases := map[string]func([]Fragment) []Fragment{
		"gap": func(fs []Fragment) []Fragment {
			fs[1].Offset += 8
			return fs
		},
		"overlap": func(fs []Fragment) []Fragment {
			fs[1].Offset -= 8
			return fs
		},
		"misaligned non-final": func(fs []Fragment) []Fragment {
			fs[0].Len -= 1
			fs[1].Offset -= 1
			return fs
		},
		"oversize frame": func(fs []Fragment) []Fragment {
			fs[0].Header = 200
			return fs
		},
		"wrong last flag": func(fs []Fragment) []Fragment {
			fs[0].Last = true
			return fs
		},
		"negative length": func(fs []Fragment) []Fragment {
			fs[1].Len = -4
			return fs
		},
	}
	for name, mutate := range cases {
		fs := make([]Fragment, len(good))
		copy(fs, good)
		if err := VerifyCoverage(p, 300, mutate(fs)); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
