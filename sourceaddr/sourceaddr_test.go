package sourceaddr

import (
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"
)

func TestDerivationAndSourceAuthorization(t *testing.T) {
	zeroNKey := make([]byte, 32)
	largeNKey := make([]byte, 32)
	binary.BigEndian.PutUint64(largeNKey[24:], 0x64a1)

	for _, tc := range []struct {
		name        string
		key         []byte
		wantN       int
		wantAddress string
		wantPrefix  string
	}{
		{
			name:        "n zero",
			key:         zeroNKey,
			wantN:       0,
			wantAddress: "0200a08d5b83b750710cf65777fba186",
			wantPrefix:  "0300a08d5b83b7500000000000000000/64",
		},
		{
			name:        "large n",
			key:         largeNKey,
			wantN:       20,
			wantAddress: "0214f58de0656905f4ce5fd89af62fe7",
			wantPrefix:  "0314f58de06569050000000000000000/64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hash := sha512.Sum512(tc.key)
			n := leadingOnes(hash[:])
			if n != tc.wantN {
				t.Fatalf("leading ones = %d, want %d", n, tc.wantN)
			}
			address, err := DeriveAddress(tc.key)
			if err != nil {
				t.Fatalf("DeriveAddress() error = %v", err)
			}
			prefix, err := DerivePrefix(tc.key)
			if err != nil {
				t.Fatalf("DerivePrefix() error = %v", err)
			}
			rawAddress := address.As16()
			if rawAddress[0] != 0x02 || int(rawAddress[1]) != n {
				t.Fatalf("address header = %#x, %d; want 0x02, %d", rawAddress[0], rawAddress[1], n)
			}
			rawPrefix := prefix.Masked().Addr().As16()
			if rawPrefix[0] != 0x03 || int(rawPrefix[1]) != n {
				t.Fatalf("prefix header = %#x, %d; want 0x03, %d", rawPrefix[0], rawPrefix[1], n)
			}
			if prefix.Bits() != 64 {
				t.Fatalf("prefix bits = %d, want 64", prefix.Bits())
			}
			for _, value := range rawPrefix[8:] {
				if value != 0 {
					t.Fatalf("prefix host part = %v, want zeroes", rawPrefix[8:])
				}
			}
			wantAddress, err := parseAddr16(t, tc.wantAddress)
			if err != nil {
				t.Fatal(err)
			}
			wantPrefix, err := parsePrefix(t, tc.wantPrefix)
			if err != nil {
				t.Fatal(err)
			}
			if address != wantAddress {
				t.Fatalf("address = %x, want %x", address.As16(), wantAddress.As16())
			}
			if prefix != wantPrefix {
				t.Fatalf("prefix = %s, want %s", prefix, wantPrefix)
			}

			if err := CheckInnerSource(tc.key, address); err != nil {
				t.Fatalf("derived address rejected: %v", err)
			}

			subnetSource := prefix.Addr()
			subnetSourceBytes := subnetSource.As16()
			subnetSourceBytes[15] = 0xfe
			subnetSource = netip.AddrFrom16(subnetSourceBytes)
			if err := CheckInnerSource(tc.key, subnetSource); err != nil {
				t.Fatalf("subnet source rejected: %v", err)
			}
			if !AllowInnerSource(tc.key, subnetSource) {
				t.Fatal("AllowInnerSource() = false, want true")
			}

			outside := rawPrefix
			outside[7] ^= 0x01
			outside[15] = 0x01
			outsideAddr := netip.AddrFrom16(outside)
			if err := CheckInnerSource(tc.key, outsideAddr); !errors.Is(err, ErrUnauthorizedSrc) {
				t.Fatalf("outside source error = %v, want ErrUnauthorizedSrc", err)
			}

			if err := CheckAnnouncedAddress(tc.key, address); err != nil {
				t.Fatalf("matching announced address rejected: %v", err)
			}
			if err := CheckAnnouncedAddress(tc.key, subnetSource); !errors.Is(err, ErrInvalidAnnounce) {
				t.Fatalf("announced subnet address error = %v, want ErrInvalidAnnounce", err)
			}
		})
	}
}

func TestAddressSpoofingAcrossPeers(t *testing.T) {
	peerOne := make([]byte, 32)
	peerTwo := make([]byte, 32)
	binary.BigEndian.PutUint64(peerTwo[24:], 0x64a1)

	peerTwoAddress, err := DeriveAddress(peerTwo)
	if err != nil {
		t.Fatalf("DeriveAddress(peer two) error = %v", err)
	}

	if err := CheckInnerSource(peerOne, peerTwoAddress); !errors.Is(err, ErrUnauthorizedSrc) {
		t.Fatalf("spoofed exact address error = %v, want ErrUnauthorizedSrc", err)
	}

	peerTwoPrefix, err := DerivePrefix(peerTwo)
	if err != nil {
		t.Fatalf("DerivePrefix(peer two) error = %v", err)
	}
	spoofedSubnet := peerTwoPrefix.Addr().As16()
	spoofedSubnet[15] = 1
	if err := CheckInnerSource(peerOne, netip.AddrFrom16(spoofedSubnet)); !errors.Is(err, ErrUnauthorizedSrc) {
		t.Fatalf("spoofed subnet address error = %v, want ErrUnauthorizedSrc", err)
	}

	if err := CheckAnnouncedAddress(peerOne, peerTwoAddress); !errors.Is(err, ErrInvalidAnnounce) {
		t.Fatalf("mismatched announcement error = %v, want ErrInvalidAnnounce", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	shortKey := make([]byte, 31)
	if _, err := DeriveAddress(shortKey); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("short key address error = %v, want ErrInvalidPublicKey", err)
	}
	if _, err := DerivePrefix(shortKey); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("short key prefix error = %v, want ErrInvalidPublicKey", err)
	}
	if err := CheckInnerSource(shortKey, netip.Addr{}); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("short key source error = %v, want ErrInvalidPublicKey", err)
	}

	key := make([]byte, 32)
	address, err := DeriveAddress(key)
	if err != nil {
		t.Fatalf("DeriveAddress() error = %v", err)
	}
	ipv4, err := netip.ParseAddr("192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := netip.ParseAddr("::ffff:192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []netip.Addr{netip.Addr{}, ipv4, mapped, address.WithZone("eth0")} {
		if err := CheckInnerSource(key, source); !errors.Is(err, ErrUnauthorizedSrc) {
			t.Fatalf("source %v error = %v, want ErrUnauthorizedSrc", source, err)
		}
	}
	if err := CheckAnnouncedAddress(key, netip.Addr{}); !errors.Is(err, ErrInvalidAnnounce) {
		t.Fatalf("invalid announcement error = %v, want ErrInvalidAnnounce", err)
	}
}

func TestAllOnesHashIsRejected(t *testing.T) {
	hash := make([]byte, sha512.Size)
	for i := range hash {
		hash[i] = 0xff
	}

	_, _, err := partsFromHash(hash)
	if !errors.Is(err, ErrUnsupportedPublicKey) {
		t.Fatalf("partsFromHash() error = %v, want ErrUnsupportedPublicKey", err)
	}
}

func TestHashWithInsufficientTrailingBitsIsRejected(t *testing.T) {
	hash := make([]byte, sha512.Size)
	for i := range hash {
		hash[i] = 0xff
	}
	hash[62] = 0xfe

	_, _, err := partsFromHash(hash)
	if !errors.Is(err, ErrUnsupportedPublicKey) {
		t.Fatalf("partsFromHash() error = %v, want ErrUnsupportedPublicKey", err)
	}
}

func parseAddr16(t *testing.T, value string) (netip.Addr, error) {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		return netip.Addr{}, err
	}
	var bytes [16]byte
	copy(bytes[:], raw)
	return netip.AddrFrom16(bytes), nil
}

func parsePrefix(t *testing.T, value string) (netip.Prefix, error) {
	t.Helper()
	raw, err := hex.DecodeString(value[:len(value)-3])
	if err != nil {
		return netip.Prefix{}, err
	}
	var bytes [16]byte
	copy(bytes[:], raw)
	return netip.AddrFrom16(bytes).Prefix(64)
}
