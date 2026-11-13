package netsec

import (
	"errors"
	"testing"
)

func recvAll(t *testing.T, c *ClientHandshake, msgs ...MessageType) {
	t.Helper()
	for _, m := range msgs {
		if err := c.Receive(m); err != nil {
			t.Fatalf("Receive(%s) failed: %v", m, err)
		}
	}
}

func requireUnexpectedMessage(t *testing.T, err error) {
	t.Helper()
	var alert *AlertError
	if !errors.As(err, &alert) {
		t.Fatalf("expected *AlertError, got %v", err)
	}
	if alert.Alert != AlertUnexpectedMessage {
		t.Fatalf("expected alert unexpected_message (%d), got %d", AlertUnexpectedMessage, alert.Alert)
	}
}

func TestEarlyChangeCipherSpec(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello, MsgCertificate, MsgServerKeyExchange, MsgServerHelloDone)

	// Server sends CCS before the client sent ClientKeyExchange.
	err := c.Receive(MsgChangeCipherSpec)
	requireUnexpectedMessage(t, err)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after early ChangeCipherSpec")
	}
	if c.Established() {
		t.Fatal("handshake must not be established after abort")
	}

	// The state machine stays aborted for subsequent messages.
	requireUnexpectedMessage(t, c.Receive(MsgFinished))
}

func TestEarlyChangeCipherSpecBeforeServerHelloDone(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello, MsgCertificate)

	err := c.Receive(MsgChangeCipherSpec)
	requireUnexpectedMessage(t, err)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after early ChangeCipherSpec")
	}
}

func TestMissingCertificate(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello)

	// ServerKeyExchange without a preceding Certificate.
	err := c.Receive(MsgServerKeyExchange)
	requireUnexpectedMessage(t, err)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after missing Certificate")
	}
}

func TestMissingCertificateHelloDone(t *testing.T) {
	c := NewClientHandshake(false)
	recvAll(t, c, MsgServerHello)

	// ServerHelloDone directly after ServerHello skips Certificate.
	err := c.Receive(MsgServerHelloDone)
	requireUnexpectedMessage(t, err)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after missing Certificate")
	}
}

func TestDuplicateServerHello(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello)

	err := c.Receive(MsgServerHello)
	requireUnexpectedMessage(t, err)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after duplicate ServerHello")
	}
}

func TestMissingServerKeyExchangeECDHE(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello, MsgCertificate)

	// ECDHE requires ServerKeyExchange; ServerHelloDone is out of order.
	requireUnexpectedMessage(t, c.Receive(MsgServerHelloDone))
}

func TestDuplicateMessage(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c, MsgServerHello, MsgCertificate)

	requireUnexpectedMessage(t, c.Receive(MsgCertificate))
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled after duplicate Certificate")
	}
}

func TestValidHandshakeECDHEWithCertRequest(t *testing.T) {
	c := NewClientHandshake(true)
	recvAll(t, c,
		MsgServerHello,
		MsgCertificate,
		MsgServerKeyExchange,
		MsgCertificateRequest,
		MsgServerHelloDone,
	)
	if c.EncryptionEnabled() {
		t.Fatal("encryption must not be enabled before ChangeCipherSpec")
	}
	if err := c.SendClientKeyExchange(); err != nil {
		t.Fatalf("SendClientKeyExchange failed: %v", err)
	}
	recvAll(t, c, MsgChangeCipherSpec)
	if !c.EncryptionEnabled() {
		t.Fatal("encryption must be enabled after valid ChangeCipherSpec")
	}
	recvAll(t, c, MsgFinished)
	if !c.Established() {
		t.Fatal("handshake must be established after Finished")
	}
}

func TestValidHandshakeNonECDHE(t *testing.T) {
	c := NewClientHandshake(false)
	recvAll(t, c, MsgServerHello, MsgCertificate, MsgServerHelloDone)
	if err := c.SendClientKeyExchange(); err != nil {
		t.Fatalf("SendClientKeyExchange failed: %v", err)
	}
	recvAll(t, c, MsgChangeCipherSpec, MsgFinished)
	if !c.Established() || !c.EncryptionEnabled() {
		t.Fatal("handshake must be established with encryption enabled")
	}
}

func TestMessageAfterEstablished(t *testing.T) {
	c := NewClientHandshake(false)
	recvAll(t, c, MsgServerHello, MsgCertificate, MsgServerHelloDone)
	if err := c.SendClientKeyExchange(); err != nil {
		t.Fatalf("SendClientKeyExchange failed: %v", err)
	}
	recvAll(t, c, MsgChangeCipherSpec, MsgFinished)

	requireUnexpectedMessage(t, c.Receive(MsgServerHello))
}
