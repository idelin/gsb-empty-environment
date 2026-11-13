package netsec_test

import (
	"bytes"
	"errors"
	"testing"

	netsec "example.org/netsec/q0238"
)

func encodeHandshake(typ byte, body []byte) []byte {
	encoded := make([]byte, 4+len(body))
	encoded[0] = typ
	encoded[1] = byte(len(body) >> 16)
	encoded[2] = byte(len(body) >> 8)
	encoded[3] = byte(len(body))
	copy(encoded[4:], body)
	return encoded
}

func TestCloseNotifyInMiddleOfHandshakeDiscardsPartial(t *testing.T) {
	body := []byte("0123456789abcdef")
	message := encodeHandshake(1, body)

	split := 9 // Four header bytes plus five body bytes.
	firstHalf := message[:split]
	secondHalf := message[split:]

	// The two halves are a valid handshake message when concatenated. The
	// point of this test is that the alert between them prevents that.
	joined := append(append([]byte(nil), firstHalf...), secondHalf...)
	if len(joined) != 4+len(body) {
		t.Fatalf("test setup: joined halves have length %d, want %d", len(joined), 4+len(body))
	}

	machine := netsec.NewMachine()

	messages, err := machine.FeedHandshake(firstHalf)
	if err != nil {
		t.Fatalf("FeedHandshake(first half): %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("FeedHandshake(first half) returned %d messages, want 0", len(messages))
	}
	if got, want := machine.PendingBytes(), len(firstHalf); got != want {
		t.Fatalf("PendingBytes after first half = %d, want %d", got, want)
	}
	if machine.State() != netsec.StateOpen {
		t.Fatalf("State after first half = %v, want open", machine.State())
	}

	if err := machine.FeedAlert(netsec.AlertLevelWarning, netsec.AlertCloseNotify); err != nil {
		t.Fatalf("FeedAlert(close_notify): %v", err)
	}
	if machine.State() != netsec.StateClosed {
		t.Fatalf("State after close_notify = %v, want closed", machine.State())
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after close_notify = %d, want 0", got)
	}

	messages, err = machine.FeedHandshake(secondHalf)
	if !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("FeedHandshake(second half) error = %v, want ErrClosed", err)
	}
	if len(messages) != 0 {
		t.Fatalf("FeedHandshake(second half) returned %d messages after close, want 0", len(messages))
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after rejected second half = %d, want 0", got)
	}

	// The same must hold for a decoded alert record or any later record.
	if err := machine.FeedAlert(netsec.AlertLevelWarning, netsec.AlertCloseNotify); !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("repeated FeedAlert error = %v, want ErrClosed", err)
	}
}

func TestCloseNotifyAfterCompleteHandshakeClosesNormally(t *testing.T) {
	body := []byte("server hello body")
	message := encodeHandshake(2, body)

	machine := netsec.NewMachine()

	messages, err := machine.FeedRecord(netsec.RecordTypeHandshake, message[:6])
	if err != nil {
		t.Fatalf("FeedRecord(first fragment): %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("FeedRecord(first fragment) returned %d messages, want 0", len(messages))
	}

	messages, err = machine.FeedRecord(netsec.RecordTypeHandshake, message[6:])
	if err != nil {
		t.Fatalf("FeedRecord(second fragment): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("FeedRecord(second fragment) returned %d messages, want 1", len(messages))
	}
	if messages[0].Type != 2 {
		t.Fatalf("message type = %d, want 2", messages[0].Type)
	}
	if !bytes.Equal(messages[0].Body, body) {
		t.Fatalf("message body = %q, want %q", messages[0].Body, body)
	}
	if !bytes.Equal(messages[0].Raw, message) {
		t.Fatalf("message raw bytes were not preserved")
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after complete message = %d, want 0", got)
	}

	if _, err := machine.FeedRecord(netsec.RecordTypeAlert, []byte{byte(netsec.AlertLevelWarning), byte(netsec.AlertCloseNotify)}); err != nil {
		t.Fatalf("FeedRecord(close_notify): %v", err)
	}
	if machine.State() != netsec.StateClosed {
		t.Fatalf("State after close_notify = %v, want closed", machine.State())
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after close_notify = %d, want 0", got)
	}
}

func TestCloseNotifyDiscardsOnlyTrailingPartialHandshake(t *testing.T) {
	first := encodeHandshake(1, []byte("first"))
	second := encodeHandshake(2, []byte("second"))
	third := encodeHandshake(3, []byte("third handshake message"))

	split := len(third) - 5
	record := append(append(append([]byte(nil), first...), second...), third[:split]...)

	machine := netsec.NewMachine()

	messages, err := machine.FeedRecord(netsec.RecordTypeHandshake, record)
	if err != nil {
		t.Fatalf("FeedRecord: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("FeedRecord returned %d messages, want 2", len(messages))
	}
	if messages[0].Type != 1 || string(messages[0].Body) != "first" {
		t.Fatalf("first message = type %d body %q", messages[0].Type, messages[0].Body)
	}
	if messages[1].Type != 2 || string(messages[1].Body) != "second" {
		t.Fatalf("second message = type %d body %q", messages[1].Type, messages[1].Body)
	}
	if got, want := machine.PendingBytes(), split; got != want {
		t.Fatalf("PendingBytes = %d, want %d", got, want)
	}

	if err := machine.FeedAlert(netsec.AlertLevelWarning, netsec.AlertCloseNotify); err != nil {
		t.Fatalf("FeedAlert(close_notify): %v", err)
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after close_notify = %d, want 0", got)
	}

	messages, err = machine.FeedHandshake(third[split:])
	if !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("FeedHandshake(trailing remainder) error = %v, want ErrClosed", err)
	}
	if len(messages) != 0 {
		t.Fatalf("FeedHandshake(trailing remainder) returned %d messages, want 0", len(messages))
	}
}

func TestCloseNotifyWithPartialHeaderDiscardsPartial(t *testing.T) {
	message := encodeHandshake(1, []byte("header split"))
	firstHalf := message[:3]
	secondHalf := message[3:]

	machine := netsec.NewMachine()
	if _, err := machine.FeedHandshake(firstHalf); err != nil {
		t.Fatalf("FeedHandshake(partial header): %v", err)
	}
	if got := machine.PendingBytes(); got != len(firstHalf) {
		t.Fatalf("PendingBytes = %d, want %d", got, len(firstHalf))
	}

	if err := machine.FeedAlert(netsec.AlertLevelFatal, netsec.AlertCloseNotify); err != nil {
		t.Fatalf("FeedAlert(close_notify): %v", err)
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes after close_notify = %d, want 0", got)
	}
	if _, err := machine.FeedHandshake(secondHalf); !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("FeedHandshake(remaining bytes) error = %v, want ErrClosed", err)
	}
}

func TestNonCloseNotifyAlertClosesAndDiscardsPartial(t *testing.T) {
	message := encodeHandshake(1, []byte("unfinished"))

	machine := netsec.NewMachine()
	if _, err := machine.FeedHandshake(message[:7]); err != nil {
		t.Fatalf("FeedHandshake: %v", err)
	}

	err := machine.FeedAlert(netsec.AlertLevelFatal, 80) // internal_error
	var alertErr *netsec.AlertError
	if !errors.As(err, &alertErr) {
		t.Fatalf("FeedAlert error = %v, want *AlertError", err)
	}
	if alertErr.Level != netsec.AlertLevelFatal || alertErr.Description != 80 {
		t.Fatalf("alert = level %d description %d, want level %d description 80",
			alertErr.Level, alertErr.Description, netsec.AlertLevelFatal)
	}
	if machine.State() != netsec.StateClosed {
		t.Fatalf("State = %v, want closed", machine.State())
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes = %d, want 0", got)
	}

	if _, err := machine.FeedHandshake(message[7:]); !errors.Is(err, netsec.ErrClosed) {
		t.Fatalf("FeedHandshake after alert error = %v, want ErrClosed", err)
	}
}

func TestMalformedAlertClosesAndDiscardsPartial(t *testing.T) {
	message := encodeHandshake(1, []byte("unfinished"))

	machine := netsec.NewMachine()
	if _, err := machine.FeedHandshake(message[:8]); err != nil {
		t.Fatalf("FeedHandshake: %v", err)
	}

	if _, err := machine.FeedRecord(netsec.RecordTypeAlert, []byte{byte(netsec.AlertLevelFatal)}); err == nil {
		t.Fatal("FeedRecord(malformed alert) returned nil error")
	}
	if machine.State() != netsec.StateClosed {
		t.Fatalf("State = %v, want closed", machine.State())
	}
	if got := machine.PendingBytes(); got != 0 {
		t.Fatalf("PendingBytes = %d, want 0", got)
	}
}

func TestZeroValueMachine(t *testing.T) {
	message := encodeHandshake(1, []byte("zero value"))

	var machine netsec.Machine
	messages, err := machine.FeedHandshake(message)
	if err != nil {
		t.Fatalf("FeedHandshake: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("FeedHandshake returned %d messages, want 1", len(messages))
	}
	if machine.State() != netsec.StateOpen {
		t.Fatalf("State = %v, want open", machine.State())
	}

	if err := machine.FeedAlert(netsec.AlertLevelWarning, netsec.AlertCloseNotify); err != nil {
		t.Fatalf("FeedAlert: %v", err)
	}
	if machine.State() != netsec.StateClosed {
		t.Fatalf("State = %v, want closed", machine.State())
	}
}
