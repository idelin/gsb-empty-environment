package tls13sm

import (
	"errors"
	"testing"
)

// TestPlaintextAlertInjection proves an attacker cannot inject a plaintext
// Alert before traffic keys exist; such records are categorically refused.
func TestPlaintextAlertInjection(t *testing.T) {
	client, err := NewClientStateMachine(ClientConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(); err != nil {
		t.Fatal(err)
	}
	_, err = client.HandleRecord(Record{
		Epoch:       EpochPlaintext,
		ContentType: ContentTypeAlert,
		Version:     LegacyVersionTLS12,
		Payload:     []byte{2, 40},
	})
	if !errors.Is(err, ErrIllegalMessage) {
		t.Fatalf("plaintext alert err=%v, want ErrIllegalMessage", err)
	}
}

// TestPlaintextKeyUpdateInjection proves a KeyUpdate presented as a
// plaintext handshake record (rather than an encrypted inner message) is
// rejected, blocking encryption-phase confusion.
func TestPlaintextKeyUpdateInjection(t *testing.T) {
	client, err := NewClientStateMachine(ClientConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(); err != nil {
		t.Fatal(err)
	}
	ku := marshalHandshake(HandshakeKeyUpdate, []byte{KeyUpdateRequested})
	_, err = client.HandleRecord(Record{
		Epoch:       EpochPlaintext,
		ContentType: ContentTypeHandshake,
		Version:     LegacyVersionTLS12,
		Payload:     ku,
	})
	if !errors.Is(err, ErrIllegalMessage) {
		t.Fatalf("plaintext KeyUpdate err=%v, want ErrIllegalMessage", err)
	}
}

// TestAppDataBeforeFinished proves application data cannot be processed
// before the handshake reaches the connected phase.
func TestAppDataBeforeFinished(t *testing.T) {
	client, err := NewClientStateMachine(ClientConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(); err != nil {
		t.Fatal(err)
	}
	// Sending application data through the public API before connected fails.
	_, err = client.SendApplicationData([]byte("premature"))
	if !errors.Is(err, ErrState) {
		t.Fatalf("premature SendApplicationData err=%v, want ErrState", err)
	}

	// An inbound protected app-data record while still waiting for ServerHello
	// is rejected by the epoch guard (no read traffic state installed).
	_, err = client.HandleRecord(Record{
		Epoch:       EpochAppData,
		ContentType: ContentTypeApplicationData,
		Version:     LegacyVersionTLS12,
		Payload:     []byte{0x01, 0x02, 0x03},
	})
	if err == nil {
		t.Fatal("inbound app data before Finished was accepted")
	}
}

// TestCrossEpochCiphertextRejected proves a record tagged with an epoch that
// does not match the active read epoch is refused even before AEAD.
func TestCrossEpochCiphertextRejected(t *testing.T) {
	server, err := NewServerStateMachine(ServerConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	// No keys installed; an app-epoch record must be rejected.
	_, err = server.HandleRecord(Record{
		Epoch:       EpochAppData,
		ContentType: ContentTypeApplicationData,
		Version:     LegacyVersionTLS12,
		Payload:     make([]byte, 20),
	})
	if !errors.Is(err, ErrIllegalMessage) {
		t.Fatalf("cross-epoch record err=%v, want ErrIllegalMessage", err)
	}
}

// TestKeyUpdateBudgetEnforced proves the per-direction rotation limit blocks
// out-of-bound KeyUpdate churn.
func TestKeyUpdateBudgetEnforced(t *testing.T) {
	mgr := newTrafficManager(SuiteAES128GCM, newKeySchedule(SuiteAES128GCM), 2)
	schedule := newKeySchedule(SuiteAES128GCM)
	schedule.initEarlySecret(nil)
	state, err := schedule.newTrafficState(schedule.earlySecret)
	if err != nil {
		t.Fatal(err)
	}
	mgr.installRead(EpochAppData, state)
	mgr.installWrite(EpochAppData, state)

	if err := mgr.rotateWrite(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.rotateWrite(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.rotateWrite(); !errors.Is(err, ErrKeyUpdateLimit) {
		t.Fatalf("third rotation err=%v, want ErrKeyUpdateLimit", err)
	}
}

// TestMalformedKeyUpdateRejected proves an out-of-range request_update value
// cannot smuggle state changes.
func TestMalformedKeyUpdateRejected(t *testing.T) {
	if _, err := parseKeyUpdate([]byte{0x02}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("KeyUpdate value 2 err=%v, want ErrProtocol", err)
	}
	if _, err := parseKeyUpdate(nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("empty KeyUpdate err=%v, want ErrProtocol", err)
	}
}
