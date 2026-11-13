package netsec

import (
	"errors"
	"testing"
)

func TestParseSYNOptions(t *testing.T) {
	raw := []byte{
		tcpOptionNOP,
		tcpOptionMSS, 4, 0x05, 0x78, // 1400
		tcpOptionWindowScale, 3, 7,
		tcpOptionSACK, 2,
		30, 3, 0xaa, // unknown but well-formed
		tcpOptionEnd, 0, 0,
	}
	opts, err := ParseSYNOptions(raw)
	if err != nil {
		t.Fatalf("ParseSYNOptions: %v", err)
	}
	assertOptions(t, opts, SYNOptions{
		MSS:           MSSOption(1400),
		WindowScale:   WindowScaleOption(7),
		SACKPermitted: true,
	})

	alias, err := ParseOptions(raw)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	assertOptions(t, alias, opts)
}

func TestParseSYNOptionsRejectsDuplicatesAndConflicts(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{
			name: "duplicate SACK",
			raw:  []byte{tcpOptionMSS, 4, 0x05, 0xb4, tcpOptionSACK, 2, tcpOptionSACK, 2},
			want: ErrDuplicateOption,
		},
		{
			name: "duplicate MSS same value",
			raw:  []byte{tcpOptionMSS, 4, 0x05, 0xb4, tcpOptionMSS, 4, 0x05, 0xb4},
			want: ErrDuplicateOption,
		},
		{
			name: "conflicting MSS",
			raw:  []byte{tcpOptionMSS, 4, 0x05, 0xb4, tcpOptionMSS, 4, 0x05, 0xdc},
			want: ErrConflictingOption,
		},
		{
			name: "duplicate window scale",
			raw:  []byte{tcpOptionWindowScale, 3, 7, tcpOptionWindowScale, 3, 7},
			want: ErrDuplicateOption,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSYNOptions(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseSYNOptionsRejectsMalformedOptions(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{name: "truncated kind", raw: []byte{tcpOptionMSS}, want: ErrInvalidOption},
		{name: "zero length", raw: []byte{tcpOptionMSS, 0}, want: ErrInvalidOption},
		{name: "length past end", raw: []byte{tcpOptionMSS, 5, 0x05}, want: ErrInvalidOption},
		{name: "short MSS", raw: []byte{tcpOptionMSS, 3, 0x05}, want: ErrInvalidOption},
		{name: "zero MSS", raw: []byte{tcpOptionMSS, 4, 0, 0}, want: ErrInvalidOption},
		{name: "short window scale", raw: []byte{tcpOptionWindowScale, 2}, want: ErrInvalidOption},
		{name: "window scale too large", raw: []byte{tcpOptionWindowScale, 3, 15}, want: ErrInvalidWindowScale},
		{name: "short SACK", raw: []byte{tcpOptionSACK, 3, 0}, want: ErrInvalidOption},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSYNOptions(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParsedOptionsRoundTripThroughCookie(t *testing.T) {
	host := testHost(t, testKey(30), testKey(31), []uint16{536, 1200, 1460})
	now := slotTime(600)
	opts, err := ParseSYNOptions([]byte{
		tcpOptionMSS, 4, 0x05, 0x78, // advertised 1400 -> encoded 1200
		tcpOptionWindowScale, 3, 0, // present zero
		tcpOptionSACK, 2,
	})
	if err != nil {
		t.Fatalf("ParseSYNOptions: %v", err)
	}

	cookie, err := host.GenerateCookieAt(now, testClient, testServer, 55, opts)
	if err != nil {
		t.Fatalf("GenerateCookieAt: %v", err)
	}
	got, err := host.VerifyCookieAt(now, cookie, cookie+1, testClient, testServer, 55)
	if err != nil {
		t.Fatalf("VerifyCookieAt: %v", err)
	}
	assertOptions(t, got, SYNOptions{
		MSS:           MSSOption(1200),
		WindowScale:   WindowScaleOption(0),
		SACKPermitted: true,
	})
}
