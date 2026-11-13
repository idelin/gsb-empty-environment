package netsec

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func testTicketCipher(t *testing.T) *TicketCipher {
	t.Helper()
	key := bytes.Repeat([]byte{0x7A}, ticketKeyLen)
	c, err := NewTicketCipher(key, 3)
	if err != nil {
		t.Fatalf("NewTicketCipher: %v", err)
	}
	return c
}

func TestTicketRoundTrip(t *testing.T) {
	c := testTicketCipher(t)
	data := &TicketData{SessionID: bytes.Repeat([]byte{0x9D}, 16)}

	ticket, err := c.Seal(data)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := c.Open(ticket)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened.SessionID, data.SessionID) {
		t.Fatal("session ID changed across ticket round trip")
	}
}

func TestTicketSealedNonDeterministically(t *testing.T) {
	c := testTicketCipher(t)
	data := &TicketData{SessionID: []byte("session-id-value")}

	first, err := c.Seal(data)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := c.Seal(data)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("tickets must use fresh random nonces")
	}
}

func TestTicketTamperRejected(t *testing.T) {
	c := testTicketCipher(t)
	ticket, err := c.Seal(&TicketData{SessionID: []byte("session-id-value")})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	mutated := append([]byte(nil), ticket...)
	mutated[len(mutated)-1] ^= 0x01
	if _, err := c.Open(mutated); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("tampered ticket err = %v, want %v", err, ErrTicketInvalid)
	}

	truncated := ticket[:len(ticket)-2]
	if _, err := c.Open(truncated); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("truncated ticket err = %v, want %v", err, ErrTicketInvalid)
	}

	if _, err := c.Open([]byte("not a ticket at all")); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("garbage ticket err = %v, want %v", err, ErrTicketInvalid)
	}
}

func TestTicketKeyMismatch(t *testing.T) {
	first := testTicketCipher(t)
	ticket, err := first.Seal(&TicketData{SessionID: []byte("session-id-value")})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	other, err := NewTicketCipher(bytes.Repeat([]byte{0x11}, ticketKeyLen), 3)
	if err != nil {
		t.Fatalf("NewTicketCipher: %v", err)
	}
	if _, err := other.Open(ticket); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("foreign key ticket err = %v, want %v", err, ErrTicketInvalid)
	}

	rotated, err := NewTicketCipher(bytes.Repeat([]byte{0x7A}, ticketKeyLen), 4)
	if err != nil {
		t.Fatalf("NewTicketCipher: %v", err)
	}
	if _, err := rotated.Open(ticket); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("rotated key id ticket err = %v, want %v", err, ErrTicketInvalid)
	}
}

func TestTicketRejectsBadKeyAndPayload(t *testing.T) {
	if _, err := NewTicketCipher([]byte("short"), 1); err == nil {
		t.Fatal("short key must be rejected")
	}
	c := testTicketCipher(t)
	if _, err := c.Seal(nil); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("nil data err = %v, want %v", err, ErrTicketInvalid)
	}
	if _, err := c.Seal(&TicketData{}); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("empty session id err = %v, want %v", err, ErrTicketInvalid)
	}
}

func TestTicketDataCarriesNoEMSFlag(t *testing.T) {
	typ := reflect.TypeOf(TicketData{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		lower := bytes.ToLower([]byte(name))
		if bytes.Contains(lower, []byte("ems")) ||
			bytes.Contains(lower, []byte("extended")) ||
			bytes.Contains(lower, []byte("master")) {
			t.Fatalf("resumption ticket field %q must not carry EMS/master-secret state", name)
		}
	}

	c := testTicketCipher(t)
	ticket, err := c.Seal(&TicketData{SessionID: []byte("opaque-session-id")})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ticket, []byte("extended")) || bytes.Contains(ticket, []byte("master")) {
		t.Fatal("ticket wire bytes must not embed EMS labels")
	}
}
