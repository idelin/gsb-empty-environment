package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRelayLegitimateRequireTLSChain(t *testing.T) {
	cert := testCertificate(t)
	nextHop := newFakeNextHop(t, fakeNextHopConfig{
		OfferSTARTTLS:             true,
		AdvertiseRequireTLSPreTLS: true,
		AdvertiseRequireTLS:       true,
	})
	deliverer := &Deliverer{
		Dial: nextHop.dial,
		VerifyCertificate: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("missing peer certificate")
			}
			return nil
		},
	}

	var relayed atomic.Bool
	conn := startInboundServer(t, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, func(msg *InboundMessage) error {
		if !msg.TLS || !msg.RequireTLS {
			return errors.New("inbound REQUIRETLS/TLS policy not satisfied")
		}
		err := deliverer.Deliver(context.Background(),
			Target{Address: nextHop.address(), ServerName: "mx.test"},
			&Envelope{
				MailFrom:   msg.MailFrom,
				RcptTo:     msg.RcptTo,
				Data:       msg.Data,
				RequireTLS: msg.RequireTLS,
			})
		if err == nil {
			relayed.Store(true)
		}
		return err
	})

	c := newTestSMTPClient(t, conn)
	if reply := c.read(); reply.code != 220 {
		t.Fatalf("greeting = %d", reply.code)
	}
	if reply := c.send("EHLO sender.example"); reply.code != 250 {
		t.Fatalf("EHLO = %d %s", reply.code, reply.text())
	}
	c.startTLS()
	if reply := c.send("EHLO sender.example"); reply.code != 250 {
		t.Fatalf("EHLO after TLS = %d %s", reply.code, reply.text())
	}
	if reply := c.send("MAIL FROM:<sender@example.org> REQUIRETLS"); reply.code != 250 {
		t.Fatalf("MAIL = %d %s", reply.code, reply.text())
	}
	if reply := c.send("RCPT TO:<victim@example.org>"); reply.code != 250 {
		t.Fatalf("RCPT = %d %s", reply.code, reply.text())
	}
	if reply := c.send("DATA"); reply.code != 354 {
		t.Fatalf("DATA = %d %s", reply.code, reply.text())
	}
	raw := "From: sender@example.org\r\nSubject: chain\r\n\r\nbody\r\n.\r\n"
	if _, err := c.w.WriteString(raw); err != nil {
		t.Fatal(err)
	}
	if err := c.w.Flush(); err != nil {
		t.Fatal(err)
	}
	if reply := c.read(); reply.code != 250 {
		t.Fatalf("relay rejected legitimate chain: %d %s", reply.code, reply.text())
	}
	_ = c.send("QUIT")

	if !relayed.Load() {
		t.Fatal("outbound handoff did not complete")
	}
	commands, data := nextHop.snapshot()
	var sawRequireTLSMail bool
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToUpper(cmd.Line), "MAIL ") && strings.Contains(strings.ToUpper(cmd.Line), "REQUIRETLS") && cmd.TLS {
			sawRequireTLSMail = true
		}
	}
	if !sawRequireTLSMail {
		t.Fatalf("next hop did not receive a TLS-protected REQUIRETLS MAIL command: %v", commands)
	}
	if !strings.Contains(string(data), "TLS-Required: Require\r\n") {
		t.Fatalf("next hop data missing TLS-Required: Require:\n%s", data)
	}
}

func TestInboundRejectsPlaintextRequireTLS(t *testing.T) {
	cert := testCertificate(t)
	cases := []struct {
		name      string
		tlsConfig *tls.Config
		offersTLS bool
	}{
		{
			name:      "TLS configured but attacker did not STARTTLS",
			tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			offersTLS: true,
		},
		{
			name: "no STARTTLS support",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := make(chan *InboundMessage, 1)
			conn := startInboundServer(t, tc.tlsConfig, func(msg *InboundMessage) error {
				called <- msg
				return nil
			})
			c := newTestSMTPClient(t, conn)
			if reply := c.read(); reply.code != 220 {
				t.Fatalf("greeting = %d %s", reply.code, reply.text())
			}
			ehlo := c.send("EHLO attacker.example")
			if ehlo.code != 250 {
				t.Fatalf("EHLO = %d %s", ehlo.code, ehlo.text())
			}
			caps := capabilities(ehlo)
			if _, ok := caps["REQUIRETLS"]; !ok {
				t.Fatal("EHLO did not advertise REQUIRETLS")
			}
			if _, ok := caps["STARTTLS"]; ok != tc.offersTLS {
				t.Fatalf("STARTTLS advertised = %v, want %v", ok, tc.offersTLS)
			}

			mail := c.send("MAIL FROM:<sender@example.org> REQUIRETLS")
			if mail.code < 500 || mail.code > 599 {
				t.Fatalf("plainttext REQUIRETLS MAIL reply = %d %s, want 5xx", mail.code, mail.text())
			}
			if !strings.Contains(strings.ToLower(mail.text()), "requiretls") {
				t.Fatalf("rejection did not explain REQUIRETLS: %s", mail.text())
			}

			rcpt := c.send("RCPT TO:<victim@example.org>")
			if rcpt.code != 503 {
				t.Fatalf("RCPT after rejected MAIL = %d %s, want 503", rcpt.code, rcpt.text())
			}
			_ = c.send("QUIT")

			select {
			case msg := <-called:
				t.Fatalf("handler accepted forged cleartext REQUIRETLS message: %+v", msg.Envelope)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}

func TestInboundAcceptsRequireTLSAfterSTARTTLS(t *testing.T) {
	cert := testCertificate(t)
	called := make(chan *InboundMessage, 1)
	conn := startInboundServer(t, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, func(msg *InboundMessage) error {
		called <- msg
		return nil
	})

	c := newTestSMTPClient(t, conn)
	if reply := c.read(); reply.code != 220 {
		t.Fatalf("greeting = %d", reply.code)
	}
	if reply := c.send("EHLO sender.example"); reply.code != 250 {
		t.Fatalf("EHLO = %d %s", reply.code, reply.text())
	}
	c.startTLS()
	if reply := c.send("EHLO sender.example"); reply.code != 250 {
		t.Fatalf("EHLO after TLS = %d %s", reply.code, reply.text())
	}
	if reply := c.send("MAIL FROM:<sender@example.org> REQUIRETLS"); reply.code != 250 {
		t.Fatalf("MAIL = %d %s", reply.code, reply.text())
	}
	if reply := c.send("RCPT TO:<victim@example.org>"); reply.code != 250 {
		t.Fatalf("RCPT = %d %s", reply.code, reply.text())
	}
	if reply := c.send("DATA"); reply.code != 354 {
		t.Fatalf("DATA = %d %s", reply.code, reply.text())
	}
	raw := "From: sender@example.org\r\nSubject: hi\r\n\r\nbody\r\n.\r\n"
	if _, err := c.w.WriteString(raw); err != nil {
		t.Fatal(err)
	}
	if err := c.w.Flush(); err != nil {
		t.Fatal(err)
	}
	if reply := c.read(); reply.code != 250 {
		t.Fatalf("end of DATA = %d %s", reply.code, reply.text())
	}
	_ = c.send("QUIT")

	select {
	case msg := <-called:
		if !msg.TLS {
			t.Fatal("handler saw a non-TLS session")
		}
		if !msg.RequireTLS {
			t.Fatal("handler lost the REQUIRETLS flag")
		}
		if msg.MailFrom != "sender@example.org" {
			t.Fatalf("MailFrom = %q", msg.MailFrom)
		}
		if len(msg.RcptTo) != 1 || msg.RcptTo[0] != "victim@example.org" {
			t.Fatalf("RcptTo = %v", msg.RcptTo)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not called")
	}
}

func TestInboundRejectsNoHeaderWithRequireTLS(t *testing.T) {
	cert := testCertificate(t)
	called := make(chan *InboundMessage, 1)
	conn := startInboundServer(t, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, func(msg *InboundMessage) error {
		called <- msg
		return nil
	})

	c := newTestSMTPClient(t, conn)
	if reply := c.read(); reply.code != 220 {
		t.Fatalf("greeting = %d", reply.code)
	}
	_ = c.send("EHLO sender.example")
	c.startTLS()
	_ = c.send("EHLO sender.example")
	if reply := c.send("MAIL FROM:<sender@example.org> REQUIRETLS"); reply.code != 250 {
		t.Fatalf("MAIL = %d %s", reply.code, reply.text())
	}
	if reply := c.send("RCPT TO:<victim@example.org>"); reply.code != 250 {
		t.Fatalf("RCPT = %d %s", reply.code, reply.text())
	}
	if reply := c.send("DATA"); reply.code != 354 {
		t.Fatalf("DATA = %d %s", reply.code, reply.text())
	}
	raw := "From: sender@example.org\r\nTLS-Required: No\r\n\r\nbody\r\n.\r\n"
	if _, err := c.w.WriteString(raw); err != nil {
		t.Fatal(err)
	}
	if err := c.w.Flush(); err != nil {
		t.Fatal(err)
	}
	reply := c.read()
	if reply.code != 554 {
		t.Fatalf("DATA with conflicting header = %d %s, want 554", reply.code, reply.text())
	}
	_ = c.send("QUIT")

	select {
	case msg := <-called:
		t.Fatalf("handler accepted TLS-Required: No with REQUIRETLS: %+v", msg.Envelope)
	case <-time.After(200 * time.Millisecond):
	}
}
