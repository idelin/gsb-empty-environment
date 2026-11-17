package netsec

import (
	"errors"
	"testing"
)

func TestDatagramRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		target Address
	}{
		{name: "ipv4", target: Address{Host: "192.0.2.10", Port: 53}},
		{name: "ipv6", target: Address{Host: "2001:db8::10", Port: 443}},
		{name: "domain", target: Address{Host: "example.com", Port: 5353}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte("udp payload")
			packet, err := EncodeDatagram(tc.target, payload)
			if err != nil {
				t.Fatalf("EncodeDatagram: %v", err)
			}
			target, gotPayload, err := ParseDatagram(packet)
			if err != nil {
				t.Fatalf("ParseDatagram: %v", err)
			}
			if target != tc.target {
				t.Fatalf("target = %#v, want %#v", target, tc.target)
			}
			if string(gotPayload) != string(payload) {
				t.Fatalf("payload = %q, want %q", gotPayload, payload)
			}
		})
	}
}

func TestParseDatagramRejectsInvalidHeaders(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		want   error
	}{
		{
			name:   "short",
			packet: []byte{0, 0, 0},
			want:   ErrDatagramTooShort,
		},
		{
			name:   "reserved",
			packet: []byte{0, 1, 0, atypIPv4, 127, 0, 0, 1, 0, 53},
			want:   ErrReservedNonZero,
		},
		{
			name:   "fragmented",
			packet: []byte{0, 0, 1, atypIPv4, 127, 0, 0, 1, 0, 53},
			want:   ErrFragmented,
		},
		{
			name:   "domain length beyond packet",
			packet: []byte{0, 0, 0, atypDomain, 5, 'a', 'b'},
			want:   ErrInvalidDomainLength,
		},
		{
			name:   "empty domain",
			packet: []byte{0, 0, 0, atypDomain, 0, 0, 53},
			want:   ErrInvalidDomainLength,
		},
		{
			name:   "domain missing port",
			packet: []byte{0, 0, 0, atypDomain, 1, 'a', 0},
			want:   ErrDatagramTooShort,
		},
		{
			name:   "truncated ipv4",
			packet: []byte{0, 0, 0, atypIPv4, 127, 0, 0},
			want:   ErrDatagramTooShort,
		},
		{
			name:   "unknown address type",
			packet: []byte{0, 0, 0, 0x02, 0, 0},
			want:   ErrUnsupportedAddressType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseDatagram(tc.packet)
			if !errors.Is(err, ErrInvalidDatagram) {
				t.Fatalf("error = %v, want ErrInvalidDatagram", err)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEncodeDatagramRejectsOversizedDomain(t *testing.T) {
	host := make([]byte, 256)
	for i := range host {
		host[i] = 'a'
	}
	if _, err := EncodeDatagram(Address{Host: string(host), Port: 53}, nil); !errors.Is(err, ErrInvalidDomainLength) {
		t.Fatalf("error = %v, want ErrInvalidDomainLength", err)
	}
}
