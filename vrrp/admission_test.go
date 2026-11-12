package vrrp

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

var (
	testVRID     uint8  = 42
	testInterval uint16 = 100

	ownerAddr1 = netip.MustParseAddr("10.0.0.1")
	ownerAddr2 = netip.MustParseAddr("10.0.0.2")
	backupAddr = netip.MustParseAddr("192.0.2.1")
	otherAddr  = netip.MustParseAddr("198.51.100.9")

	testVAddrs = []netip.Addr{ownerAddr1, ownerAddr2}
)

func newTestAdmitter(t *testing.T) *Admitter {
	t.Helper()
	adm, err := NewAdmitter(Config{
		VRID:                     testVRID,
		MaxAdvertisementInterval: testInterval,
		VirtualAddresses:         testVAddrs,
	})
	if err != nil {
		t.Fatalf("NewAdmitter: %v", err)
	}
	return adm
}

// makeAdvert builds an un-checksumed VRRPv3 advertisement for the fields.
func makeAdvert(vrid, priority uint8, interval uint16, vaddrs []netip.Addr) []byte {
	b := make([]byte, headerLen+4*len(vaddrs))
	b[0] = Version3<<4 | typeAdvertise
	b[offsetVRID] = vrid
	b[offsetPriority] = priority
	b[offsetAddrCount] = byte(len(vaddrs))
	binary.BigEndian.PutUint16(b[offsetMaxAdver:offsetMaxAdver+2], interval)
	for i, addr := range vaddrs {
		copy(b[headerLen+4*i:headerLen+4*(i+1)], addr.AsSlice())
	}
	return b
}

// stampChecksum fills the VRRP checksum using the given source/destination.
func stampChecksum(t *testing.T, b []byte, src, dst netip.Addr) []byte {
	t.Helper()
	binary.BigEndian.PutUint16(b[offsetChecksum:offsetChecksum+2], checksumV4(b, src, dst))
	return b
}

func baseInbound(src netip.Addr, pkt []byte) Inbound {
	return Inbound{
		TTL:         MulticastTTL,
		Source:      src,
		Destination: MulticastAddrV4,
		VRRPPacket:  pkt,
	}
}

func validPkt(t *testing.T, src netip.Addr, priority uint8) []byte {
	t.Helper()
	pkt := makeAdvert(testVRID, priority, testInterval, testVAddrs)
	return stampChecksum(t, pkt, src, MulticastAddrV4)
}

// refChecksum is an independent RFC 1071 reference: build the full pseudo
// header plus VRRP packet (checksum zeroed) and sum all 16-bit words.
func refChecksum(pkt []byte, src, dst netip.Addr) uint16 {
	s4 := src.As4()
	d4 := dst.As4()
	buf := make([]byte, 12+len(pkt))
	copy(buf[0:4], s4[:])
	copy(buf[4:8], d4[:])
	buf[8] = 0
	buf[9] = ProtocolNumber
	binary.BigEndian.PutUint16(buf[10:12], uint16(len(pkt)))
	copy(buf[12:], pkt)
	buf[12+offsetChecksum] = 0
	buf[12+offsetChecksum+1] = 0

	return sumFold(buf)
}

// sumFold returns the one's complement of the 16-bit one's complement sum.
func sumFold(buf []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(buf); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(buf[i : i+2]))
	}
	if len(buf)%2 == 1 {
		sum += uint32(buf[len(buf)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// verifyChecksum folds the full pseudo header plus VRRP packet, including the
// embedded checksum field; a correct checksum folds to zero.
func verifyChecksum(pkt []byte, src, dst netip.Addr) bool {
	s4 := src.As4()
	d4 := dst.As4()
	buf := make([]byte, 12+len(pkt))
	copy(buf[0:4], s4[:])
	copy(buf[4:8], d4[:])
	buf[8] = 0
	buf[9] = ProtocolNumber
	binary.BigEndian.PutUint16(buf[10:12], uint16(len(pkt)))
	copy(buf[12:], pkt)
	return sumFold(buf) == 0
}

func sumDrops(adm *Admitter) uint64 {
	var total uint64
	for r := Reason(1); r < numReasons; r++ {
		total += adm.DropCount(r)
	}
	return total
}

func TestChecksumMatchesReferenceImplementation(t *testing.T) {
	src := backupAddr
	pkt := makeAdvert(testVRID, 100, testInterval, testVAddrs)
	got := checksumV4(pkt, src, MulticastAddrV4)
	want := refChecksum(pkt, src, MulticastAddrV4)
	if got != want {
		t.Fatalf("checksum = %#04x, reference = %#04x", got, want)
	}
	// The stored checksum must validate (folded sum becomes zero) when present.
	binary.BigEndian.PutUint16(pkt[offsetChecksum:offsetChecksum+2], got)
	if !verifyChecksum(pkt, src, MulticastAddrV4) {
		t.Fatal("checksum with embedded field did not validate")
	}
}

func TestAdmitAcceptsValidBackupAdvertisement(t *testing.T) {
	adm := newTestAdmitter(t)
	adv, reason := adm.Admit(baseInbound(backupAddr, validPkt(t, backupAddr, 100)))
	if reason != ReasonNone || adv == nil {
		t.Fatalf("expected acceptance, got %v", reason)
	}
	if adv.VRID != testVRID || adv.Priority != 100 || adv.MaxAdvertisementInterval != testInterval {
		t.Fatalf("parsed advertisement mismatch: %+v", adv)
	}
	if len(adv.VirtualAddresses) != len(testVAddrs) {
		t.Fatalf("virtual addresses = %v", adv.VirtualAddresses)
	}
}

func TestAdmitAcceptsOwnerFromVirtualAddress(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, src := range testVAddrs {
		adv, reason := adm.Admit(baseInbound(src, validPkt(t, src, PriorityOwner)))
		if reason != ReasonNone || adv == nil || adv.Priority != PriorityOwner {
			t.Fatalf("owner packet from %s rejected: %v", src, reason)
		}
	}
	if total := sumDrops(adm); total != 0 {
		t.Fatalf("unexpected drops: %d", total)
	}
}

func TestTTLBelow255IsDroppedAndCounted(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, ttl := range []uint8{254, 1, 0} {
		in := baseInbound(backupAddr, validPkt(t, backupAddr, 100))
		in.TTL = ttl
		adv, reason := adm.Admit(in)
		if reason != ReasonTTL || adv != nil {
			t.Fatalf("TTL %d: expected ReasonTTL, got %v", ttl, reason)
		}
	}
	if got := adm.DropCount(ReasonTTL); got != 3 {
		t.Fatalf("TTL drop count = %d, want 3", got)
	}
}

func TestPriority255FromNonOwnerIsSpoof(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, src := range []netip.Addr{backupAddr, otherAddr} {
		adv, reason := adm.Admit(baseInbound(src, validPkt(t, src, PriorityOwner)))
		if reason != ReasonOwnerSpoof || adv != nil {
			t.Fatalf("spoofed owner from %s: expected ReasonOwnerSpoof, got %v", src, reason)
		}
	}
	if got := adm.DropCount(ReasonOwnerSpoof); got != 2 {
		t.Fatalf("owner spoof count = %d, want 2", got)
	}
}

func TestIntervalMismatchIsDroppedAndCounted(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, interval := range []uint16{99, 200, 4095} {
		pkt := makeAdvert(testVRID, 100, interval, testVAddrs)
		stampChecksum(t, pkt, backupAddr, MulticastAddrV4)
		adv, reason := adm.Admit(baseInbound(backupAddr, pkt))
		if reason != ReasonMaxAdvertInterval || adv != nil {
			t.Fatalf("interval %d: expected ReasonMaxAdvertInterval, got %v", interval, reason)
		}
	}
	if got := adm.DropCount(ReasonMaxAdvertInterval); got != 3 {
		t.Fatalf("interval drop count = %d, want 3", got)
	}
}

func TestDestinationMustBeVRRPMulticast(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, dst := range []netip.Addr{
		netip.MustParseAddr("224.0.0.19"),
		netip.MustParseAddr("10.0.0.254"),
	} {
		pkt := makeAdvert(testVRID, 100, testInterval, testVAddrs)
		stampChecksum(t, pkt, backupAddr, dst)
		in := baseInbound(backupAddr, pkt)
		in.Destination = dst
		adv, reason := adm.Admit(in)
		if reason != ReasonDestination || adv != nil {
			t.Fatalf("dst %s: expected ReasonDestination, got %v", dst, reason)
		}
	}
}

func TestVRIDMismatchDropped(t *testing.T) {
	adm := newTestAdmitter(t)
	pkt := makeAdvert(testVRID+1, 100, testInterval, testVAddrs)
	stampChecksum(t, pkt, backupAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, pkt)); reason != ReasonVRID {
		t.Fatalf("got %v, want ReasonVRID", reason)
	}
}

func TestChecksumFailuresDropped(t *testing.T) {
	adm := newTestAdmitter(t)

	// Corrupt one payload byte after checksumming.
	pkt := validPkt(t, backupAddr, 100)
	pkt[offsetPriority] = 99 // priority byte
	if _, reason := adm.Admit(baseInbound(backupAddr, pkt)); reason != ReasonChecksum {
		t.Fatalf("corrupt packet: got %v, want ReasonChecksum", reason)
	}

	// A checksum computed against another pseudo-header source must fail when
	// the packet actually arrives from the real source: this proves the pseudo
	// header is included in checksum coverage.
	spoofed := makeAdvert(testVRID, 100, testInterval, testVAddrs)
	stampChecksum(t, spoofed, otherAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, spoofed)); reason != ReasonChecksum {
		t.Fatalf("pseudo-header spoof: got %v, want ReasonChecksum", reason)
	}

	// Re-stamp for the real source and ensure it now passes.
	stampChecksum(t, spoofed, backupAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, spoofed)); reason != ReasonNone {
		t.Fatalf("re-stamped packet: got %v, want acceptance", reason)
	}

	if got := adm.DropCount(ReasonChecksum); got != 2 {
		t.Fatalf("checksum drop count = %d, want 2", got)
	}
}

func withFirstByte(t *testing.T, b0 byte) []byte {
	t.Helper()
	pkt := validPkt(t, backupAddr, 100)
	pkt[0] = b0
	return pkt
}

func withType(t *testing.T, ptype uint8) []byte {
	t.Helper()
	pkt := validPkt(t, backupAddr, 100)
	pkt[0] = Version3<<4 | ptype
	return pkt
}

func TestMalformedPacketsDropped(t *testing.T) {
	adm := newTestAdmitter(t)

	cases := []struct {
		name   string
		pkt    []byte
		reason Reason
	}{
		{"empty", nil, ReasonShortPacket},
		{"seven bytes", make([]byte, headerLen-1), ReasonShortPacket},
		{"wrong version", withFirstByte(t, (Version3-1)<<4), ReasonVersion},
		{"wrong type", withType(t, 2), ReasonPacketType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, reason := adm.Admit(baseInbound(backupAddr, tc.pkt)); reason != tc.reason {
				t.Fatalf("got %v, want %v", reason, tc.reason)
			}
		})
	}
}

func TestAddressListValidation(t *testing.T) {
	adm := newTestAdmitter(t)

	// Address count lying about the actual packet length.
	pkt := makeAdvert(testVRID, 100, testInterval, testVAddrs)
	pkt = append(pkt, 0, 0, 0, 0) // four trailing bytes, count stays 2
	stampChecksum(t, pkt, backupAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, pkt)); reason != ReasonAddressCount {
		t.Fatalf("length/count mismatch: got %v, want ReasonAddressCount", reason)
	}

	// Different advertised address.
	foreign := []netip.Addr{ownerAddr1, netip.MustParseAddr("10.0.0.3")}
	pkt = makeAdvert(testVRID, 100, testInterval, foreign)
	stampChecksum(t, pkt, backupAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, pkt)); reason != ReasonVirtualAddress {
		t.Fatalf("foreign address: got %v, want ReasonVirtualAddress", reason)
	}

	// Reordered but identical set must be accepted.
	reordered := []netip.Addr{ownerAddr2, ownerAddr1}
	pkt = makeAdvert(testVRID, 100, testInterval, reordered)
	stampChecksum(t, pkt, backupAddr, MulticastAddrV4)
	if _, reason := adm.Admit(baseInbound(backupAddr, pkt)); reason != ReasonNone {
		t.Fatalf("reordered address set rejected: %v", reason)
	}
}

func TestInvalidSourceAddressDropped(t *testing.T) {
	adm := newTestAdmitter(t)
	for _, src := range []netip.Addr{
		netip.MustParseAddr("0.0.0.0"),
		netip.MustParseAddr("224.0.0.18"),
		netip.MustParseAddr("255.255.255.255"),
	} {
		pkt := makeAdvert(testVRID, 100, testInterval, testVAddrs)
		stampChecksum(t, pkt, src, MulticastAddrV4)
		if _, reason := adm.Admit(baseInbound(src, pkt)); reason != ReasonSourceAddress {
			t.Fatalf("source %s: got %v, want ReasonSourceAddress", src, reason)
		}
	}
}

func TestDropCountersAccumulateAndSnapshot(t *testing.T) {
	adm := newTestAdmitter(t)

	in := baseInbound(backupAddr, validPkt(t, backupAddr, 100))
	in.TTL = 1
	if _, reason := adm.Admit(in); reason != ReasonTTL {
		t.Fatalf("got %v, want ReasonTTL", reason)
	}
	if _, reason := adm.Admit(in); reason != ReasonTTL {
		t.Fatalf("got %v, want ReasonTTL", reason)
	}

	// Accepted packets never appear in the snapshot.
	if _, reason := adm.Admit(baseInbound(backupAddr, validPkt(t, backupAddr, 100))); reason != ReasonNone {
		t.Fatalf("valid packet rejected: %v", reason)
	}

	snapshot := adm.DropCounts()
	if len(snapshot) != 1 || snapshot[ReasonTTL] != 2 {
		t.Fatalf("snapshot = %v, want only ReasonTTL:2", snapshot)
	}
	if adm.DropCount(ReasonOwnerSpoof) != 0 || adm.DropCount(ReasonNone) != 0 {
		t.Fatal("untouched counters must read zero")
	}
}

func TestNewAdmitterRejectsBadConfig(t *testing.T) {
	v4 := netip.MustParseAddr("10.0.0.1")
	v6 := netip.MustParseAddr("2001:db8::1")

	cases := []struct {
		name    string
		cfg     Config
		wantMsg string
	}{
		{"zero VRID", Config{VRID: 0, MaxAdvertisementInterval: 100, VirtualAddresses: []netip.Addr{v4}}, "VRID must be in 1..255"},
		{"zero interval", Config{VRID: 1, MaxAdvertisementInterval: 0, VirtualAddresses: []netip.Addr{v4}}, "MaxAdvertisementInterval must be in 1..4095 centiseconds"},
		{"interval too large", Config{VRID: 1, MaxAdvertisementInterval: 4096, VirtualAddresses: []netip.Addr{v4}}, "MaxAdvertisementInterval must be in 1..4095 centiseconds"},
		{"no addresses", Config{VRID: 1, MaxAdvertisementInterval: 100}, "at least one virtual address is required"},
		{"ipv6 address", Config{VRID: 1, MaxAdvertisementInterval: 100, VirtualAddresses: []netip.Addr{v6}}, "virtual addresses must be IPv4"},
		{"duplicate address", Config{VRID: 1, MaxAdvertisementInterval: 100, VirtualAddresses: []netip.Addr{v4, v4}}, "duplicate virtual address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAdmitter(tc.cfg)
			var ce configError
			if err == nil {
				t.Fatal("expected configuration error, got nil")
			}
			if !errors.As(err, &ce) {
				t.Fatalf("expected configError, got %T", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestNewAdmitterCopiesAddresses(t *testing.T) {
	vaddrs := []netip.Addr{ownerAddr1}
	adm, err := NewAdmitter(Config{VRID: 1, MaxAdvertisementInterval: 100, VirtualAddresses: vaddrs})
	if err != nil {
		t.Fatalf("NewAdmitter: %v", err)
	}
	vaddrs[0] = otherAddr // mutate caller slice after construction
	if _, ok := adm.vaddrSet[otherAddr]; ok {
		t.Fatal("admitter did not copy the configured addresses")
	}
}

func TestReasonStringAndError(t *testing.T) {
	if ReasonOwnerSpoof.String() != "owner_spoof" {
		t.Fatalf("unexpected reason name %q", ReasonOwnerSpoof.String())
	}
	if ReasonOwnerSpoof.Error() != "vrrp admission: owner_spoof" {
		t.Fatalf("unexpected error text %q", ReasonOwnerSpoof.Error())
	}

	// Exercise the defensive default branch of the String switch.
	if Reason(255).String() != "unknown" {
		t.Fatalf("unknown reason rendered as %q", Reason(255).String())
	}
	// Out-of-range reason arguments read zero rather than panicking.
	adm := newTestAdmitter(t)
	if adm.DropCount(ReasonNone) != 0 || adm.DropCount(numReasons) != 0 {
		t.Fatal("out-of-range DropCount must return zero")
	}
}

func TestSameAddressSetRejectsDuplicates(t *testing.T) {
	duplicates := []netip.Addr{ownerAddr1, ownerAddr1}
	if sameAddressSet(duplicates, testVAddrs) {
		t.Fatal("a duplicated address list must not match the configured set")
	}
	if sameAddressSet(testVAddrs, append([]netip.Addr(nil), testVAddrs...)) != true {
		t.Fatal("identical sets must match")
	}
}

func TestChecksumHandlesOddLengthPacket(t *testing.T) {
	// VRRP packets are even-sized on the wire; the odd branch is defensive.
	odd := make([]byte, headerLen+1)
	odd[0] = Version3<<4 | typeAdvertise
	got := checksumV4(odd, backupAddr, MulticastAddrV4)
	want := refChecksum(odd, backupAddr, MulticastAddrV4)
	if got != want {
		t.Fatalf("odd-length checksum = %#04x, want %#04x", got, want)
	}
}
