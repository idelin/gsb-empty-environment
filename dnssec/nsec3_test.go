package dnssec

import "testing"

// NSEC3 hashes computed by an independent reference implementation
// (SHA-1 wire-name/salt iteration per RFC 5155 section 3.1.4, salt aabbccdd).
func TestNSEC3HashVectors(t *testing.T) {
	salt := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	cases := []struct {
		name       string
		iterations uint16
		wantHex    string
		wantB32    string
	}{
		{"example.", 0, "6b452789c6451ac63cc30a04e7187cefb8db4e4c", "dd2if2e68kdccf63182ee63stusdmjic"},
		{"a.example.", 1, "3132c1fec17c4982d417bd223b8b427b9b9658cf", "64pc3vm1fh4o5l0nnkh3n2q2fedpcm6f"},
		{"ai.example.", 2, "db6fdc251038597ad0717a4c164661139cfb9963", "rdnto98g71cnlk3hf961chj12eefn6b3"},
		{"d.example.", 3, "72b5942eff4dd6766f879ec6eda5858ff645b327", "eaqp8bnv9nb7crs7jr3er9c5hvr4bcp7"},
		{"*.example.", 6, "00b968ccd1fee9608f8d0d333604635d093a7f40", "02smhj6hvrkm13sd1kpjc133bk4jkvq0"},
		{"xxxxx.example.", 12, "c884767386cd6fdd6f78c5e275442eb715b17795", "p227css6plntqrroonh7ah1emsar2tsl"},
	}
	for _, tc := range cases {
		hash, err := NSEC3Hash(MustParseName(tc.name), salt, tc.iterations, NSEC3SHA1)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if hexStr := toHex(hash); hexStr != tc.wantHex {
			t.Errorf("%s hex: got %s want %s", tc.name, hexStr, tc.wantHex)
		}
		if got := Base32HexEncode(hash); got != tc.wantB32 {
			t.Errorf("%s b32: got %s want %s", tc.name, got, tc.wantB32)
		}
	}
}

// TestBase32HexRFC4648 uses the standard Extended Hex Alphabet vectors of
// RFC 4648 section 7, including uppercase acceptance and round trips.
func TestBase32HexRFC4648(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xff, 0xfb}, "vvtg"},
		{[]byte{0x01}, "04"},
		{[]byte("Hello"), "91imor3f"},
		{[]byte(""), ""},
	}
	for _, c := range cases {
		if got := Base32HexEncode(c.in); got != c.want {
			t.Errorf("encode(%x)=%s want %s", c.in, got, c.want)
		}
		dec, err := Base32HexDecode(c.want)
		if err != nil {
			t.Fatalf("decode %s: %v", c.want, err)
		}
		if string(dec) != string(c.in) {
			t.Errorf("round trip %x != %x", dec, c.in)
		}
		// Uppercase encoding must decode identically.
		up, err := Base32HexDecode(upper(c.want))
		if err != nil {
			t.Fatalf("uppercase decode: %v", err)
		}
		if string(up) != string(c.in) {
			t.Errorf("uppercase round trip %x != %x", up, c.in)
		}
	}
	if _, err := Base32HexDecode("zzz"); err == nil {
		t.Fatal("invalid base32hex character accepted")
	}
}

func toHex(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexd[c>>4]
		out[i*2+1] = hexd[c&0xF]
	}
	return string(out)
}

func upper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'v' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

func TestNSEC3HashUnsupportedAlg(t *testing.T) {
	if _, err := NSEC3Hash(MustParseName("example."), nil, 0, 2); err == nil {
		t.Fatal("expected error for unsupported NSEC3 algorithm")
	}
}

func TestNSEC3CoversNormalAndWrap(t *testing.T) {
	owner := []byte{0x10}
	next := []byte{0x20}
	targets := map[string][]byte{
		"inside":   {0x15},
		"at owner": {0x10},
		"at next":  {0x20},
		"outside":  {0x30},
	}
	want := map[string]bool{"inside": true, "at owner": false, "at next": false, "outside": false}
	for label, h := range targets {
		if got := NSEC3Covers(owner, next, h); got != want[label] {
			t.Errorf("normal interval %s: got %v want %v", label, got, want[label])
		}
	}

	// Wraparound interval owner > next: covers the zero point.
	wrapOwner := []byte{0xA0}
	wrapNext := []byte{0x20}
	type coverCase struct {
		h    []byte
		want bool
	}
	wrapCases := map[string]coverCase{
		"after owner": {[]byte{0xB0}, true},
		"near zero":   {[]byte{0x01}, true},
		"before next": {[]byte{0x1F}, true},
		"in the gap":  {[]byte{0x50}, false},
		"equal owner": {[]byte{0xA0}, false},
		"equal next":  {[]byte{0x20}, false},
	}
	for label, c := range wrapCases {
		if got := NSEC3Covers(wrapOwner, wrapNext, c.h); got != c.want {
			t.Errorf("wrap interval %s: got %v want %v", label, got, c.want)
		}
	}
}

func TestNSEC3CoversRFC5155Example(t *testing.T) {
	// RFC 5155 appendix B chain fragment around 2vptu5... (wildcard hash).
	// Constructed hashes below are 20-byte values where only prefixes vary;
	// the goal is exercising ordering against realistic-length digests.
	base := make([]byte, 20)
	owner := append(append([]byte{}, base...), 0)
	owner[0] = 0x35
	next := append(append([]byte{}, base...), 0)
	next[0] = 0x39
	target := append(append([]byte{}, base...), 0)
	target[0] = 0x37
	if !NSEC3Covers(owner, next, target) {
		t.Error("expected interval coverage")
	}
	if NSEC3Covers(owner, next, owner) || NSEC3Covers(owner, next, next) {
		t.Error("endpoints must never be covered")
	}
}
