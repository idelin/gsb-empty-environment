package tls13sm

import (
	"testing"
	"time"
)

// TestEarlyDataFullInterop drives a PSK+DHE resumption handshake with
// accepted 0-RTT: the client emits an idempotent early request, the server
// admits it through all three defense lines, EndOfEarlyData is enforced, and
// the handshake completes with mutually consistent application keys.
func TestEarlyDataFullInterop(t *testing.T) {
	now := time.Now()
	ticketAge := 500 * time.Millisecond
	ticket := &ResumptionTicket{
		Nonce:        []byte("early-data-nonce-001"),
		PSK:          []byte("this-is-a-32-byte-resumption-psk!"),
		TicketAgeAdd: 0x0a0b0c0d,
		IssuedAt:     now.Add(-ticketAge),
		SuiteID:      SuiteAES128GCM.ID,
		MaxEarlyData: 16384,
	}

	client, err := NewClientStateMachine(ClientConfig{
		Suite:           SuiteAES128GCM,
		Groups:          []uint16{GroupP256},
		PSK:             ticket.PSK,
		PSKIsResumption: true,
		TicketAgeAdd:    ticket.TicketAgeAdd,
		TicketAge:       ticketAge,
		UseEarlyData:    true,
		Idempotent:      true,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerStateMachine(ServerConfig{
		Suite:          SuiteAES128GCM,
		Groups:         []uint16{GroupP256},
		Tickets:        map[string]*ResumptionTicket{"resumption-ticket": ticket},
		Strike:         NewStrikeRegister(time.Hour),
		AllowEarlyData: true,
		TicketAgeSkew:  time.Minute,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	start, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}

	// Client sends an idempotent 0-RTT request before the server responds.
	early, err := client.SendEarlyData([]byte("GET/idempotent-resource"))
	if err != nil {
		t.Fatal(err)
	}

	// Server processes ClientHello and admits 0-RTT (all three defense lines).
	chEvent, err := server.HandleRecord(start.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if !server.EarlyDataAccepted() {
		t.Fatal("server did not accept admissible early data")
	}

	// Early-data record decrypts under client_early_traffic keys.
	early.Records[0].Epoch = EpochEarlyData
	earlyEvent, err := server.HandleRecord(early.Records[0])
	if err != nil {
		t.Fatalf("early data decryption failed: %v", err)
	}
	if string(earlyEvent.EarlyData) != "GET/idempotent-resource" {
		t.Fatalf("early data=%q", earlyEvent.EarlyData)
	}

	// Server flight: plaintext ServerHello + encrypted EE (early_data ack) +
	// encrypted server Finished.
	if len(chEvent.Records) != 3 {
		t.Fatalf("server flight records=%d, want 3", len(chEvent.Records))
	}
	if _, err := client.HandleRecord(chEvent.Records[0]); err != nil {
		t.Fatal(err)
	}
	var clientFlight []Record
	for _, record := range chEvent.Records[1:] {
		record.Epoch = EpochHandshake
		event, err := client.HandleRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		clientFlight = append(clientFlight, event.Records...)
	}
	if !client.EarlyDataAccepted() {
		t.Fatal("client did not observe early_data acceptance in EncryptedExtensions")
	}

	// Client flight must be EndOfEarlyData (early epoch) then Finished
	// (handshake epoch), in that order.
	if len(clientFlight) != 2 {
		t.Fatalf("client flight records=%d, want EOED + Finished", len(clientFlight))
	}

	clientFlight[0].Epoch = EpochEarlyData
	eoedEvent, err := server.HandleRecord(clientFlight[0])
	if err != nil {
		t.Fatalf("EndOfEarlyData rejected: %v", err)
	}
	if !server.eoedReceived || server.Phase() != ServerPhaseWaitFinished {
		t.Fatalf("after EOED phase=%s eoed=%v", server.Phase(), server.eoedReceived)
	}
	if len(eoedEvent.Records) != 0 {
		t.Fatal("EOED must not produce server records")
	}

	clientFlight[1].Epoch = EpochHandshake
	if _, err := server.HandleRecord(clientFlight[1]); err != nil {
		t.Fatalf("client Finished rejected: %v", err)
	}
	if server.Phase() != ServerPhaseConnected || client.Phase() != ClientPhaseConnected {
		t.Fatalf("phases server=%s client=%s", server.Phase(), client.Phase())
	}

	// Normal application data after the handshake uses fresh app keys.
	app, err := client.SendApplicationData([]byte("normal-request"))
	if err != nil {
		t.Fatal(err)
	}
	app.Records[0].Epoch = EpochAppData
	got, err := server.HandleRecord(app.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got.AppData) != "normal-request" {
		t.Fatalf("post-handshake app data=%q", got.AppData)
	}
}

// TestEarlyDataRejectedWhenSkewTooLarge proves an obfuscated ticket age
// outside the window causes the server to refuse 0-RTT (it still completes a
// normal handshake).
func TestEarlyDataRejectedWhenSkewTooLarge(t *testing.T) {
	now := time.Now()
	ticket := &ResumptionTicket{
		Nonce:        []byte("skew-nonce"),
		PSK:          []byte("this-is-a-32-byte-resumption-psk!"),
		TicketAgeAdd: 0x01020304,
		IssuedAt:     now.Add(-2 * time.Hour),
		SuiteID:      SuiteAES128GCM.ID,
		MaxEarlyData: 16384,
	}
	client, err := NewClientStateMachine(ClientConfig{
		Suite:           SuiteAES128GCM,
		Groups:          []uint16{GroupP256},
		PSK:             ticket.PSK,
		PSKIsResumption: true,
		TicketAgeAdd:    ticket.TicketAgeAdd,
		TicketAge:       time.Hour,
		UseEarlyData:    true,
		Idempotent:      true,
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerStateMachine(ServerConfig{
		Suite:          SuiteAES128GCM,
		Groups:         []uint16{GroupP256},
		Tickets:        map[string]*ResumptionTicket{"resumption-ticket": ticket},
		Strike:         NewStrikeRegister(time.Hour),
		AllowEarlyData: true,
		TicketAgeSkew:  5 * time.Second,
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	start, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	event, err := server.HandleRecord(start.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if server.EarlyDataAccepted() {
		t.Fatal("early data must be rejected when age skew exceeds the window")
	}
	if !event.EarlyRejected {
		t.Fatal("server event did not flag early-data rejection")
	}
}
