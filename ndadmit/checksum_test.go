package ndadmit

import (
	"errors"
	"testing"
)

// TestChecksumVerification exercises the optional ICMPv6 checksum admission
// check, including a forged advertisement whose checksum does not match.
func TestChecksumVerification(t *testing.T) {
	rawPkt := concat(
		baseHeader(RouterAdvertisement),
		raBody(),
		llOption(OptSourceLinkLayerAddr, macA),
	)

	t.Run("valid checksum admitted", func(t *testing.T) {
		pkt := withChecksum(addrLL, addrAll, rawPkt)
		if _, err := ParseWith(Config{VerifyChecksum: true}, 255, addrLL, addrAll, pkt); err != nil {
			t.Fatalf("correctly checksummed RA rejected: %v", err)
		}
	})

	t.Run("forged checksum rejected", func(t *testing.T) {
		pkt := withChecksum(addrLL, addrAll, rawPkt)
		pkt[len(pkt)-1] ^= 0xff // tamper with the SLLA payload after summing
		_, err := ParseWith(Config{VerifyChecksum: true}, 255, addrLL, addrAll, pkt)
		if !errors.Is(err, ErrChecksum) {
			t.Fatalf("tampered packet got %v, want ErrChecksum", err)
		}
	})

	t.Run("checksum ignored by default", func(t *testing.T) {
		// Zero checksum is never a valid computed value, but the default
		// configuration leaves verification to the host's stack.
		if _, err := Parse(255, addrLL, addrAll, rawPkt); err != nil {
			t.Fatalf("default Parse must not verify checksum, got %v", err)
		}
	})
}

// TestZeroLengthNeverLoops feeds a zero-length option in every legal message
// type. The parser must return promptly instead of advancing zero bytes.
func TestZeroLengthNeverLoops(t *testing.T) {
	packets := map[int][]byte{
		RouterSolicitation:    concat(baseHeader(RouterSolicitation), rsBody(), []byte{99, 0}),
		RouterAdvertisement:   concat(baseHeader(RouterAdvertisement), raBody(), []byte{99, 0}),
		NeighborSolicitation:  concat(baseHeader(NeighborSolicitation), nsBody(addrTarget), []byte{99, 0}),
		NeighborAdvertisement: concat(baseHeader(NeighborAdvertisement), naBody(0x20, addrTarget), []byte{99, 0}),
		Redirect:              concat(baseHeader(Redirect), redirectBody(addrLL2, addrGlobal), []byte{99, 0}),
	}
	for msgType, pkt := range packets {
		_, err := Parse(255, addrLL, addrLL2, pkt)
		if !errors.Is(err, ErrZeroOption) {
			t.Fatalf("type %d: got %v, want ErrZeroOption", msgType, err)
		}
	}
}

// FuzzParse ensures no constructed byte sequence can panic or hang the
// admission parser.
func FuzzParse(f *testing.F) {
	f.Add(uint8(255), []byte{NeighborSolicitation, 0, 0, 0}, make([]byte, 16), byte(1), byte(1))
	f.Fuzz(func(t *testing.T, hop uint8, header []byte, body []byte, optType, optLen byte) {
		pkt := append(append([]byte(nil), header...), body...)
		pkt = append(pkt, optType, optLen)
		_, _ = Parse(hop, addrLL, addrAll, pkt)
	})
}
