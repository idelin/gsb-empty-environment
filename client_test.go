package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

func requireTLSMessage(data string) *Envelope {
	return &Envelope{
		MailFrom:   "sender@example.org",
		RcptTo:     []string{"victim@example.org"},
		Data:       []byte(data),
		RequireTLS: true,
	}
}

func assertNoMailCommand(t *testing.T, commands []recordedCommand) {
	t.Helper()
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToUpper(cmd.Line), "MAIL ") {
			t.Fatalf("client sent MAIL before satisfying REQUIRETLS policy: %q (TLS=%v)", cmd.Line, cmd.TLS)
		}
	}
}

func TestDelivererRejectsMissingSTARTTLS(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS: false,
	})
	d := &Deliverer{
		Dial: hop.dial,
		VerifyCertificate: func(tls.ConnectionState) error {
			t.Error("certificate verifier called without STARTTLS")
			return nil
		},
	}
	err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"},
		requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent failure", err)
	}
	var derr *DeliveryError
	if !errors.As(err, &derr) || derr.Stage != "starttls-capability" {
		t.Fatalf("failure stage = %+v, want starttls-capability", derr)
	}
	commands, _ := hop.snapshot()
	assertNoMailCommand(t, commands)
}

func TestDelivererRejectsSTARTTLSRefusal(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		RejectSTARTTLS:            true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	d := &Deliverer{Dial: hop.dial}
	err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"},
		requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent failure", err)
	}
	var derr *DeliveryError
	if !errors.As(err, &derr) || derr.Stage != "starttls" {
		t.Fatalf("failure stage = %+v, want starttls", derr)
	}
	commands, _ := hop.snapshot()
	assertNoMailCommand(t, commands)
}

func TestDelivererRejectsCertificateFailure(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	errVerify := errors.New("certificate policy rejected the peer")
	var calls atomic.Int32
	d := &Deliverer{
		Dial: hop.dial,
		VerifyCertificate: func(state tls.ConnectionState) error {
			calls.Add(1)
			if len(state.PeerCertificates) == 0 {
				return errors.New("no peer certificate")
			}
			return errVerify
		},
	}
	err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"},
		requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent failure", err)
	}
	if !errors.Is(err, errVerify) {
		t.Fatalf("Deliver error = %v, want wrapped verification error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}
	var derr *DeliveryError
	if !errors.As(err, &derr) || derr.Stage != "tls" {
		t.Fatalf("failure stage = %+v, want tls", derr)
	}
	commands, _ := hop.snapshot()
	assertNoMailCommand(t, commands)
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToUpper(cmd.Line), "EHLO ") && cmd.TLS {
			t.Fatalf("client continued with EHLO after failed verification: %q", cmd.Line)
		}
	}
}

func TestDelivererStandardVerificationFailure(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	d := &Deliverer{Dial: hop.dial, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"},
		requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent certificate failure", err)
	}
	commands, _ := hop.snapshot()
	assertNoMailCommand(t, commands)
}

func TestDelivererRequiresRequireTLSInPostTLSEHLO(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       false,
	})
	var calls atomic.Int32
	d := &Deliverer{
		Dial: hop.dial,
		VerifyCertificate: func(tls.ConnectionState) error {
			calls.Add(1)
			return nil
		},
	}
	err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"},
		requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent failure", err)
	}
	var derr *DeliveryError
	if !errors.As(err, &derr) || derr.Stage != "requiretls-capability" {
		t.Fatalf("failure stage = %+v, want requiretls-capability", derr)
	}
	if calls.Load() != 1 {
		t.Fatalf("verifier calls = %d, want 1", calls.Load())
	}
	commands, _ := hop.snapshot()
	assertNoMailCommand(t, commands)
	var sawPostTLSEHLO bool
	for _, cmd := range commands {
		if cmd.TLS && strings.HasPrefix(strings.ToUpper(cmd.Line), "EHLO ") {
			sawPostTLSEHLO = true
		}
	}
	if !sawPostTLSEHLO {
		t.Fatal("client did not re-issue EHLO after STARTTLS")
	}
}

func TestDelivererLegitimateTLSChain(t *testing.T) {
	hop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	var verified atomic.Bool
	d := &Deliverer{
		Dial: hop.dial,
		VerifyCertificate: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 {
				return errors.New("unexpected certificate count")
			}
			if state.Version < tls.VersionTLS12 {
				return errors.New("TLS version too old")
			}
			verified.Store(true)
			return nil
		},
	}
	msg := requireTLSMessage("From: sender@example.org\r\nSubject: test\r\n\r\nhello\r\n")
	if err := d.Deliver(context.Background(), Target{Address: hop.address(), ServerName: "mx.test"}, msg); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !verified.Load() {
		t.Fatal("certificate verifier was not called")
	}

	commands, data := hop.snapshot()
	var sawMail, sawTLS bool
	for _, cmd := range commands {
		upper := strings.ToUpper(cmd.Line)
		if strings.HasPrefix(upper, "MAIL ") {
			sawMail = true
			if !strings.Contains(upper, "REQUIRETLS") {
				t.Fatalf("MAIL command lacked REQUIRETLS: %q", cmd.Line)
			}
			if !cmd.TLS {
				t.Fatalf("MAIL command was sent in cleartext: %q", cmd.Line)
			}
		}
		if strings.HasPrefix(upper, "STARTTLS") {
			sawTLS = true
		}
	}
	if !sawMail || !sawTLS {
		t.Fatalf("commands = %v, want STARTTLS and MAIL", commands)
	}
	if !strings.Contains(string(data), "TLS-Required: Require\r\n") {
		t.Fatalf("delivered data missing TLS-Required: Require:\n%s", data)
	}
	if strings.Count(strings.ToLower(string(data)), "tls-required:") != 1 {
		t.Fatalf("delivered data has duplicate/missing TLS-Required fields:\n%s", data)
	}
	if !strings.Contains(string(data), "hello\r\n") {
		t.Fatalf("delivered data lost body:\n%s", data)
	}
}

func TestDeliverAllStopsOnPermanentFailure(t *testing.T) {
	bad := newFakeNextHop(t, fakeNextHopConfig{OfferSTARTTLS: false})
	good := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	d := &Deliverer{
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			switch address {
			case bad.address():
				return bad.dial(context.Background(), "", "")
			case good.address():
				return good.dial(context.Background(), "", "")
			default:
				return nil, errors.New("unexpected target " + address)
			}
		},
		VerifyCertificate: func(tls.ConnectionState) error { return nil },
	}
	err := d.DeliverAll(context.Background(), []Target{
		{Address: bad.address(), ServerName: "mx.test"},
		{Address: good.address(), ServerName: "mx.test"},
	}, requireTLSMessage("Subject: x\r\n\r\ny\r\n"))
	if err == nil || !IsPermanent(err) {
		t.Fatalf("DeliverAll error = %v, want permanent failure", err)
	}
	commands, _ := good.snapshot()
	if len(commands) != 0 {
		t.Fatalf("DeliverAll touched the second MX after a permanent policy failure: %v", commands)
	}
}

func TestDelivererRejectsHeaderConflictBeforeDialing(t *testing.T) {
	var dials atomic.Int32
	d := &Deliverer{
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("should not dial")
		},
	}
	msg := requireTLSMessage("From: sender@example.org\r\nTLS-Required: No\r\n\r\nbody\r\n")
	err := d.Deliver(context.Background(), Target{Address: "127.0.0.1:2525"}, msg)
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Deliver error = %v, want permanent failure", err)
	}
	if !errors.Is(err, ErrTLSRequiredHeaderConflict) {
		t.Fatalf("Deliver error = %v, want header conflict", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("dialed %d times for a locally rejected message", dials.Load())
	}
}
