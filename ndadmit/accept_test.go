package ndadmit

import (
	"bytes"
	"errors"
	"testing"
)

// TestParseAccept checks that well-formed messages are admitted and that the
// identity-bearing fields are decoded to the single, unambiguous value.
func TestParseAccept(t *testing.T) {
	t.Run("neighbor solicitation with slla", func(t *testing.T) {
		pkt := concat(
			baseHeader(NeighborSolicitation),
			nsBody(addrTarget),
			llOption(OptSourceLinkLayerAddr, macA),
		)
		msg, err := ParseWith(Config{LinkLayerAddrLen: 6}, 255, addrLL, addrAll, pkt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.Target.Compare(addrTarget) != 0 {
			t.Fatalf("target = %v, want %v", msg.Target, addrTarget)
		}
		if !bytes.Equal(msg.SLLA, macA) {
			t.Fatalf("slla = %x, want %x", msg.SLLA, macA)
		}
		if msg.OptionCount != 1 {
			t.Fatalf("option count = %d, want 1", msg.OptionCount)
		}
	})

	t.Run("dad solicitation without slla", func(t *testing.T) {
		pkt := concat(baseHeader(NeighborSolicitation), nsBody(addrTarget))
		if _, err := Parse(255, addrUnspec, addrAll, pkt); err != nil {
			t.Fatalf("DAD probe should be admitted, got %v", err)
		}
	})

	t.Run("router advertisement fields", func(t *testing.T) {
		pkt := concat(
			baseHeader(RouterAdvertisement),
			raBody(),
			llOption(OptSourceLinkLayerAddr, macA),
			prefixOption(),
			mtuOption(1500),
		)
		msg, err := Parse(255, addrLL, addrAll, pkt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !msg.Managed || msg.CurHopLimit != 64 || msg.RouterLifetime != 1800 {
			t.Fatalf("RA fixed fields decoded wrong: %+v", msg)
		}
		if !msg.HasMTU || msg.MTU != 1500 {
			t.Fatalf("MTU = %d (present %v), want 1500", msg.MTU, msg.HasMTU)
		}
		if len(msg.Prefixes) != 1 || msg.Prefixes[0].Prefix.Compare(addrPrefix) != 0 {
			t.Fatalf("prefixes = %+v", msg.Prefixes)
		}
		if !msg.Prefixes[0].OnLink || !msg.Prefixes[0].Autonomous {
			t.Fatalf("prefix flags not decoded: %+v", msg.Prefixes[0])
		}
		if msg.OptionCount != 3 {
			t.Fatalf("option count = %d, want 3", msg.OptionCount)
		}
	})

	t.Run("repeated prefixes stay legal", func(t *testing.T) {
		pkt := concat(
			baseHeader(RouterAdvertisement),
			raBody(),
			prefixOption(),
			prefixOption(),
		)
		msg, err := Parse(255, addrLL, addrAll, pkt)
		if err != nil {
			t.Fatalf("duplicate prefix option must be legal, got %v", err)
		}
		if len(msg.Prefixes) != 2 {
			t.Fatalf("got %d prefixes, want 2", len(msg.Prefixes))
		}
	})

	t.Run("neighbor advertisement tlla to unicast", func(t *testing.T) {
		pkt := concat(
			baseHeader(NeighborAdvertisement),
			naBody(0x60, addrTarget),
			llOption(OptTargetLinkLayerAddr, macB),
		)
		msg, err := Parse(255, addrLL2, addrLL, pkt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !msg.Solicited || !msg.Override {
			t.Fatalf("NA flags decoded wrong: %+v", msg)
		}
		if !bytes.Equal(msg.TLLA, macB) {
			t.Fatalf("tlla = %x, want %x", msg.TLLA, macB)
		}
	})

	t.Run("router solicitation", func(t *testing.T) {
		pkt := concat(
			baseHeader(RouterSolicitation),
			rsBody(),
			llOption(OptSourceLinkLayerAddr, macA),
		)
		if _, err := Parse(255, addrLL, addrAll, pkt); err != nil {
			t.Fatalf("RS should be admitted, got %v", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		pkt := concat(baseHeader(Redirect), redirectBody(addrLL2, addrGlobal))
		msg, err := Parse(255, addrLL, addrLL2, pkt)
		if err != nil {
			t.Fatalf("redirect should be admitted, got %v", err)
		}
		if msg.Target.Compare(addrLL2) != 0 || msg.Destination.Compare(addrGlobal) != 0 {
			t.Fatalf("redirect addresses decoded wrong: %+v", msg)
		}
	})
}

// TestUnknownOptionsShareBudget verifies unknown options are skipped by length
// while still consuming the packet's option budget and framing guarantees.
func TestUnknownOptionsShareBudget(t *testing.T) {
	pkt := concat(
		baseHeader(RouterAdvertisement),
		raBody(),
		rawOption(100, 1, []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}), // unknown
		rawOption(200, 2, make([]byte, 14)),                           // unknown, 16 bytes
		mtuOption(1500),                                               // known, still parsed
	)
	msg, err := Parse(255, addrLL, addrAll, pkt)
	if err != nil {
		t.Fatalf("unknown options must be skipped, got %v", err)
	}
	if msg.Unknown != 2 {
		t.Fatalf("unknown count = %d, want 2", msg.Unknown)
	}
	if msg.OptionCount != 3 {
		t.Fatalf("total option count = %d, want 3", msg.OptionCount)
	}
	if !msg.HasMTU || msg.MTU != 1500 {
		t.Fatalf("known MTU option after unknowns was lost: %+v", msg)
	}

	// An unknown option whose claimed length overruns the packet must still
	// be caught: skipping is length-driven, not blind.
	overrun := concat(
		baseHeader(RouterAdvertisement),
		raBody(),
		[]byte{100, 3, 1, 2},
	)
	if _, err := Parse(255, addrLL, addrAll, overrun); !errors.Is(err, ErrTruncated) {
		t.Fatalf("overrunning unknown option got %v, want ErrTruncated", err)
	}
}
