package netsec

import (
	"errors"
	"fmt"
	"testing"
)

func testSpec() *Spec {
	return &Spec{
		Fields: map[int]FieldSpec{
			2:  {Kind: LLVAR, Max: 19},
			3:  {Kind: Fixed, Max: 6},
			11: {Kind: Fixed, Max: 6},
			52: {Kind: Fixed, Max: 8},
			70: {Kind: Fixed, Max: 3},
		},
		PINAllowedMTIs: map[string]bool{"0200": true},
	}
}

// bitmapWith builds a bitmap (8 or 16 bytes) with the given bits set.
// Bit 1 is set automatically when any bit > 64 is requested.
func bitmapWith(bits ...int) []byte {
	n := 8
	for _, b := range bits {
		if b > 64 {
			n = 16
		}
	}
	bm := make([]byte, n)
	if n == 16 {
		bm[0] |= 0x80
	}
	for _, b := range bits {
		bm[(b-1)/8] |= 0x80 >> uint((b-1)%8)
	}
	return bm
}

func TestAdmitValid(t *testing.T) {
	body := append(bitmapWith(3, 11, 52),
		[]byte("000000123456")...,
	)
	body = append(body, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88)
	msg, err := testSpec().Admit("0200", body)
	if err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	if msg.PIN == nil {
		t.Fatal("expected PIN block to be parsed")
	}
	if got := msg.Fields[52]; got != nil {
		t.Fatalf("raw PIN bytes must not be retained in Fields, got %x", got)
	}
	if string(msg.Fields[3]) != "000000" {
		t.Fatalf("field 3 mismatch: %q", msg.Fields[3])
	}
}

func TestAdmitUndefinedBit(t *testing.T) {
	// Bit 41 is not in the caller's field table.
	body := append(bitmapWith(3, 41), []byte("000000")...)
	_, err := testSpec().Admit("0200", body)
	if !errors.Is(err, ErrUndefinedField) {
		t.Fatalf("want ErrUndefinedField, got %v", err)
	}
}

func TestAdmitUndefinedSecondaryBit(t *testing.T) {
	// Bit 1 set, secondary bitmap carries undefined bit 100.
	body := append(bitmapWith(3, 100), []byte("000000")...)
	_, err := testSpec().Admit("0200", body)
	if !errors.Is(err, ErrUndefinedField) {
		t.Fatalf("want ErrUndefinedField, got %v", err)
	}
}

func TestAdmitSecondaryBitmap(t *testing.T) {
	// Defined secondary-bitmap field 70 parses fine.
	body := append(bitmapWith(3, 70), []byte("000000001")...)
	msg, err := testSpec().Admit("0810", body)
	if err != nil {
		t.Fatalf("secondary bitmap message rejected: %v", err)
	}
	if string(msg.Fields[70]) != "001" {
		t.Fatalf("field 70 mismatch: %q", msg.Fields[70])
	}
}

func TestAdmitLLVAROverRemaining(t *testing.T) {
	// Field 2 declares 19 bytes but only 3 remain.
	body := append(bitmapWith(2), 19, '1', '2', '3')
	_, err := testSpec().Admit("0200", body)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow, got %v", err)
	}
}

func TestAdmitLLVAROverFieldLimit(t *testing.T) {
	// Field 2 limit is 19; attacker declares 99 and supplies 99 bytes.
	body := append(bitmapWith(2), 99)
	body = append(body, make([]byte, 99)...)
	_, err := testSpec().Admit("0200", body)
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("want ErrLengthOverflow, got %v", err)
	}
}

func TestAdmitLLLVARBounds(t *testing.T) {
	spec := testSpec()
	spec.Fields[48] = FieldSpec{Kind: LLLVAR, Max: 10}

	// Declared 0x00 0x20 = 32 > limit 10.
	body := append(bitmapWith(48), 0x00, 0x20)
	body = append(body, make([]byte, 32)...)
	if _, err := spec.Admit("0200", body); !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("LLLVAR over limit: want ErrLengthOverflow, got %v", err)
	}

	// Declared 8 <= limit 10 but only 2 bytes remain.
	body = append(bitmapWith(48), 0x00, 0x08, 'a', 'b')
	if _, err := spec.Admit("0200", body); !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("LLLVAR over remaining: want ErrLengthOverflow, got %v", err)
	}

	// Well-formed LLLVAR passes.
	body = append(bitmapWith(48), 0x00, 0x05, 'h', 'e', 'l', 'l', 'o')
	msg, err := spec.Admit("0200", body)
	if err != nil {
		t.Fatalf("valid LLLVAR rejected: %v", err)
	}
	if string(msg.Fields[48]) != "hello" {
		t.Fatalf("field 48 mismatch: %q", msg.Fields[48])
	}
}

func TestAdmitPINUnauthorizedMTI(t *testing.T) {
	// 0800 network management message must not carry field 52.
	body := append(bitmapWith(52), make([]byte, 8)...)
	_, err := testSpec().Admit("0800", body)
	if !errors.Is(err, ErrPINNotAllowed) {
		t.Fatalf("want ErrPINNotAllowed, got %v", err)
	}
}

func TestAdmitPINWrongLength(t *testing.T) {
	// Caller mis-registers field 52 as LLVAR; a 4-byte "PIN" must fail.
	spec := testSpec()
	spec.Fields[52] = FieldSpec{Kind: LLVAR, Max: 8}
	body := append(bitmapWith(52), 4, 1, 2, 3, 4)
	_, err := spec.Admit("0200", body)
	if !errors.Is(err, ErrPINLength) {
		t.Fatalf("want ErrPINLength, got %v", err)
	}
}

func TestPINLogBlanked(t *testing.T) {
	pin := PINBlock{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4}
	if s := pin.String(); s != "" {
		t.Fatalf("String() must be blank, got %q", s)
	}
	if s := fmt.Sprintf("%v %#v %s", pin, pin, pin); s != "  " {
		t.Fatalf("log forms must be blank, got %q", s)
	}
}

func TestAdmitTruncated(t *testing.T) {
	if _, err := testSpec().Admit("0200", []byte{1, 2, 3}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// Bit 1 set but no room for the secondary bitmap.
	body := bitmapWith(3)
	body[0] |= 0x80
	if _, err := testSpec().Admit("0200", body); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}
