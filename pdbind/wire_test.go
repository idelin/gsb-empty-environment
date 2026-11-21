package pdbind

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func option(code uint16, body []byte) []byte {
	buf := make([]byte, 4+len(body))
	binary.BigEndian.PutUint16(buf[0:2], code)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(body)))
	copy(buf[4:], body)
	return buf
}

func iaPrefixOption(prefix netip.Prefix, preferred, valid uint32) []byte {
	body := make([]byte, 25)
	binary.BigEndian.PutUint32(body[0:4], preferred)
	binary.BigEndian.PutUint32(body[4:8], valid)
	body[8] = byte(prefix.Bits())
	raw := prefix.Masked().Addr().As16()
	copy(body[9:25], raw[:])
	return option(OptIAPrefix, body)
}

func iaPDOption(iaid uint32, nested ...[]byte) []byte {
	body := make([]byte, 12)
	binary.BigEndian.PutUint32(body[0:4], iaid)
	for _, opt := range nested {
		body = append(body, opt...)
	}
	return option(OptIAPD, body)
}

func relayForw(link, peer netip.Addr, inner []byte) []byte {
	packet := make([]byte, 34)
	packet[0] = byte(MsgRelayForw)
	linkRaw := link.As16()
	peerRaw := peer.As16()
	copy(packet[2:18], linkRaw[:])
	copy(packet[18:34], peerRaw[:])
	packet = append(packet, option(OptRelayMsg, inner)...)
	return packet
}

func clientPacket(msgType MsgType, duid []byte, iapds ...[]byte) []byte {
	packet := []byte{byte(msgType), 0, 0, 0}
	packet = append(packet, option(OptClientID, duid)...)
	for _, ia := range iapds {
		packet = append(packet, ia...)
	}
	return packet
}

func TestParseClientMessage(t *testing.T) {
	packet := clientPacket(MsgRequest, duidA,
		iaPDOption(7, iaPrefixOption(pd0, 100, 200)))

	msg, err := ParseClientMessage(packet)
	if err != nil {
		t.Fatalf("ParseClientMessage: %v", err)
	}
	if msg.Type != MsgRequest || string(msg.DUID) != string(duidA) {
		t.Fatalf("unexpected header: %+v", msg)
	}
	if len(msg.IAPDs) != 1 || msg.IAPDs[0].IAID != 7 {
		t.Fatalf("unexpected IA_PD: %+v", msg.IAPDs)
	}
	lease := msg.IAPDs[0].Hint
	if lease == nil || lease.Prefix != pd0 || lease.PreferredLft != 100 || lease.ValidLft != 200 {
		t.Fatalf("unexpected IAPREFIX: %+v", lease)
	}
}

func TestParseRelayChain(t *testing.T) {
	inner := clientPacket(MsgSolicit, duidA,
		iaPDOption(9, iaPrefixOption(pd1, 100, 200)))

	link1 := netip.MustParseAddr("2001:db8:ffff::1")
	link2 := netip.MustParseAddr("2001:db8:ffff::2")
	peer1 := netip.MustParseAddr("fe80::a")
	peer2 := netip.MustParseAddr("fe80::b")

	packet := relayForw(link2, peer2, relayForw(link1, peer1, inner))
	hops, clientBytes, err := ParseRelayChain(packet, 32)
	if err != nil {
		t.Fatalf("ParseRelayChain: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("expected 2 hops, got %d", len(hops))
	}
	if hops[0].LinkAddr != link1 || hops[0].PeerAddr != peer1 {
		t.Fatalf("innermost hop wrong: %+v", hops[0])
	}
	if hops[1].LinkAddr != link2 || hops[1].PeerAddr != peer2 {
		t.Fatalf("outer hop wrong: %+v", hops[1])
	}

	msg, err := ParseClientMessage(clientBytes)
	if err != nil {
		t.Fatalf("ParseClientMessage inner: %v", err)
	}
	if msg.IAPDs[0].IAID != 9 || msg.IAPDs[0].Hint.Prefix != pd1 {
		t.Fatalf("inner message parse wrong: %+v", msg.IAPDs)
	}

	// Depth cap is enforced while unwrapping.
	deep := inner
	for i := 0; i < 33; i++ {
		deep = relayForw(link1, peer1, deep)
	}
	if _, _, err := ParseRelayChain(deep, 32); err != ErrRelayDepth {
		t.Fatalf("expected ErrRelayDepth, got %v", err)
	}

	// A direct client message is not a relay chain.
	if _, _, err := ParseRelayChain(inner, 32); err != ErrRelayMessage {
		t.Fatalf("expected ErrRelayMessage, got %v", err)
	}
}

func TestParseMessageEndToEnd(t *testing.T) {
	inner := clientPacket(MsgRenew, duidA,
		iaPDOption(7, iaPrefixOption(pd0, 100, 200)))
	packet := relayForw(linkA, peerRelay, inner)

	msg, hops, err := ParseMessage(packet, netip.MustParseAddr("fe80::ffff"), 32)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if len(hops) != 1 || msg.Type != MsgRenew || msg.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("unexpected parse result: %+v hops=%d", msg, len(hops))
	}

	direct := clientPacket(MsgRequest, duidB, iaPDOption(1))
	msg2, hops2, err := ParseMessage(direct, peerCli, 32)
	if err != nil || len(hops2) != 0 || msg2.PeerAddr != peerCli || msg2.IAPDs[0].Hint != nil {
		t.Fatalf("direct parse wrong: %+v hops=%v err=%v", msg2, hops2, err)
	}
}

func TestMalformedPackets(t *testing.T) {
	cases := [][]byte{
		nil,
		{0x03, 0x00},
		option(OptIAPD, []byte{0x00, 0x01}),
		clientPacket(MsgRequest, duidA, option(OptIAPD, []byte{0, 1, 2, 3})),
	}
	for i, packet := range cases {
		if _, err := ParseClientMessage(packet); err != ErrMalformedMessage {
			t.Fatalf("case %d: expected ErrMalformedMessage, got %v", i, err)
		}
	}

	badPrefix := make([]byte, 24)
	badPrefix[8] = 129
	badOpt := option(OptIAPrefix, badPrefix)
	if _, err := parseIAPrefix(badOpt[4:]); err != ErrMalformedMessage {
		t.Fatalf("expected malformed IAPREFIX, got %v", err)
	}
}

func TestPrefixPoolMath(t *testing.T) {
	pool := PrefixPool{
		CIDR:         netip.MustParsePrefix("2001:db8::/48"),
		DelegatedLen: 56,
	}
	if pool.Size() != 256 {
		t.Fatalf("expected 256 /56 prefixes, got %d", pool.Size())
	}
	first, ok := pool.nthPrefix(0)
	if !ok || first.String() != "2001:db8::/56" {
		t.Fatalf("nth(0) wrong: %s ok=%v", first, ok)
	}
	last, ok := pool.nthPrefix(255)
	if !ok || last.String() != "2001:db8:0:ff00::/56" {
		t.Fatalf("nth(255) wrong: %s ok=%v", last, ok)
	}
	if _, ok := pool.nthPrefix(256); ok {
		t.Fatal("nth(256) must be out of range")
	}
	for i := uint64(0); i < 256; i++ {
		prefix, _ := pool.nthPrefix(i)
		if pool.indexOf(prefix) != i {
			t.Fatalf("round-trip index failed at %d: %s -> %d", i, prefix, pool.indexOf(prefix))
		}
	}

	// Large pool where slot indices exceed the low 32 bits of the address.
	big := PrefixPool{
		CIDR:         netip.MustParsePrefix("2001:db8::/32"),
		DelegatedLen: 48,
	}
	prefix, _ := big.nthPrefix(1 << 12)
	if prefix.String() != "2001:db8:1000::/48" || big.indexOf(prefix) != 1<<12 {
		t.Fatalf("large pool math wrong: %s idx=%d", prefix, big.indexOf(prefix))
	}

	unaligned := netip.MustParsePrefix("2001:db8::1/56")
	if pool.contains(unaligned) {
		t.Fatal("unaligned prefix must not count as contained")
	}
	if pool.contains(netip.MustParsePrefix("2001:db9::/56")) {
		t.Fatal("outside prefix must not count as contained")
	}
}
