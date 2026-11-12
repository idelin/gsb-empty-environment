package ndadmit

import (
	"errors"
	"net/netip"
	"testing"
)

// TestParseRejections drives constructed malformed packets through the
// admission choke point and asserts the exact sentinel error.
func TestParseRejections(t *testing.T) {
	validNS := concat(baseHeader(NeighborSolicitation), nsBody(addrTarget))

	truncatedOption := concat(
		baseHeader(NeighborSolicitation),
		nsBody(addrTarget),
		[]byte{OptSourceLinkLayerAddr, 2, 1, 2, 3}, // claims 16 bytes, gives 5
	)
	truncatedFixed := baseHeader(NeighborSolicitation)[:2]

	zeroLengthOption := concat(
		baseHeader(NeighborSolicitation),
		nsBody(addrTarget),
		[]byte{99, 0}, // unknown type with a zero length: classic loop trap
	)
	oneByteTrailingOption := concat(
		baseHeader(NeighborSolicitation),
		nsBody(addrTarget),
		[]byte{99}, // only the type byte, no length unit
	)

	dupSLLA := concat(
		baseHeader(NeighborSolicitation),
		nsBody(addrTarget),
		llOption(OptSourceLinkLayerAddr, macA),
		llOption(OptSourceLinkLayerAddr, macB),
	)
	dupTLLA := concat(
		baseHeader(NeighborAdvertisement),
		naBody(0x60, addrTarget), // solicited + override
		llOption(OptTargetLinkLayerAddr, macA),
		llOption(OptTargetLinkLayerAddr, macB),
	)
	dupMTU := concat(
		baseHeader(RouterAdvertisement),
		raBody(),
		mtuOption(1500),
		mtuOption(1280),
	)

	forgedRAGlobalSrc := concat(
		baseHeader(RouterAdvertisement),
		raBody(),
		llOption(OptSourceLinkLayerAddr, macA),
	)
	forgedRAUnspecSrc := concat(baseHeader(RouterAdvertisement), raBody())
	forgedRAHopLimit := concat(baseHeader(RouterAdvertisement), raBody())

	badCode := concat([]byte{NeighborSolicitation, 1, 0, 0}, nsBody(addrTarget))
	unknownType := []byte{200, 0, 0, 0, 1, 2, 3, 4}

	dadWithSLLA := concat(
		baseHeader(NeighborSolicitation),
		nsBody(addrTarget),
		llOption(OptSourceLinkLayerAddr, macA),
	)
	multicastNAWithTLLA := concat(
		baseHeader(NeighborAdvertisement),
		naBody(0x20, addrTarget), // override only
		llOption(OptTargetLinkLayerAddr, macA),
	)
	solicitedNAFromUnspec := concat(
		baseHeader(NeighborAdvertisement),
		naBody(0x40, addrTarget),
	)
	redirectGlobalSrc := concat(
		baseHeader(Redirect),
		redirectBody(addrLL2, addrGlobal),
	)

	tests := []struct {
		name    string
		hop     uint8
		src     netip.Addr
		dst     netip.Addr
		packet  []byte
		cfg     Config
		wantErr error
	}{
		{"truncated fixed header", 255, addrLL, addrAll, truncatedFixed, Config{}, ErrTruncated},
		{"truncated option body", 255, addrLL, addrAll, truncatedOption, Config{}, ErrTruncated},
		{"one byte option fragment", 255, addrLL, addrAll, oneByteTrailingOption, Config{}, ErrTruncated},
		{"zero length option loops forever", 255, addrLL, addrAll, zeroLengthOption, Config{}, ErrZeroOption},
		{"hop limit below 255", 64, addrLL, addrAll, validNS, Config{}, ErrHopLimit},
		{"nonzero icmp code", 255, addrLL, addrAll, badCode, Config{}, ErrBadCode},
		{"unknown icmp type", 255, addrLL, addrAll, unknownType, Config{}, ErrUnknownType},
		{"duplicate source link layer address", 255, addrLL, addrAll, dupSLLA, Config{}, ErrDuplicateOption},
		{"duplicate target link layer address", 255, addrLL, addrLL2, dupTLLA, Config{}, ErrDuplicateOption},
		{"duplicate mtu option", 255, addrLL, addrAll, dupMTU, Config{}, ErrDuplicateOption},
		{"forged ra from global source", 255, addrGlobal, addrAll, forgedRAGlobalSrc, Config{}, ErrBadFields},
		{"forged ra from unspecified source", 255, addrUnspec, addrAll, forgedRAUnspecSrc, Config{}, ErrBadFields},
		{"forged ra with low hop limit", 254, addrLL, addrAll, forgedRAHopLimit, Config{}, ErrHopLimit},
		{"dad solicitation must not carry slla", 255, addrUnspec, addrAll, dadWithSLLA, Config{}, ErrBadFields},
		{"multicast na must not carry tlla", 255, addrLL, addrAll, multicastNAWithTLLA, Config{}, ErrBadFields},
		{"solicited na cannot use unspecified source", 255, addrUnspec, addrAll, solicitedNAFromUnspec, Config{}, ErrBadFields},
		{"redirect must use link local source", 255, addrGlobal, addrLL2, redirectGlobalSrc, Config{}, ErrBadFields},
		{"bad ethernet slla length", 255, addrLL, addrAll,
			concat(baseHeader(NeighborSolicitation), nsBody(addrTarget),
				rawOption(OptSourceLinkLayerAddr, 2, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14})),
			Config{LinkLayerAddrLen: 6}, ErrOptionLen},
		{"mtu below ipv6 minimum", 255, addrLL, addrAll,
			concat(baseHeader(RouterAdvertisement), raBody(), mtuOption(1000)),
			Config{}, ErrBadFields},
		{"multicast source rejected", 255, addrAll, addrLL2, validNS, Config{}, ErrBadFields},
		{"multicast prefix rejected", 255, addrLL, addrAll,
			concat(baseHeader(RouterAdvertisement), raBody(),
				rawOption(OptPrefixInformation, 4,
					append([]byte{128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						netip.MustParseAddr("ff00::").AsSlice()...))),
			Config{}, ErrBadFields},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseWith(tc.cfg, tc.hop, tc.src, tc.dst, tc.packet)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}
