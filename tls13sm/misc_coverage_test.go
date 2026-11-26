package tls13sm

import (
	"errors"
	"testing"
)

func TestCipherSuiteLookup(t *testing.T) {
	if _, err := cipherSuiteByID(SuiteAES256GCM.ID); err != nil {
		t.Fatalf("AES-256-GCM suite lookup failed: %v", err)
	}
	if got := len(SupportedCipherSuites()); got != 2 {
		t.Fatalf("supported suites=%d, want 2", got)
	}
	if _, err := cipherSuiteByID(0xffff); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unknown suite err=%v, want ErrProtocol", err)
	}
}

func TestResumptionTicketDerivation(t *testing.T) {
	schedule := newKeySchedule(SuiteAES128GCM)
	schedule.initEarlySecret(nil)
	// Drive to an application/master secret so the resumption master exists.
	schedule.deriveHandshake(
		make([]byte, SuiteAES128GCM.HashLen),
		make([]byte, SuiteAES128GCM.HashLen),
	)
	schedule.deriveApplication(make([]byte, SuiteAES128GCM.HashLen))

	nonce := []byte{0x01, 0x02}
	psk := schedule.resumptionTicket(nonce)
	if len(psk) != SuiteAES128GCM.HashLen {
		t.Fatalf("resumption PSK length=%d", len(psk))
	}
	// Deterministic: same nonce yields same PSK.
	if !equalConstantTime(psk, schedule.resumptionTicket(nonce)) {
		t.Fatal("resumption PSK derivation is not deterministic")
	}
}

func TestStrikeRegisterReset(t *testing.T) {
	reg := NewStrikeRegister(0)
	nonce := []byte("reset-me")
	if err := reg.checkAndStore(nonce); err != nil {
		t.Fatal(err)
	}
	reg.Reset()
	if reg.Contains(nonce) {
		t.Fatal("nonce still present after Reset")
	}
	if err := reg.checkAndStore(nonce); err != nil {
		t.Fatalf("reuse after Reset should pass: %v", err)
	}
}

// TestServerKeyUpdateAndAlert exercises the server-side post-handshake
// rotation and protected alert emission, plus the connected-phase guard.
func TestServerKeyUpdateAndAlert(t *testing.T) {
	client, server := connectedPair(t)

	ku, err := server.SendKeyUpdate(false)
	if err != nil {
		t.Fatal(err)
	}
	ku.Records[0].Epoch = EpochAppData
	if _, err := client.HandleRecord(ku.Records[0]); err != nil {
		t.Fatalf("client rejected server KeyUpdate: %v", err)
	}

	alert, err := server.SendAlert(0x28)
	if err != nil {
		t.Fatal(err)
	}
	alert.Records[0].Epoch = EpochAppData
	event, err := client.HandleRecord(alert.Records[0])
	if err != nil {
		t.Fatalf("client rejected alert: %v", err)
	}
	if event.Alert != 0x28 {
		t.Fatalf("alert description=%d, want 40", event.Alert)
	}
}

func TestClientSendAlert(t *testing.T) {
	client, server := connectedPair(t)
	alert, err := client.SendAlert(0x28)
	if err != nil {
		t.Fatal(err)
	}
	alert.Records[0].Epoch = EpochAppData
	event, err := server.HandleRecord(alert.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if event.Alert != 0x28 {
		t.Fatalf("server alert=%d", event.Alert)
	}
}

// connectedPair runs a full handshake and returns both connected machines.
func connectedPair(t *testing.T) (*ClientStateMachine, *ServerStateMachine) {
	t.Helper()
	client, err := NewClientStateMachine(ClientConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerStateMachine(ServerConfig{Suite: SuiteAES128GCM, Groups: []uint16{GroupP256}})
	if err != nil {
		t.Fatal(err)
	}
	start, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	ev, err := server.HandleRecord(start.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HandleRecord(ev.Records[0]); err != nil {
		t.Fatal(err)
	}
	var flight []Record
	for _, record := range ev.Records[1:] {
		record.Epoch = EpochHandshake
		cev, err := client.HandleRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		flight = append(flight, cev.Records...)
	}
	flight[0].Epoch = EpochHandshake
	if _, err := server.HandleRecord(flight[0]); err != nil {
		t.Fatal(err)
	}
	if client.Phase() != ClientPhaseConnected || server.Phase() != ServerPhaseConnected {
		t.Fatalf("pair not connected: %s / %s", client.Phase(), server.Phase())
	}
	return client, server
}
