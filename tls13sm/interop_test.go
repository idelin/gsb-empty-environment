package tls13sm

import (
	"bytes"
	"errors"
	"testing"
)

func expectErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", err, target)
	}
}

// TestFullHandshakeInterop drives a complete PSK-less handshake through both
// state machines, then exchanges protected application data and a KeyUpdate.
// Successful AEAD decryption on both sides and Finished verification prove
// that the entire RFC 8446 7.1 derivation chain, nonce construction and
// transcript handling are internally consistent.
func TestFullHandshakeInterop(t *testing.T) {
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
	serverEvent, err := server.HandleRecord(start.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	// Plaintext ServerHello + encrypted EE + encrypted server Finished.
	if len(serverEvent.Records) != 3 {
		t.Fatalf("expected 3 server flight records, got %d", len(serverEvent.Records))
	}

	if _, err := client.HandleRecord(serverEvent.Records[0]); err != nil {
		t.Fatal(err)
	}

	var clientFlight []Record
	for _, record := range serverEvent.Records[1:] {
		record.Epoch = EpochHandshake
		event, err := client.HandleRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		clientFlight = append(clientFlight, event.Records...)
	}
	// Client emits one Finished record (no EOED because no early data).
	if len(clientFlight) != 1 {
		t.Fatalf("expected 1 client Finished record, got %d", len(clientFlight))
	}

	clientFlight[0].Epoch = EpochHandshake
	finEvent, err := server.HandleRecord(clientFlight[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(finEvent.Records) != 0 {
		t.Fatalf("server should not emit records after client Finished")
	}
	if server.Phase() != ServerPhaseConnected || client.Phase() != ClientPhaseConnected {
		t.Fatalf("phases server=%s client=%s", server.Phase(), client.Phase())
	}

	// Application data flows in both directions under app traffic keys.
	appRecord, err := client.SendApplicationData([]byte("hello-server"))
	if err != nil {
		t.Fatal(err)
	}
	appRecord.Records[0].Epoch = EpochAppData
	got, err := server.HandleRecord(appRecord.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got.AppData) != "hello-server" {
		t.Fatalf("server app data = %q", got.AppData)
	}

	reply, err := server.SendApplicationData([]byte("hello-client"))
	if err != nil {
		t.Fatal(err)
	}
	reply.Records[0].Epoch = EpochAppData
	got2, err := client.HandleRecord(reply.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got2.AppData) != "hello-client" {
		t.Fatalf("client app data = %q", got2.AppData)
	}

	// KeyUpdate rotates keys and stays mutually intelligible.
	ku, err := client.SendKeyUpdate(true)
	if err != nil {
		t.Fatal(err)
	}
	ku.Records[0].Epoch = EpochAppData
	kuEvent, err := server.HandleRecord(ku.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(kuEvent.Records) != 1 {
		t.Fatalf("expected server KeyUpdate reply, got %d", len(kuEvent.Records))
	}
	kuEvent.Records[0].Epoch = EpochAppData
	if _, err := client.HandleRecord(kuEvent.Records[0]); err != nil {
		t.Fatal(err)
	}

	app2, err := client.SendApplicationData([]byte("after-rotation"))
	if err != nil {
		t.Fatal(err)
	}
	app2.Records[0].Epoch = EpochAppData
	got3, err := server.HandleRecord(app2.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got3.AppData) != "after-rotation" {
		t.Fatalf("post-rotation app data = %q", got3.AppData)
	}

	// The two sides must derive identical application traffic secrets.
	if !bytes.Equal(client.schedule.clientApp, server.schedule.clientApp) {
		t.Fatal("client application traffic secret mismatch")
	}
	if !bytes.Equal(client.schedule.serverApp, server.schedule.serverApp) {
		t.Fatal("server application traffic secret mismatch")
	}
	// And identical handshake transcripts.
	if !bytes.Equal(client.TranscriptHash(), server.TranscriptHash()) {
		t.Fatal("final transcript hash mismatch")
	}
}

func TestRecordRoundTrip(t *testing.T) {
	suite := SuiteAES128GCM
	schedule := newKeySchedule(suite)
	schedule.initEarlySecret(nil)
	sealState, err := schedule.newTrafficState(schedule.earlySecret)
	if err != nil {
		t.Fatal(err)
	}
	openState, err := schedule.newTrafficState(schedule.earlySecret)
	if err != nil {
		t.Fatal(err)
	}
	record := sealState.cipher.sealRecord(ContentTypeApplicationData, []byte("ping"))
	if record.ContentType != ContentTypeApplicationData {
		t.Fatal("outer content type must be application_data")
	}
	innerType, body, err := openState.cipher.openRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if innerType != ContentTypeApplicationData || !bytes.Equal(body, []byte("ping")) {
		t.Fatalf("round trip mismatch type=%d body=%q", innerType, body)
	}
}
