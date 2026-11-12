package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	netsec "example.org/netsec/q0263"
)

func TestParseHTTPDatagramAcceptsContextIDZero(t *testing.T) {
	t.Parallel()

	encodings := [][]byte{
		{0x00},
		{0x40, 0x00},
		{0x80, 0x00, 0x00, 0x00},
		{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}

	for _, prefix := range encodings {
		prefix := prefix
		t.Run(fmt.Sprintf("%x", prefix), func(t *testing.T) {
			t.Parallel()
			wantPayload := []byte{0xde, 0xad, 0xbe, 0xef}
			datagram := append(append([]byte(nil), prefix...), wantPayload...)

			got, err := netsec.ParseHTTPDatagram(datagram)
			if err != nil {
				t.Fatalf("ParseHTTPDatagram: %v", err)
			}
			if got.ContextID != 0 {
				t.Fatalf("ContextID = %d, want 0", got.ContextID)
			}
			if !bytes.Equal(got.Payload, wantPayload) {
				t.Fatalf("payload = %x, want %x", got.Payload, wantPayload)
			}

			payload, ok := netsec.UDPPayload(datagram)
			if !ok || !bytes.Equal(payload, wantPayload) {
				t.Fatalf("UDPPayload = (%x, %t), want (%x, true)", payload, ok, wantPayload)
			}
		})
	}
}

func TestParseHTTPDatagramDropsNonZeroContextIDs(t *testing.T) {
	t.Parallel()

	datagrams := [][]byte{
		{0x01, 0xaa},
		{0x40, 0x01, 0xaa},
		{0x80, 0x00, 0x00, 0x01, 0xaa},
		{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0xaa},
		{0x7f, 0xff, 0xaa},
	}

	for _, datagram := range datagrams {
		datagram := datagram
		t.Run(fmt.Sprintf("%x", datagram), func(t *testing.T) {
			t.Parallel()
			if _, err := netsec.ParseHTTPDatagram(datagram); !errors.Is(err, netsec.ErrUnsupportedContextID) {
				t.Fatalf("ParseHTTPDatagram(%x) error = %v, want ErrUnsupportedContextID", datagram, err)
			}
			if payload, ok := netsec.UDPPayload(datagram); ok || payload != nil {
				t.Fatalf("UDPPayload(%x) = (%x, true), want dropped", datagram, payload)
			}
		})
	}
}

func TestParseHTTPDatagramRejectsMalformedVarints(t *testing.T) {
	t.Parallel()

	datagrams := [][]byte{
		nil,
		{},
		{0x40},
		{0x80, 0x00},
		{0x80, 0x00, 0x00},
		{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}

	for _, datagram := range datagrams {
		datagram := datagram
		t.Run(fmt.Sprintf("%x", datagram), func(t *testing.T) {
			t.Parallel()
			if _, err := netsec.ParseHTTPDatagram(datagram); !errors.Is(err, netsec.ErrMalformedDatagram) {
				t.Fatalf("ParseHTTPDatagram(%x) error = %v, want ErrMalformedDatagram", datagram, err)
			}
			if payload, ok := netsec.UDPPayload(datagram); ok || payload != nil {
				t.Fatalf("UDPPayload(%x) = (%x, true), want dropped", datagram, payload)
			}
		})
	}
}

func TestContextIDZeroWithEmptyUDPPayload(t *testing.T) {
	t.Parallel()

	payload, ok := netsec.UDPPayload([]byte{0x00})
	if !ok {
		t.Fatal("empty UDP payload with Context ID 0 was dropped")
	}
	if len(payload) != 0 {
		t.Fatalf("payload length = %d, want 0", len(payload))
	}
}
