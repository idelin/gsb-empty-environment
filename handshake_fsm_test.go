package netsec_test

import (
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0834"
)

var ccsPayload = []byte{1}

// hs builds a minimal handshake record fragment: 1-byte type + 3-byte length
// plus a dummy body, matching the layout consumed by HandleRecord.
func hs(t uint8, bodyLen int) []byte {
	rec := make([]byte, 4+bodyLen)
	rec[0] = t
	rec[1] = byte(bodyLen >> 16)
	rec[2] = byte(bodyLen >> 8)
	rec[3] = byte(bodyLen)
	return rec
}

func expectUnexpectedMessage(t *testing.T, m *netsec.TLS12ClientHandshake, err error, wantState netsec.HandshakeState) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error in state %s, got nil", wantState)
	}
	var he *netsec.HandshakeError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HandshakeError, got %T: %v", err, err)
	}
	if he.Alert != netsec.AlertUnexpectedMessage {
		t.Fatalf("expected alert unexpected_message(%d), got %d",
			netsec.AlertUnexpectedMessage, he.Alert)
	}
	if he.State != wantState {
		t.Fatalf("error should capture state %s, got %s", wantState, he.State)
	}
	if m.State() != netsec.StateFailed {
		t.Fatalf("machine must be failed after violation, got %s", m.State())
	}
	if m.ReadEncrypted() {
		t.Fatalf("inbound encryption must stay disabled after abort")
	}
	if m.Complete() {
		t.Fatalf("handshake must not report complete after abort")
	}
}

// The mandated attack: CCS is injected before the key exchange is complete.
// Every insertion point must abort with unexpected_message and must never
// turn on inbound encryption.
func TestEarlyChangeCipherSpec(t *testing.T) {
	t.Run("before any message", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		err := m.HandleChangeCipherSpec(ccsPayload)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitServerHello)
	})

	t.Run("right after server_hello", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		if err := m.HandleHandshake(netsec.HandshakeServerHello, nil); err != nil {
			t.Fatal(err)
		}
		err := m.HandleChangeCipherSpec(ccsPayload)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitCertificate)
	})

	t.Run("inside the certificate/serverkeyexchange flight", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
		mustOK(t, m.HandleHandshake(netsec.HandshakeCertificate, nil))
		err := m.HandleChangeCipherSpec(ccsPayload)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitServerKeyExchange)
	})

	t.Run("after server_hello_done but before local client_key_exchange", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		driveTo(t, m, false)
		// Deliberately do NOT call NotifyClientKeyExchangeSent.
		err := m.HandleChangeCipherSpec(ccsPayload)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitChangeCipherSpec)
	})

	t.Run("duplicate ccs after a valid one", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		driveTo(t, m, false)
		mustOK(t, m.NotifyClientKeyExchangeSent())
		mustOK(t, m.HandleChangeCipherSpec(ccsPayload))
		if !m.ReadEncrypted() {
			t.Fatal("read encryption should be on after accepted CCS")
		}
		err := m.HandleChangeCipherSpec(ccsPayload)
		if err == nil {
			t.Fatal("duplicate CCS must be rejected")
		}
		var he *netsec.HandshakeError
		if !errors.As(err, &he) || he.Alert != netsec.AlertUnexpectedMessage {
			t.Fatalf("duplicate CCS must be unexpected_message, got %v", err)
		}
		if m.State() != netsec.StateFailed {
			t.Fatalf("expected failed state, got %s", m.State())
		}
	})

	t.Run("malformed payload", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		err := m.HandleChangeCipherSpec([]byte{2})
		if err == nil {
			t.Fatal("malformed CCS must be rejected")
		}
		var he *netsec.HandshakeError
		if !errors.As(err, &he) || he.Alert != netsec.AlertIllegalParameter {
			t.Fatalf("malformed CCS must be illegal_parameter, got %v", err)
		}
		if m.ReadEncrypted() {
			t.Fatal("encryption must stay disabled")
		}
	})
}

// The mandated attack: the server skips Certificate entirely. The machine
// must refuse the next message rather than continue with an unauthenticated
// peer.
func TestMissingCertificate(t *testing.T) {
	t.Run("server_key_exchange without certificate", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
		err := m.HandleHandshake(netsec.HandshakeServerKeyExchange, nil)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitCertificate)
	})

	t.Run("server_hello_done without certificate", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
		err := m.HandleHandshake(netsec.HandshakeServerHelloDone, nil)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitCertificate)
	})

	t.Run("finished without certificate cannot recover", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
		err := m.HandleHandshake(netsec.HandshakeCertificate, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = m.HandleHandshake(netsec.HandshakeServerHelloDone, nil)
		expectUnexpectedMessage(t, m, err, netsec.StateWaitServerKeyExchange)
	})
}

// The mandated attack: a second ServerHello is replayed into the handshake.
func TestDuplicateServerHello(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
	err := m.HandleHandshake(netsec.HandshakeServerHello, nil)
	expectUnexpectedMessage(t, m, err, netsec.StateWaitCertificate)

	// A failed machine rejects everything afterwards.
	if err := m.HandleHandshake(netsec.HandshakeCertificate, nil); err == nil {
		t.Fatal("failed machine must keep rejecting messages")
	}
}

func TestHappyPathWithoutCertificateRequest(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	driveTo(t, m, false)

	if m.CertificateRequestSeen() {
		t.Fatal("no certificate request should be recorded")
	}
	if m.ReadEncrypted() {
		t.Fatal("encryption must remain off before the accepted CCS")
	}
	mustOK(t, m.NotifyClientKeyExchangeSent())
	mustOK(t, m.HandleChangeCipherSpec(ccsPayload))
	if !m.ReadEncrypted() {
		t.Fatal("encryption must be on after accepted CCS")
	}
	mustOK(t, m.HandleHandshake(netsec.HandshakeFinished, nil))
	if !m.Complete() || m.State() != netsec.StateComplete {
		t.Fatalf("handshake should be complete, got %s", m.State())
	}

	// Post-completion traffic must not restart the handshake.
	err := m.HandleHandshake(netsec.HandshakeServerHello, nil)
	var he *netsec.HandshakeError
	if !errors.As(err, &he) || he.Alert != netsec.AlertUnexpectedMessage {
		t.Fatalf("post-completion message must be unexpected_message, got %v", err)
	}
	if he.State != netsec.StateComplete {
		t.Fatalf("expected captured state complete, got %s", he.State)
	}
	if m.State() != netsec.StateFailed {
		t.Fatalf("machine must move to failed, got %s", m.State())
	}
}

func TestHappyPathWithCertificateRequest(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	driveTo(t, m, true)
	if !m.CertificateRequestSeen() {
		t.Fatal("certificate request should be recorded")
	}

	// SHD has already consumed the wait_server_hello_done state; the only
	// legal move now is the encrypted phase, so another handshake message is
	// rejected as out of order.
	err := m.HandleHandshake(netsec.HandshakeCertificateRequest, nil)
	expectUnexpectedMessage(t, m, err, netsec.StateWaitChangeCipherSpec)

	// Duplicate CertificateRequest at the point where the first one is still
	// expected must likewise be rejected.
	m2 := netsec.NewTLS12ClientHandshake()
	mustOK(t, m2.HandleHandshake(netsec.HandshakeServerHello, nil))
	mustOK(t, m2.HandleHandshake(netsec.HandshakeCertificate, nil))
	mustOK(t, m2.HandleHandshake(netsec.HandshakeServerKeyExchange, nil))
	mustOK(t, m2.HandleHandshake(netsec.HandshakeCertificateRequest, nil))
	err = m2.HandleHandshake(netsec.HandshakeCertificateRequest, nil)
	expectUnexpectedMessage(t, m2, err, netsec.StateWaitServerHelloDone)
}

func TestFinishedBeforeCCSRejected(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	driveTo(t, m, false)
	mustOK(t, m.NotifyClientKeyExchangeSent())
	err := m.HandleHandshake(netsec.HandshakeFinished, nil)
	expectUnexpectedMessage(t, m, err, netsec.StateWaitChangeCipherSpec)
}

func TestClientKeyExchangeGating(t *testing.T) {
	t.Run("signalled too early", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
		err := m.NotifyClientKeyExchangeSent()
		expectUnexpectedMessage(t, m, err, netsec.StateWaitCertificate)
	})

	t.Run("signalled twice", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		driveTo(t, m, false)
		mustOK(t, m.NotifyClientKeyExchangeSent())
		if err := m.NotifyClientKeyExchangeSent(); err == nil {
			t.Fatal("duplicate client_key_exchange notification must be rejected")
		} else if m.State() != netsec.StateFailed {
			t.Fatalf("duplicate notification must abort, got %s", m.State())
		}
	})

	t.Run("signalled after ccs", func(t *testing.T) {
		m := netsec.NewTLS12ClientHandshake()
		driveTo(t, m, false)
		mustOK(t, m.NotifyClientKeyExchangeSent())
		mustOK(t, m.HandleChangeCipherSpec(ccsPayload))
		err := m.NotifyClientKeyExchangeSent()
		var he *netsec.HandshakeError
		if !errors.As(err, &he) || he.Alert != netsec.AlertUnexpectedMessage {
			t.Fatalf("post-CCS notification must be unexpected_message, got %v", err)
		}
		if he.State != netsec.StateWaitFinished {
			t.Fatalf("expected captured state wait_finished, got %s", he.State)
		}
		if m.State() != netsec.StateFailed {
			t.Fatalf("machine must abort, got %s", m.State())
		}
		// The earlier CCS was legitimate, so read crypto had already been
		// activated; the abort only blocks further state progression.
		if !m.ReadEncrypted() {
			t.Fatal("read crypto activated by the valid CCS must remain on")
		}
	})
}

func TestRecordDispatcher(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	mustOK(t, m.HandleRecord(netsec.RecordTypeHandshake, hs(netsec.HandshakeServerHello, 0)))
	mustOK(t, m.HandleRecord(netsec.RecordTypeHandshake, hs(netsec.HandshakeCertificate, 2)))

	// Early CCS fed through the record adapter must be rejected as well.
	err := m.HandleRecord(netsec.RecordTypeChangeCipherSpec, ccsPayload)
	expectUnexpectedMessage(t, m, err, netsec.StateWaitServerKeyExchange)

	// Application data before completion is never legal.
	m2 := netsec.NewTLS12ClientHandshake()
	err = m2.HandleRecord(netsec.RecordTypeApplicationData, []byte("x"))
	expectUnexpectedMessage(t, m2, err, netsec.StateWaitServerHello)

	// Truncated handshake record.
	m3 := netsec.NewTLS12ClientHandshake()
	if err := m3.HandleRecord(netsec.RecordTypeHandshake, []byte{2}); err == nil {
		t.Fatal("truncated handshake record must be rejected")
	}
}

func TestZeroValueStart(t *testing.T) {
	var m netsec.TLS12ClientHandshake
	if m.State() != netsec.StateNew {
		t.Fatalf("zero value must be StateNew, got %s", m.State())
	}
	mustOK(t, m.Start())
	if m.State() != netsec.StateWaitServerHello {
		t.Fatalf("expected wait_server_hello, got %s", m.State())
	}
	if err := m.Start(); err == nil {
		t.Fatal("double Start must fail")
	}
	if m.State() != netsec.StateFailed {
		t.Fatalf("double Start must abort, got %s", m.State())
	}
}

func TestAsHandshakeError(t *testing.T) {
	m := netsec.NewTLS12ClientHandshake()
	err := m.HandleChangeCipherSpec(ccsPayload)
	he, ok := netsec.AsHandshakeError(err)
	if !ok {
		t.Fatalf("AsHandshakeError failed for %v", err)
	}
	if he.Alert.String() != "unexpected_message" {
		t.Fatalf("unexpected alert label %q", he.Alert.String())
	}
	if _, ok := netsec.AsHandshakeError(nil); ok {
		t.Fatal("nil error must not unwrap")
	}
	if he.Error() == "" || !strings.Contains(he.Error(), "alert 10") ||
		!strings.Contains(he.Error(), "wait_server_hello") {
		t.Fatalf("handshake error must render a useful string, got %q", he.Error())
	}
	if got := netsec.AlertDescription(99).String(); got != "alert(99)" {
		t.Fatalf("unexpected unknown alert label %q", got)
	}
}

func TestRecordAndTerminalEdgeCases(t *testing.T) {
	// Alert and unknown record content types abort the handshake.
	m := netsec.NewTLS12ClientHandshake()
	err := m.HandleRecord(netsec.RecordTypeAlert, []byte{2, 0})
	expectUnexpectedMessage(t, m, err, netsec.StateWaitServerHello)

	m2 := netsec.NewTLS12ClientHandshake()
	err = m2.HandleRecord(255, nil)
	expectUnexpectedMessage(t, m2, err, netsec.StateWaitServerHello)

	// A CCS landing on a terminal machine is always a protocol violation.
	m3 := netsec.NewTLS12ClientHandshake()
	if err := m3.HandleChangeCipherSpec(ccsPayload); err == nil {
		t.Fatal("early CCS must fail")
	}
	if err := m3.HandleChangeCipherSpec(ccsPayload); err == nil {
		t.Fatal("CCS on a failed machine must fail")
	} else if m3.State() != netsec.StateFailed {
		t.Fatalf("machine stays failed, got %s", m3.State())
	}

	m4 := netsec.NewTLS12ClientHandshake()
	driveTo(t, m4, false)
	mustOK(t, m4.NotifyClientKeyExchangeSent())
	mustOK(t, m4.HandleChangeCipherSpec(ccsPayload))
	mustOK(t, m4.HandleHandshake(netsec.HandshakeFinished, nil))
	if err := m4.HandleChangeCipherSpec(ccsPayload); err == nil {
		t.Fatal("CCS after completion must fail")
	}

	// A zero-value machine that was never started cannot consume messages.
	var m5 netsec.TLS12ClientHandshake
	err = m5.HandleHandshake(netsec.HandshakeServerHello, nil)
	expectUnexpectedMessage(t, &m5, err, netsec.StateNew)

	// Unknown handshake types render a fallback diagnostic name.
	m6 := netsec.NewTLS12ClientHandshake()
	err = m6.HandleHandshake(255, nil)
	if err == nil || !strings.Contains(err.Error(), "handshake_type(255)") {
		t.Fatalf("unknown type must be named in the error, got %v", err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// driveTo advances a fresh machine through the plaintext server flight up to
// the point where the local ClientKeyExchange is due. With certRequest set it
// also consumes the optional CertificateRequest.
func driveTo(t *testing.T, m *netsec.TLS12ClientHandshake, certRequest bool) {
	t.Helper()
	mustOK(t, m.HandleHandshake(netsec.HandshakeServerHello, nil))
	mustOK(t, m.HandleHandshake(netsec.HandshakeCertificate, nil))
	mustOK(t, m.HandleHandshake(netsec.HandshakeServerKeyExchange, nil))
	if certRequest {
		mustOK(t, m.HandleHandshake(netsec.HandshakeCertificateRequest, nil))
	}
	mustOK(t, m.HandleHandshake(netsec.HandshakeServerHelloDone, nil))
	if m.State() != netsec.StateWaitChangeCipherSpec {
		t.Fatalf("expected wait_change_cipher_spec, got %s", m.State())
	}
}
