package netsec

import (
	"errors"
	"fmt"
	"testing"
)

func TestAddressLayoutAndParsing(t *testing.T) {
	address, err := NewAddress([]byte{0x01, 0x02, 0x03}, []byte{0x41, 0x04, 0x05})
	if err != nil {
		t.Fatalf("NewAddress: %v", err)
	}
	if address.Hash() != [3]byte{0x01, 0x02, 0x03} {
		t.Fatalf("hash = %x", address.Hash())
	}
	if address.PRand() != [3]byte{0x41, 0x04, 0x05} {
		t.Fatalf("prand = %x", address.PRand())
	}
	if got, want := address.String(), "010203410405"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if !address.IsResolvable() {
		t.Fatal("valid address was not resolvable")
	}

	parsed, err := ParseAddress("010203410405")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if parsed != address {
		t.Fatalf("parsed = %s, want %s", parsed, address)
	}

	decoded, err := AddressFromBytes([]byte{0x01, 0x02, 0x03, 0x41, 0x04, 0x05})
	if err != nil {
		t.Fatalf("AddressFromBytes: %v", err)
	}
	if decoded != address {
		t.Fatalf("decoded = %s, want %s", decoded, address)
	}

	if _, err := AddressFromBytes(make([]byte, 5)); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("short address error = %v", err)
	}
	if _, err := ParseAddress("zz0203410405"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("non-hex address error = %v", err)
	}
}

func TestPRandTypeBits(t *testing.T) {
	for _, bits := range []byte{0x00, 0x80, 0xc0} {
		bits := bits
		t.Run(fmt.Sprintf("%02b", bits>>6), func(t *testing.T) {
			_, err := NewAddress([]byte{1, 2, 3}, []byte{bits, 4, 5})
			if !errors.Is(err, ErrInvalidPRand) {
				t.Fatalf("top bits %02b: error = %v, want ErrInvalidPRand", bits>>6, err)
			}
		})
	}
}

func TestAESCallbackUsesIRKAndZeroPaddedPRand(t *testing.T) {
	device := newTestDevice("device-a", 1)
	prand := [3]byte{0x40, 0x11, 0x22}
	address := addressFor(t, testAES, device.irk, prand)

	var gotKey IRK
	var gotBlock [16]byte
	calls := 0
	capturingAES := func(key IRK, block [16]byte) ([16]byte, error) {
		calls++
		gotKey = key
		gotBlock = block
		return testAES(key, block)
	}

	resolver, err := NewResolver(Options{AES: capturingAES})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	resolution, err := resolver.Resolve(address)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolution.IsUnique() {
		t.Fatalf("resolution status = %s, want unique", resolution.Status)
	}
	if calls != 1 {
		t.Fatalf("AES calls = %d, want 1", calls)
	}
	if gotKey != device.irk {
		t.Fatalf("AES key = %x, want %x", gotKey, device.irk)
	}
	if gotBlock[0] != prand[0] || gotBlock[1] != prand[1] || gotBlock[2] != prand[2] {
		t.Fatalf("AES block starts with %x, want prand %x", gotBlock[:3], prand)
	}
	for index := 3; index < len(gotBlock); index++ {
		if gotBlock[index] != 0 {
			t.Fatalf("AES block byte %d = %02x, want zero padding", index, gotBlock[index])
		}
	}
}

func TestInvalidPRandIsRejectedBeforeAES(t *testing.T) {
	device := newTestDevice("device-a", 1)
	valid := addressFor(t, testAES, device.irk, [3]byte{0x40, 0x51, 0x61})

	calls := 0
	resolver, err := NewResolver(Options{
		AES: func(key IRK, block [16]byte) ([16]byte, error) {
			calls++
			return testAES(key, block)
		},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	for _, bits := range []byte{0x00, 0x80, 0xc0} {
		invalid := valid
		invalid[3] = (invalid[3] & 0x3f) | bits
		resolution, err := resolver.Resolve(invalid)
		if !errors.Is(err, ErrInvalidPRand) {
			t.Fatalf("top bits %02b: error = %v, want ErrInvalidPRand", bits>>6, err)
		}
		if !resolution.IsAnonymous() || !resolution.Ticket.IsZero() {
			t.Fatalf("top bits %02b: resolution = %+v, want anonymous without ticket", bits>>6, resolution)
		}
	}
	if calls != 0 {
		t.Fatalf("AES called %d times for invalid prand", calls)
	}
}
