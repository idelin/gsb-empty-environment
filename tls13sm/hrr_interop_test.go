package tls13sm

import (
	"bytes"
	"testing"
)

// TestHRRFullInterop drives a handshake where the client only offers a group
// the server does not accept first, forcing a HelloRetryRequest, then proves
// the retry ClientHello with the folded transcript completes successfully.
func TestHRRFullInterop(t *testing.T) {
	// Client offers X25519 first and P-384; server is configured to require
	// P-384 only, so an HRR is mandatory.
	client, err := NewClientStateMachine(ClientConfig{
		Suite:  SuiteAES128GCM,
		Groups: []uint16{GroupX25519, GroupP384},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerStateMachine(ServerConfig{
		Suite:  SuiteAES128GCM,
		Groups: []uint16{GroupP384},
	})
	if err != nil {
		t.Fatal(err)
	}

	start, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}

	// First ClientHello triggers an HRR.
	hrrEvent, err := server.HandleRecord(start.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if !server.hrrSent || server.Phase() != ServerPhaseWaitCH2 {
		t.Fatalf("server did not enter HRR wait: phase=%s hrr=%v", server.Phase(), server.hrrSent)
	}
	if len(hrrEvent.Records) != 1 {
		t.Fatalf("HRR flight should contain one record, got %d", len(hrrEvent.Records))
	}

	// Both transcripts must now contain the synthetic message_hash entry.
	retryEvent, err := client.HandleRecord(hrrEvent.Records[0])
	if err != nil {
		t.Fatalf("client rejected valid HRR: %v", err)
	}
	if !client.hrrSeen || !client.transcript.HRRFolded() {
		t.Fatal("client did not fold the HRR transcript")
	}
	if !server.transcript.HRRFolded() {
		t.Fatal("server did not fold the HRR transcript")
	}
	if len(retryEvent.Records) != 1 {
		t.Fatalf("retry ClientHello record count=%d", len(retryEvent.Records))
	}

	// Confirm the synthetic message_hash is the first transcript entry on
	// both machines and that they agree on the running hash.
	firstClient, _, err := parseHandshakeHeader(client.transcript.Messages()[0])
	if err != nil {
		t.Fatal(err)
	}
	if firstClient.Type != HandshakeMessageHash {
		t.Fatalf("client first transcript entry type=%d", firstClient.Type)
	}

	// Second ClientHello completes the key exchange; server emits the full
	// encrypted flight.
	secondEvent, err := server.HandleRecord(retryEvent.Records[0])
	if err != nil {
		t.Fatalf("retry ClientHello rejected: %v", err)
	}
	if len(secondEvent.Records) != 3 {
		t.Fatalf("post-HRR server flight records=%d, want 3", len(secondEvent.Records))
	}

	if _, err := client.HandleRecord(secondEvent.Records[0]); err != nil {
		t.Fatal(err)
	}
	var clientFlight []Record
	for _, record := range secondEvent.Records[1:] {
		record.Epoch = EpochHandshake
		event, err := client.HandleRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		clientFlight = append(clientFlight, event.Records...)
	}
	if len(clientFlight) != 1 {
		t.Fatalf("client Finished records=%d", len(clientFlight))
	}

	clientFlight[0].Epoch = EpochHandshake
	if _, err := server.HandleRecord(clientFlight[0]); err != nil {
		t.Fatal(err)
	}
	if client.Phase() != ClientPhaseConnected || server.Phase() != ServerPhaseConnected {
		t.Fatalf("phases client=%s server=%s", client.Phase(), server.Phase())
	}
	if !bytes.Equal(client.TranscriptHash(), server.TranscriptHash()) {
		t.Fatal("HRR transcript hashes diverged")
	}

	// Application data after the HRR handshake must flow correctly.
	app, err := client.SendApplicationData([]byte("post-hrr"))
	if err != nil {
		t.Fatal(err)
	}
	app.Records[0].Epoch = EpochAppData
	got, err := server.HandleRecord(app.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got.AppData) != "post-hrr" {
		t.Fatalf("post-HRR app data=%q", got.AppData)
	}
}
