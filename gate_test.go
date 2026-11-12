package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log"
	"strings"
	"testing"
)

func buildFrame(txID, protoID uint16, length int, unitID uint8, pdu ...byte) []byte {
	frame := make([]byte, 0, mbapHeaderLen+len(pdu))
	frame = binary.BigEndian.AppendUint16(frame, txID)
	frame = binary.BigEndian.AppendUint16(frame, protoID)
	frame = binary.BigEndian.AppendUint16(frame, uint16(length))
	frame = append(frame, unitID)
	return append(frame, pdu...)
}

func validFrame(txID uint16, unitID uint8, pdu ...byte) []byte {
	return buildFrame(txID, 0, 1+len(pdu), unitID, pdu...)
}

func TestAdmitRequestLengthMismatch(t *testing.T) {
	// Length field claims one byte more than the PDU actually carries.
	frame := buildFrame(1, 0, 5, 1, 0x03, 0x00, 0x00)
	if _, err := AdmitRequest(frame, nil); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("want ErrLengthMismatch, got %v", err)
	}

	// Truncated frame: shorter than header plus function code.
	if _, err := AdmitRequest(validFrame(1, 1, 0x03)[:6], nil); !errors.Is(err, ErrFrameTooShort) {
		t.Fatalf("want ErrFrameTooShort, got %v", err)
	}
}

func TestAdmitRequestProtocolID(t *testing.T) {
	frame := buildFrame(1, 1, 2, 1, 0x03)
	if _, err := AdmitRequest(frame, nil); !errors.Is(err, ErrProtocolID) {
		t.Fatalf("want ErrProtocolID, got %v", err)
	}
}

func TestAdmitRequestUnitIDDenied(t *testing.T) {
	policy := &Policy{UnitIDs: map[uint8]bool{1: true, 2: true}, AllowWrites: true}
	// Attacker rewrites the unit ID to reach another production line's slave.
	frame := validFrame(7, 9, 0x03, 0x00, 0x00, 0x00, 0x01)
	if _, err := AdmitRequest(frame, policy); !errors.Is(err, ErrUnitIDDenied) {
		t.Fatalf("want ErrUnitIDDenied, got %v", err)
	}
	if _, err := AdmitRequest(validFrame(7, 2, 0x03, 0x00, 0x00, 0x00, 0x01), policy); err != nil {
		t.Fatalf("allowed unit ID rejected: %v", err)
	}
}

func TestAdmitRequestFuncCodeDenied(t *testing.T) {
	policy := &Policy{FuncCodes: map[uint8]bool{3: true}, AllowWrites: true}
	if _, err := AdmitRequest(validFrame(1, 1, 0x04, 0x00), policy); !errors.Is(err, ErrFuncCodeDenied) {
		t.Fatalf("want ErrFuncCodeDenied, got %v", err)
	}
}

func TestAdmitRequestWriteCoilDeniedByDefault(t *testing.T) {
	// FC 5: write single coil on another line's slave.
	frame := validFrame(3, 1, 0x05, 0x00, 0x01, 0xFF, 0x00)
	if _, err := AdmitRequest(frame, nil); !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("want ErrWriteDenied, got %v", err)
	}
	for _, fc := range []uint8{5, 6, 15, 16, 23} {
		if _, err := AdmitRequest(validFrame(3, 1, fc, 0x00), nil); !errors.Is(err, ErrWriteDenied) {
			t.Fatalf("function code %d: want ErrWriteDenied, got %v", fc, err)
		}
	}
	// Explicit opt-in allows writes.
	if _, err := AdmitRequest(frame, &Policy{AllowWrites: true}); err != nil {
		t.Fatalf("AllowWrites should admit write coil, got %v", err)
	}
}

func TestMatcherTransactionConflict(t *testing.T) {
	m := NewMatcher(nil)
	first, err := AdmitRequest(validFrame(42, 1, 0x03, 0x00), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AdmitRequest(validFrame(42, 2, 0x03, 0x00), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Track(first); err != nil {
		t.Fatal(err)
	}
	if err := m.Track(second); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("want ErrTransactionConflict, got %v", err)
	}
}

func TestMatcherNormalResponse(t *testing.T) {
	m := NewMatcher(nil)
	req, _ := AdmitRequest(validFrame(10, 1, 0x03, 0x00), nil)
	if err := m.Track(req); err != nil {
		t.Fatal(err)
	}
	resp, ok := m.MatchResponse(validFrame(10, 1, 0x03, 0x02, 0x11, 0x22))
	if !ok {
		t.Fatal("matching response dropped")
	}
	if resp.Exception || resp.FuncCode != 0x03 || resp.TransactionID != 10 || resp.UnitID != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	// Transaction is consumed; a replayed response must be dropped.
	if _, ok := m.MatchResponse(validFrame(10, 1, 0x03, 0x02, 0x11, 0x22)); ok {
		t.Fatal("replayed response should be dropped")
	}
}

func TestMatcherExceptionResponse(t *testing.T) {
	m := NewMatcher(nil)
	req, _ := AdmitRequest(validFrame(11, 1, 0x03, 0x00), nil)
	if err := m.Track(req); err != nil {
		t.Fatal(err)
	}
	// Exception response: function code 0x03 | 0x80, exception code 2.
	resp, ok := m.MatchResponse(validFrame(11, 1, 0x83, 0x02))
	if !ok {
		t.Fatal("exception response dropped")
	}
	if !resp.Exception || resp.ExceptionCode != 2 || resp.FuncCode != 0x03 {
		t.Fatalf("unexpected exception response: %+v", resp)
	}
}

func TestMatcherDropsMismatchedResponses(t *testing.T) {
	var buf bytes.Buffer
	m := NewMatcher(log.New(&buf, "", 0))
	req, _ := AdmitRequest(validFrame(20, 1, 0x03, 0x00), nil)
	if err := m.Track(req); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		// Attacker races to answer someone else's transaction.
		"unknown transaction": validFrame(99, 1, 0x03, 0x02, 0x00, 0x00),
		// Response from a different unit ID than the request targeted.
		"wrong unit": validFrame(20, 2, 0x03, 0x02, 0x00, 0x00),
		// Function code does not match the pending request.
		"wrong function": validFrame(20, 1, 0x04, 0x02, 0x00, 0x00),
		// Corrupted MBAP length.
		"bad length": buildFrame(20, 0, 9, 1, 0x03, 0x02, 0x00, 0x00),
	}
	for name, frame := range cases {
		if _, ok := m.MatchResponse(frame); ok {
			t.Fatalf("%s: response should be dropped", name)
		}
	}
	logOut := buf.String()
	for _, want := range []string{"no pending transaction 99", "unit ID 2", "function code 0x04", "malformed response"} {
		if !strings.Contains(logOut, want) {
			t.Fatalf("drop log missing %q, got: %s", want, logOut)
		}
	}

	// Pending entry survives mismatches; the genuine response still matches.
	if _, ok := m.MatchResponse(validFrame(20, 1, 0x03, 0x02, 0x00, 0x01)); !ok {
		t.Fatal("genuine response dropped after mismatches")
	}
}
