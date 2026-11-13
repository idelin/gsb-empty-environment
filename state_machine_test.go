package netsec

import (
	"errors"
	"testing"
)

func TestMissingEndOfEarlyDataFails(t *testing.T) {
	m := NewStateMachine(true)
	if err := m.Feed(Finished); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("Feed(Finished) error = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
	if !m.Failed() {
		t.Fatal("machine did not enter failed state")
	}
	if err := m.Feed(EndOfEarlyData); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("feed after failure error = %v, want original failure", err)
	}
}

func TestCompleteEarlyDataSessionSucceeds(t *testing.T) {
	m := NewStateMachine()
	sequence := []any{
		Message{Type: MessageClientHello, EarlyData: true},
		ServerHello,
		Message{Type: MessageEncryptedExtensions, EarlyDataAccepted: true},
		EarlyData,
		EndOfEarlyData,
		Finished,
	}
	if err := m.FeedAll(sequence...); err != nil {
		t.Fatalf("FeedAll returned %v", err)
	}
	if !m.EarlyDataAccepted() {
		t.Fatal("early data was not accepted")
	}
	if !m.EndOfEarlyDataReceived() {
		t.Fatal("EndOfEarlyData was not recorded")
	}
	if !m.Complete() {
		t.Fatal("handshake did not complete")
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
}

func TestNoEarlyDataDoesNotRequireEndOfEarlyData(t *testing.T) {
	m := NewStateMachine()
	if err := m.FeedAll(ClientHello, ServerHello, Finished); err != nil {
		t.Fatalf("normal handshake failed: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
}

func TestUnexpectedEndOfEarlyData(t *testing.T) {
	m := NewStateMachine()
	if err := m.Feed(EndOfEarlyData); !errors.Is(err, ErrUnexpectedEndOfEarlyData) {
		t.Fatalf("error = %v, want ErrUnexpectedEndOfEarlyData", err)
	}
}

func TestDuplicateEndOfEarlyData(t *testing.T) {
	m := NewStateMachine(true)
	if err := m.Feed(EndOfEarlyData); err != nil {
		t.Fatalf("first EndOfEarlyData: %v", err)
	}
	if err := m.Feed(EndOfEarlyData); !errors.Is(err, ErrDuplicateEndOfEarlyData) {
		t.Fatalf("second EndOfEarlyData: %v", err)
	}
}

func TestStringEvents(t *testing.T) {
	m := NewStateMachine()
	events := []any{"ClientHello", "early data accepted", "early data", "end_of_early_data", "Finished"}
	if err := m.FeedAll(events...); err != nil {
		t.Fatalf("string sequence failed: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
}

func TestValidateSequenceRejectsRemovedEndOfEarlyData(t *testing.T) {
	events := []any{
		Message{Type: MessageClientHello, EarlyData: true},
		ServerHello,
		Message{Type: MessageEncryptedExtensions, EarlyDataAccepted: true},
		EarlyData,
		EndOfEarlyData,
		Finished,
	}
	without := DeleteMessage(EndOfEarlyData, events)
	if err := ValidateSequence(without...); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("ValidateSequence without EndOfEarlyData = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
	if err := ValidateSequence(events...); err != nil {
		t.Fatalf("complete sequence = %v", err)
	}
}

func TestQUICModeAllowsRemovedEndOfEarlyData(t *testing.T) {
	m := NewStateMachine(true, WithoutEndOfEarlyData())
	if err := m.Feed(Finished); err != nil {
		t.Fatalf("QUIC mode rejected Finished: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("QUIC mode Validate: %v", err)
	}
}

func TestHandshakeCompleteCannotBypassEndOfEarlyData(t *testing.T) {
	m := NewStateMachine()
	if err := m.FeedAll(
		Message{Type: MessageClientHello, EarlyData: true},
		Message{Type: MessageEncryptedExtensions, EarlyDataAccepted: true},
		EarlyData,
		MessageHandshakeComplete,
	); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("HandshakeComplete = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
}

func TestAcceptedFlagOnFinishedDoesNotBypassBoundary(t *testing.T) {
	m := NewStateMachine()
	msg := Message{Type: MessageFinished, EarlyDataAccepted: true}
	if err := m.Feed(msg); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("Finished with accepted flag = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
}

func TestMessageTypeSliceInput(t *testing.T) {
	m := NewStateMachine(true)
	events := []MessageType{EarlyData, EndOfEarlyData, Finished}
	if err := m.Feed(events); err != nil {
		t.Fatalf("slice input failed: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
}

func TestTransportQUICSkipsBoundary(t *testing.T) {
	m := NewStateMachine(true, TransportQUIC)
	if err := m.Feed(Finished); err != nil {
		t.Fatalf("QUIC transport rejected Finished: %v", err)
	}
}

func TestHandshakeMessageBeforeEndOfEarlyDataFails(t *testing.T) {
	m := NewStateMachine(true)
	if err := m.Feed(Certificate); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("Certificate before EndOfEarlyData = %v", err)
	}
}

func TestEarlyDataWithoutOfferOrAcceptanceFails(t *testing.T) {
	m := NewStateMachine()
	if err := m.Feed(EarlyData); !errors.Is(err, ErrUnexpectedEarlyData) {
		t.Fatalf("EarlyData without offer = %v, want %v", err, ErrUnexpectedEarlyData)
	}
}

type testStackEvent struct {
	MessageType string
	EarlyData   bool
}

func TestReflectedStackEvent(t *testing.T) {
	m := NewStateMachine()
	if err := m.Feed(testStackEvent{MessageType: "ClientHello", EarlyData: true}); err != nil {
		t.Fatalf("ClientHello: %v", err)
	}
	if err := m.Feed(testStackEvent{MessageType: "EncryptedExtensions"}); err != nil {
		t.Fatalf("EncryptedExtensions: %v", err)
	}
	if err := m.FeedAll(EarlyData, Finished); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("EarlyData then no boundary = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
}

func TestMapAndStringSliceEvents(t *testing.T) {
	m := NewStateMachine(true)
	if err := m.Feed([]string{"EndOfEarlyData", "Finished"}); err != nil {
		t.Fatalf("string slice: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	n := NewStateMachine()
	if err := n.Feed([]map[string]any{
		{"type": "ClientHello", "early_data": true},
		{"type": "EarlyDataAccepted"},
		{"type": "Finished"},
	}); !errors.Is(err, ErrMissingEndOfEarlyData) {
		t.Fatalf("map sequence = %v, want %v", err, ErrMissingEndOfEarlyData)
	}
}
