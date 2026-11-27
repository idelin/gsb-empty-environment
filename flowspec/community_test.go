package flowspec

import (
	"errors"
	"math"
	"net"
	"testing"
)

func TestTrafficRateCommunity(t *testing.T) {
	drop := TrafficRateCommunity(65000, 0)
	if drop.ID() != 0x8006 {
		t.Fatalf("traffic-rate id = %04x", drop.ID())
	}
	wantZero := []byte{0x80, 0x06, 0xFD, 0xE8, 0, 0, 0, 0}
	drop = TrafficRateCommunity(65000, 0)
	if string(drop.Raw[:]) != string(wantZero) {
		t.Fatalf("drop community = % X", drop.Raw)
	}
	tr := drop.AsTrafficRate()
	if tr == nil || !tr.IsDrop() || tr.AS != 65000 {
		t.Fatalf("bad typed rate: %+v", tr)
	}

	rate := TrafficRateCommunity(0, 1000.5)
	got := rate.AsTrafficRate()
	if got.IsDrop() || math.Abs(float64(got.Rate-1000.5)) > 0.01 {
		t.Fatalf("rate decode = %+v", got)
	}
	// Float bits must round-trip exactly.
	if got.RateBits != math.Float32bits(1000.5) {
		t.Fatal("float bits mismatch")
	}
}

func TestTrafficActionCommunity(t *testing.T) {
	ec := TrafficActionCommunity(true, true)
	if ec.ID() != 0x8007 || ec.Raw[7] != 0x03 {
		t.Fatalf("action bytes = % X", ec.Raw)
	}
	ta := ec.AsTrafficAction()
	if !ta.Terminal || !ta.Sample {
		t.Fatalf("flags = %+v", ta)
	}
}

func TestRedirectCommunity(t *testing.T) {
	var target [6]byte
	copy(target[:], []byte{0, 10, 0, 0, 0, 7})
	ec := RedirectCommunity(target)
	if ec.ID() != 0x8008 {
		t.Fatalf("redirect id = %04x", ec.ID())
	}
	r := ec.AsRedirect()
	if r == nil || r.Target != target {
		t.Fatalf("redirect target = %+v", r)
	}
}

func TestRedirectToIPCommunity(t *testing.T) {
	ip := net.ParseIP("198.51.100.7")
	ec := RedirectToIPCommunity(ip, true)
	if ec.ID() != 0x010b {
		t.Fatalf("redirect-to-ip id = %04x", ec.ID())
	}
	want := "01 0B C6 33 64 07 00 01"
	if got := ec.Raw[:]; bytesToHex(got) != want {
		t.Fatalf("redirect-to-ip bytes = % X, want %s", got, want)
	}
	r := ec.AsRedirectToIP()
	if r == nil || r.IP.String() != "198.51.100.7" || !r.Copy {
		t.Fatalf("redirect-to-ip = %+v", r)
	}
}

func bytesToHex(b []byte) string {
	const h = "0123456789ABCDEF"
	out := make([]byte, 0, len(b)*3)
	for i, x := range b {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, h[x>>4], h[x&0xf])
	}
	return string(out)
}

func TestDecodeECListAndActions(t *testing.T) {
	ecs := []ExtendedCommunity{
		TrafficRateCommunity(0, 0),
		RedirectToIPCommunity(net.ParseIP("198.18.0.9"), false),
	}
	payload := EncodeECList(ecs)
	decoded, err := DecodeECList(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("got %d communities", len(decoded))
	}
	actions := ActionsFromCommunities(decoded)
	if !actions.TrafficRate.IsDrop() || actions.RedirectToIP == nil {
		t.Fatalf("actions = %+v", actions)
	}
	if actions.Verdict() != VerdictDrop {
		t.Fatalf("verdict = %s, want DROP (rate0 wins over redirect)", actions.Verdict())
	}

	if _, err := DecodeECList([]byte{1, 2, 3}); !errors.Is(err, ErrBadCommunityLength) {
		t.Fatalf("want length error, got %v", err)
	}
	if _, err := DecodeEC(nil); !errors.Is(err, ErrBadCommunityLength) {
		t.Fatal("DecodeEC(nil) must error")
	}
}

func TestExtCommunityPathAttribute(t *testing.T) {
	ec := TrafficRateCommunity(0, 0).Raw
	// Normal one-octet length, flags optional+transitive (0xC0), type 16.
	attr := []byte{0xC0, AttrExtCommunities, 8}
	attr = append(attr, ec[:]...)
	ecs, n, err := DecodeExtCommunityAttribute(attr)
	if err != nil || n != len(attr) || len(ecs) != 1 {
		t.Fatalf("normal attr: n=%d len=%d ecs=%d err=%v", n, len(attr), len(ecs), err)
	}

	// Extended length form: 0xD0 sets the extended-length bit.
	long := []byte{0xD0, AttrExtCommunities, 0x00, 16}
	two := EncodeECList([]ExtendedCommunity{TrafficRateCommunity(0, 0), TrafficActionCommunity(true, false)})
	long = append(long, two...)
	ecs2, n2, err := DecodeExtCommunityAttribute(long)
	if err != nil || n2 != len(long) || len(ecs2) != 2 {
		t.Fatalf("extended attr: n=%d len=%d ecs=%d err=%v", n2, len(long), len(ecs2), err)
	}

	bad := []byte{0xC0, 99, 0}
	if _, _, err := DecodeExtCommunityAttribute(bad); !errors.Is(err, ErrNotExtCommunity) {
		t.Fatalf("want wrong-type error, got %v", err)
	}
}
