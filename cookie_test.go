package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"
)

var (
	testClient = netip.MustParseAddrPort("192.0.2.10:40000")
	testServer = netip.MustParseAddrPort("198.51.100.20:443")
)

func testKey(seed byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func testHost(t *testing.T, current, previous [32]byte, table []uint16) *Host {
	t.Helper()
	host, err := NewHost(current, previous, time.Second, table)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return host
}

func slotTime(slot uint64) time.Time {
	return time.Unix(int64(slot), 0)
}

// referenceCookie independently constructs the documented HMAC input. It is
// deliberately kept separate from the production helper so a mistake in field
// ordering or truncation is caught by the tests.
func referenceCookie(t *testing.T, key [32]byte, slot uint64, src, dst netip.AddrPort, clientISN uint32, mssPresent bool, mssIndex, windowScale, sack byte) uint32 {
	t.Helper()

	input := make([]byte, 0, 16+2+16+2+4+8+3)
	source := src.Addr().As16()
	input = append(input, source[:]...)
	input = binary.BigEndian.AppendUint16(input, src.Port())
	destination := dst.Addr().As16()
	input = append(input, destination[:]...)
	input = binary.BigEndian.AppendUint16(input, dst.Port())
	input = binary.BigEndian.AppendUint32(input, clientISN)
	input = binary.BigEndian.AppendUint64(input, slot)
	mssTag := mssIndex
	if !mssPresent {
		mssTag |= mssAbsentTag
	}
	input = append(input, mssTag, windowScale, sack)

	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(input)
	digest := mac.Sum(nil)

	low := uint32(digest[29])<<16 | uint32(digest[30])<<8 | uint32(digest[31])
	high := uint32(mssIndex<<5 | windowScale<<1 | sack)
	return high<<24 | low
}

func assertOptions(t *testing.T, got, want SYNOptions) {
	t.Helper()
	if got.MSS == nil || want.MSS == nil {
		if got.MSS != nil || want.MSS != nil {
			t.Fatalf("MSS presence = %v, want %v", got.MSS != nil, want.MSS != nil)
		}
	} else if *got.MSS != *want.MSS {
		t.Fatalf("MSS = %d, want %d", *got.MSS, *want.MSS)
	}

	if got.WindowScale == nil || want.WindowScale == nil {
		if got.WindowScale != nil || want.WindowScale != nil {
			t.Fatalf("window scale presence = %v, want %v", got.WindowScale != nil, want.WindowScale != nil)
		}
	} else if *got.WindowScale != *want.WindowScale {
		t.Fatalf("window scale = %d, want %d", *got.WindowScale, *want.WindowScale)
	}

	if got.SACKPermitted != want.SACKPermitted {
		t.Fatalf("SACK = %v, want %v", got.SACKPermitted, want.SACKPermitted)
	}
}

func TestGenerateCookieUsesRealHMAC(t *testing.T) {
	table := []uint16{536, 1200, 1460, 9000}
	key := testKey(7)
	host := testHost(t, key, testKey(99), table)
	now := slotTime(1234)

	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 0x11223344, SYNOptions{
		MSS:           MSSOption(1400), // rounds down to table index 1 (1200)
		WindowScale:   WindowScaleOption(7),
		SACKPermitted: true,
	})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	want := referenceCookie(t, key, host.SlotAt(now), testClient, testServer, 0x11223344, true, 1, 7, 1)
	if cookie != want {
		t.Fatalf("cookie = %#08x, reference HMAC cookie = %#08x", cookie, want)
	}
	if got := byte(cookie >> 24); got != 1<<5|7<<1|1 {
		t.Fatalf("high byte = %#02x, want %#02x", got, 1<<5|7<<1|1)
	}

	got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 0x11223344)
	if err != nil {
		t.Fatalf("VerifyCookieAt: %v", err)
	}
	assertOptions(t, got, SYNOptions{
		MSS:           MSSOption(1200),
		WindowScale:   WindowScaleOption(7),
		SACKPermitted: true,
	})
}

func TestTamperedHighByteRejected(t *testing.T) {
	host := testHost(t, testKey(1), testKey(2), []uint16{536, 1200, 1460})
	now := slotTime(500)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 42, SYNOptions{
		MSS:           MSSOption(1460),
		WindowScale:   WindowScaleOption(4),
		SACKPermitted: true,
	})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	originalHigh := byte(cookie >> 24)
	low := cookie & 0x00ffffff
	for high := 0; high < 256; high++ {
		if byte(high) == originalHigh {
			continue
		}
		tampered := low | uint32(high)<<24
		if _, err := host.VerifyCookieAt(now, tampered, tampered+1, testClient, testServer, 42); !errors.Is(err, ErrBadCookie) {
			t.Fatalf("high byte %#02x: error = %v, want ErrBadCookie", high, err)
		}
	}
}

func TestTamperedLowBitsRejected(t *testing.T) {
	host := testHost(t, testKey(3), testKey(4), []uint16{1460})
	now := slotTime(501)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 43, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	for bit := uint(0); bit < 24; bit++ {
		tampered := cookie ^ uint32(1)<<bit
		if _, err := host.VerifyCookieAt(now, tampered, tampered+1, testClient, testServer, 43); !errors.Is(err, ErrBadCookie) {
			t.Fatalf("bit %d: error = %v, want ErrBadCookie", bit, err)
		}
	}
}

func TestTupleBinding(t *testing.T) {
	host := testHost(t, testKey(5), testKey(6), []uint16{1460})
	now := slotTime(502)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 44, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	cases := []struct {
		name string
		src  netip.AddrPort
		dst  netip.AddrPort
		isn  uint32
	}{
		{name: "source address", src: netip.MustParseAddrPort("192.0.2.11:40000"), dst: testServer, isn: 44},
		{name: "source port", src: netip.MustParseAddrPort("192.0.2.10:40001"), dst: testServer, isn: 44},
		{name: "destination address", src: testClient, dst: netip.MustParseAddrPort("198.51.100.21:443"), isn: 44},
		{name: "destination port", src: testClient, dst: netip.MustParseAddrPort("198.51.100.20:444"), isn: 44},
		{name: "client ISN", src: testClient, dst: testServer, isn: 45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := host.VerifyCookieAt(now, cookie, cookie+1, tc.src, tc.dst, tc.isn); !errors.Is(err, ErrBadCookie) {
				t.Fatalf("error = %v, want ErrBadCookie", err)
			}
		})
	}
}

func TestACKMustAcknowledgeCookie(t *testing.T) {
	if !acknowledgesCookie(0xffffffff, 0) {
		t.Fatal("ACK sequence did not wrap modulo 2^32")
	}
	if acknowledgesCookie(0xfffffffe, 0) {
		t.Fatal("ACK sequence accepted without wrapping")
	}

	host := testHost(t, testKey(8), testKey(9), []uint16{1460})
	now := slotTime(503)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 46, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	for _, ack := range []uint32{cookie, cookie + 2, cookie + 0x100} {
		if _, err := host.VerifyCookieAt(now, cookie, ack, testClient, testServer, 46); !errors.Is(err, ErrBadACK) {
			t.Fatalf("ACK %#x: error = %v, want ErrBadACK", ack, err)
		}
	}
	if _, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 46); err != nil {
		t.Fatalf("correct ACK: %v", err)
	}
}

func TestAllHighByteEncodingsRoundTrip(t *testing.T) {
	table := []uint16{400, 536, 800, 1000, 1200, 1460, 2000, 9000}
	host := testHost(t, testKey(10), testKey(11), table)
	now := slotTime(504)
	seen := make(map[byte]bool)

	for index := 0; index < 8; index++ {
		for wsCode := 0; wsCode < 16; wsCode++ {
			for _, sack := range []bool{false, true} {
				opts := SYNOptions{
					MSS:           MSSOption(table[index]),
					SACKPermitted: sack,
				}
				if wsCode != windowScaleAbsent {
					opts.WindowScale = WindowScaleOption(uint8(wsCode))
				}

				cookie, err := host.GenerateCookieAt(now, testClient, testServer, 47, opts)
				if err != nil {
					t.Fatalf("index %d ws %d sack %v: GenerateCookieAt: %v", index, wsCode, sack, err)
				}
				high := byte(cookie >> 24)
				wantHigh := byte(index<<5 | wsCode<<1)
				if sack {
					wantHigh |= 1
				}
				if high != wantHigh {
					t.Fatalf("index %d ws %d sack %v: high = %#02x, want %#02x", index, wsCode, sack, high, wantHigh)
				}
				seen[high] = true

				got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 47)
				if err != nil {
					t.Fatalf("index %d ws %d sack %v: VerifyCookieAt: %v", index, wsCode, sack, err)
				}
				want := SYNOptions{MSS: MSSOption(table[index]), SACKPermitted: sack}
				if wsCode != windowScaleAbsent {
					want.WindowScale = WindowScaleOption(uint8(wsCode))
				}
				assertOptions(t, got, want)
			}
		}
	}

	if len(seen) != 256 {
		t.Fatalf("enumerated %d distinct high bytes, want 256", len(seen))
	}
}

func TestMissingOptionsAreNotFabricated(t *testing.T) {
	table := []uint16{400, 536, 800, 1000, 1200, 1460, 2000, 9000}
	host := testHost(t, testKey(12), testKey(13), table)
	now := slotTime(505)

	for wsCode := 0; wsCode < 16; wsCode++ {
		for _, sack := range []bool{false, true} {
			opts := SYNOptions{SACKPermitted: sack}
			if wsCode != windowScaleAbsent {
				opts.WindowScale = WindowScaleOption(uint8(wsCode))
			}
			cookie, err := host.GenerateCookieAt(now, testClient, testServer, 48, opts)
			if err != nil {
				t.Fatalf("ws %d sack %v: GenerateCookieAt: %v", wsCode, sack, err)
			}
			got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 48)
			if err != nil {
				t.Fatalf("ws %d sack %v: VerifyCookieAt: %v", wsCode, sack, err)
			}
			assertOptions(t, got, opts)
		}
	}

	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 49, SYNOptions{WindowScale: WindowScaleOption(3)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}
	for _, index := range []byte{1, 2, 7} {
		tampered := (cookie & 0x00ffffff) | uint32(index)<<29 | cookie&0x1f000000
		if _, err := host.VerifyCookieAt(now, tampered, tampered+1, testClient, testServer, 49); !errors.Is(err, ErrBadCookie) {
			t.Fatalf("tampered absent-MSS index %d: error = %v, want ErrBadCookie", index, err)
		}
	}
}

func TestWindowScaleZeroIsPresent(t *testing.T) {
	host := testHost(t, testKey(14), testKey(15), []uint16{1460})
	now := slotTime(506)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 50, SYNOptions{
		MSS:         MSSOption(1460),
		WindowScale: WindowScaleOption(0),
	})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}
	got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 50)
	if err != nil {
		t.Fatalf("VerifyCookieAt: %v", err)
	}
	if got.WindowScale == nil || *got.WindowScale != 0 {
		t.Fatalf("window scale = %v, want present zero", got.WindowScale)
	}
}

func TestMSSRoundsDownToLargestTableEntry(t *testing.T) {
	table := []uint16{9000, 1200, 536, 1460}
	host := testHost(t, testKey(16), testKey(17), table)
	now := slotTime(507)

	cases := []struct {
		advertised uint16
		want       uint16
	}{
		{advertised: 536, want: 536},
		{advertised: 800, want: 536},
		{advertised: 1400, want: 1200},
		{advertised: 1460, want: 1460},
		{advertised: 5000, want: 1460},
		{advertised: 9000, want: 9000},
	}
	for _, tc := range cases {
		cookie, err := host.GenerateCookieAt(now, testClient, testServer, 51, SYNOptions{MSS: MSSOption(tc.advertised)})
		if err != nil {
			t.Fatalf("advertised %d: GenerateCookieAt: %v", tc.advertised, err)
		}
		got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 51)
		if err != nil {
			t.Fatalf("advertised %d: VerifyCookieAt: %v", tc.advertised, err)
		}
		if got.MSS == nil || *got.MSS != tc.want {
			t.Fatalf("advertised %d: MSS = %v, want %d", tc.advertised, got.MSS, tc.want)
		}
	}

	if _, err := host.GenerateCookieAt(now, testClient, testServer, 51, SYNOptions{MSS: MSSOption(535)}); !errors.Is(err, ErrNoMSSMatch) {
		t.Fatalf("advertised below table: error = %v, want ErrNoMSSMatch", err)
	}

	empty := testHost(t, testKey(18), testKey(19), nil)
	if _, err := empty.GenerateCookieAt(now, testClient, testServer, 51, SYNOptions{MSS: MSSOption(1460)}); !errors.Is(err, ErrNoMSSMatch) {
		t.Fatalf("empty table: error = %v, want ErrNoMSSMatch", err)
	}
}

func TestPreviousKeyGraceAndSlotWrap(t *testing.T) {
	table := []uint16{1460}
	keyOld := testKey(20)
	keyNew := testKey(60)
	keyNewer := testKey(100)

	hostAt255 := testHost(t, keyOld, testKey(140), table)
	now255 := slotTime(255)
	if got := byte(hostAt255.SlotAt(now255)); got != 255 {
		t.Fatalf("low slot at 255 = %d", got)
	}
	cookie, err := hostAt255.GenerateCookieAt(now255, testClient, testServer, 52, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	// At slot 256 the 8-bit slot has wrapped to zero. keyOld is now the
	// previous generation and is accepted for exactly one slot.
	hostAt256 := testHost(t, keyNew, keyOld, table)
	now256 := slotTime(256)
	if got := byte(hostAt256.SlotAt(now256)); got != 0 {
		t.Fatalf("low slot at 256 = %d", got)
	}
	got, err := hostAt256.VerifyCookieAt(now256, cookie, cookie+1, testClient, testServer, 52)
	if err != nil {
		t.Fatalf("previous key across wrap: %v", err)
	}
	assertOptions(t, got, SYNOptions{MSS: MSSOption(1460)})

	// One more slot later the previous generation is no longer valid.
	if _, err := hostAt256.VerifyCookieAt(slotTime(257), cookie, cookie+1, testClient, testServer, 52); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("expired previous key: error = %v, want ErrBadCookie", err)
	}

	hostAt257 := testHost(t, keyNewer, keyNew, table)
	if _, err := hostAt257.VerifyCookieAt(slotTime(257), cookie, cookie+1, testClient, testServer, 52); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("two generations old: error = %v, want ErrBadCookie", err)
	}
}

func TestSlotWrapBindsFull64BitEpoch(t *testing.T) {
	table := []uint16{1460}
	key := testKey(21)
	host := testHost(t, key, testKey(22), table)

	early := slotTime(0)
	cookie, err := host.GenerateCookieAt(early, testClient, testServer, 53, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}

	// Slot 256 has the same low eight bits as slot 0. A verifier that only
	// compared the 8-bit generation would accept this old cookie as current.
	if _, err := host.VerifyCookieAt(slotTime(256), cookie, cookie+1, testClient, testServer, 53); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("cookie from 256 slots ago: error = %v, want ErrBadCookie", err)
	}

	fresh := slotTime(256)
	cookie, err = host.GenerateCookieAt(fresh, testClient, testServer, 53, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt at 256: %v", err)
	}
	if _, err := host.VerifyCookieAt(fresh, cookie, cookie+1, testClient, testServer, 53); err != nil {
		t.Fatalf("fresh cookie at 256: %v", err)
	}
}

func TestRotateKeys(t *testing.T) {
	table := []uint16{1460}
	host := testHost(t, testKey(23), testKey(24), table)
	now := slotTime(300)
	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 54, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}
	if _, err := host.VerifyCookieAt(slotTime(301), cookie, cookie+1, testClient, testServer, 54); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("current key accepted in the next slot without rotation: error = %v, want ErrBadCookie", err)
	}

	host.RotateKeys(testKey(25))
	if _, err := host.VerifyCookieAt(slotTime(301), cookie, cookie+1, testClient, testServer, 54); err != nil {
		t.Fatalf("rotated previous key: %v", err)
	}

	host.RotateKeys(testKey(26))
	if _, err := host.VerifyCookieAt(slotTime(302), cookie, cookie+1, testClient, testServer, 54); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("cookie after two rotations: error = %v, want ErrBadCookie", err)
	}
}

func TestConstructorAndInputValidation(t *testing.T) {
	key := testKey(27)
	if _, err := NewHost(key, key, 0, []uint16{1460}); err == nil {
		t.Fatal("NewHost accepted zero slot duration")
	}
	if _, err := NewHost(key, key, time.Second, make([]uint16, 9)); err == nil {
		t.Fatal("NewHost accepted nine MSS entries")
	}
	if _, err := NewHost(key, key, time.Second, []uint16{1460, 0}); err == nil {
		t.Fatal("NewHost accepted a zero MSS entry")
	}

	host := testHost(t, key, key, []uint16{1460})
	if _, err := host.GenerateCookieAt(slotTime(1), netip.AddrPort{}, testServer, 1, SYNOptions{MSS: MSSOption(1460)}); err == nil {
		t.Fatal("GenerateCookieAt accepted an invalid source")
	}
	if _, err := host.GenerateCookieAt(slotTime(1), testClient, testServer, 1, SYNOptions{MSS: MSSOption(0)}); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("zero MSS: error = %v, want ErrInvalidOption", err)
	}
	if _, err := host.GenerateCookieAt(slotTime(1), testClient, testServer, 1, SYNOptions{WindowScale: WindowScaleOption(15)}); !errors.Is(err, ErrInvalidWindowScale) {
		t.Fatalf("window scale 15: error = %v, want ErrInvalidWindowScale", err)
	}
}

func TestClockBasedAPI(t *testing.T) {
	host := testHost(t, testKey(28), testKey(29), []uint16{1460})
	now := slotTime(700)
	host.SetNowFunc(func() time.Time { return now })

	if got := host.CurrentSlot(); got != 700 {
		t.Fatalf("CurrentSlot = %d, want 700", got)
	}
	cookie, err := host.GenerateCookie(testClient, testServer, 56, SYNOptions{MSS: MSSOption(1460)})
	if err != nil {
		t.Fatalf("GenerateCookie: %v", err)
	}
	if _, err := host.VerifyCookie(cookie, cookie+1, testClient, testServer, 56); err != nil {
		t.Fatalf("VerifyCookie: %v", err)
	}

	host.SetNowFunc(nil)
	if host.CurrentSlot() == 0 {
		t.Fatal("CurrentSlot returned zero after restoring time.Now")
	}
}

func TestSlotAtFloorsNegativeTimes(t *testing.T) {
	host := testHost(t, testKey(32), testKey(33), []uint16{1460})
	beforeEpoch := time.Unix(-1, 500_000_000)
	if got := host.SlotAt(beforeEpoch); got != ^uint64(0) {
		t.Fatalf("SlotAt(before epoch) = %d, want floor -1", got)
	}
}
