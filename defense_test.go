package streamguard_test

import (
	"errors"
	"testing"

	sg "formal.local/w27-8ca5752422"
)

const (
	testPlainStreamOpen = `<stream:stream` +
		` xmlns:stream="http://etherx.jabber.org/streams"` +
		` xmlns="jabber:client" version="1.0">`
	testSecureStreamOpen = `<stream:stream` +
		` xmlns:stream="http://etherx.jabber.org/streams"` +
		` xmlns="jabber:client" version="1.0" id="c2s-encrypted">`
	testStartTLS = `<starttls xmlns="urn:ietf:params:xml:ns:xmpp-tls"/>`
	testAuth     = `<auth xmlns="urn:ietf:params:xml:ns:xmpp-sasl" mechanism="PLAIN">` +
		`AGFsaWNlAHNlY3JldA==</auth>`
	testBindIQ = `<iq type="set" id="bind-1">` +
		`<bind xmlns="urn:ietf:params:xml:ns:xmpp-bind">` +
		`<resource>edge-node-7</resource>` +
		`</bind></iq>`
	testControlStanza = `<message from="gateway@example/console" to="device@example/edge-node-7" type="chat">` +
		`<body>SET_PUMP=ON</body></message>`
)

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorIs(t *testing.T, err error, targets ...error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, target := range targets {
		if !errors.Is(err, target) {
			t.Fatalf("error %v does not wrap %v", err, target)
		}
	}
}

func TestMachineEnforcesExactOrderedPhases(t *testing.T) {
	machine := sg.NewMachine(sg.DefaultConfig())

	err := machine.SASLAuth("PLAIN")
	requireErrorIs(t, err, sg.ErrInvalidState)

	requireNoError(t, machine.StreamOpen())
	err = machine.SASLAuth("PLAIN")
	requireErrorIs(t, err, sg.ErrInvalidState)

	requireNoError(t, machine.StartTLS())
	err = machine.SASLAuth("PLAIN")
	requireErrorIs(t, err, sg.ErrInvalidState)

	requireNoError(t, machine.ResetBufferAndStream())
	err = machine.ResourceBind("edge")
	requireErrorIs(t, err, sg.ErrInvalidState)

	requireNoError(t, machine.SASLAuth("PLAIN"))
	requireNoError(t, machine.ResourceBind("edge"))
	if machine.State() != sg.StateResourceBind {
		t.Fatalf("expected ResourceBind state, got %s", machine.State())
	}
}

func TestStartTLSStrippingRejectsPlaintextSASL(t *testing.T) {
	guard := sg.NewGuard(sg.DefaultConfig())
	attack := testPlainStreamOpen + testAuth

	_, err := guard.Feed([]byte(attack))
	requireErrorIs(
		t,
		err,
		sg.ErrSASLBeforeTLS,
		sg.ErrStartTLSStripped,
		sg.ErrTLSRequired,
	)
	if guard.State() != sg.StateStreamOpen {
		t.Fatalf("state must remain StreamOpen after stripped StartTLS, got %s", guard.State())
	}
}

func TestPlaintextBusinessStanzaIsRejectedBeforeStartTLS(t *testing.T) {
	guard := sg.NewGuard(sg.DefaultConfig())
	attack := testPlainStreamOpen +
		`<iq type="set" id="evil"><control xmlns="iot:actuator"><pump>ON</pump></control></iq>`

	_, err := guard.Feed([]byte(attack))
	requireErrorIs(t, err, sg.ErrStanzaBeforeTLS, sg.ErrStartTLSStripped)
	if guard.State() != sg.StateStreamOpen {
		t.Fatalf("unexpected state %s", guard.State())
	}
}

func TestStartTLSUpgradeDiscardsSmuggledPlaintextBuffer(t *testing.T) {
	smuggled := `<message id="smuggled-control" xmlns="jabber:client">` +
		`<body>OPEN_PUMP_NOW</body></message>`
	attack := testPlainStreamOpen + testStartTLS + smuggled

	guard := sg.NewGuard(sg.DefaultConfig())
	frames, err := guard.Feed([]byte(attack))
	requireNoError(t, err)

	if len(frames) != 2 {
		t.Fatalf("expected only StreamOpen and StartTLS frames before upgrade, got %d", len(frames))
	}
	if frames[0].Kind != sg.FrameStreamOpen || frames[1].Kind != sg.FrameStartTLS {
		t.Fatalf("unexpected pre-upgrade frames: %+v", frames)
	}
	if guard.Buffered() != len(smuggled) {
		t.Fatalf("expected %d smuggled bytes buffered, got %d", len(smuggled), guard.Buffered())
	}

	_, err = guard.ReadFrame()
	requireErrorIs(t, err, sg.ErrTLSUpgradePending)

	requireNoError(t, guard.UpgradeTLS())
	if guard.Buffered() != 0 {
		t.Fatalf("smuggled bytes survived StartTLS reset: %d", guard.Buffered())
	}
	if guard.DiscardedBytes() != len(smuggled) {
		t.Fatalf("expected %d discarded bytes, got %d", len(smuggled), guard.DiscardedBytes())
	}
	if guard.State() != sg.StateResetBufferAndStream {
		t.Fatalf("unexpected state after reset: %s", guard.State())
	}

	_, err = guard.ReadFrame()
	requireErrorIs(t, err, sg.ErrNeedMore)

	// The post-TLS stream starts cleanly and can complete the defended flow.
	_, err = guard.Feed([]byte(testSecureStreamOpen))
	requireNoError(t, err)

	_, err = guard.Feed([]byte(testAuth))
	requireNoError(t, err)
	if guard.State() != sg.StateSASLAuth {
		t.Fatalf("expected SASLAuth state, got %s", guard.State())
	}

	err = guard.SASLSuccess()
	requireNoError(t, err)

	bindFrames, err := guard.Feed([]byte(testBindIQ))
	requireNoError(t, err)
	if len(bindFrames) != 1 || bindFrames[0].Kind != sg.FrameResourceBind {
		t.Fatalf("expected one ResourceBind frame, got %+v err=%v", bindFrames, err)
	}
	if guard.State() != sg.StateResourceBind {
		t.Fatalf("expected ResourceBind state, got %s", guard.State())
	}

	requireNoError(t, guard.BindSuccess())
	stanzaFrames, err := guard.Feed([]byte(testControlStanza))
	requireNoError(t, err)
	if len(stanzaFrames) != 1 || stanzaFrames[0].Kind != sg.FrameStanza {
		t.Fatalf("expected encrypted control stanza after bind, got %+v", stanzaFrames)
	}
	if body := stanzaFrames[0].Element.Child("body"); body == nil || body.Text != "SET_PUMP=ON" {
		t.Fatalf("control stanza body not parsed correctly: %+v", stanzaFrames[0].Element)
	}
}

func TestStartTLSUpgradeDiscardsPipelinedPlaintextCredentials(t *testing.T) {
	guard := sg.NewGuard(sg.DefaultConfig())
	attack := testPlainStreamOpen + testStartTLS + testAuth

	frames, err := guard.Feed([]byte(attack))
	requireNoError(t, err)
	if len(frames) != 2 {
		t.Fatalf("expected parser to stop after StartTLS, got %d frames", len(frames))
	}
	if guard.Buffered() != len(testAuth) {
		t.Fatalf("expected pipelined auth to remain unprocessed, buffered=%d want=%d", guard.Buffered(), len(testAuth))
	}

	requireNoError(t, guard.ResetBufferAndStream())
	if guard.Buffered() != 0 {
		t.Fatal("plaintext SASL bytes survived buffer reset")
	}
	if guard.State() != sg.StateResetBufferAndStream {
		t.Fatalf("unexpected state %s", guard.State())
	}

	// A fresh encrypted stream is required; discarded auth did not advance state.
	_, err = guard.Feed([]byte(testSecureStreamOpen))
	requireNoError(t, err)
	if guard.Machine().SASLAuthenticated() {
		t.Fatal("discarded plaintext auth must not authenticate the machine")
	}
}

func TestUpgradeWithoutStartTLSIsRejected(t *testing.T) {
	guard := sg.NewGuard(sg.DefaultConfig())
	_, err := guard.Feed([]byte(testPlainStreamOpen))
	requireNoError(t, err)

	err = guard.UpgradeTLS()
	requireErrorIs(t, err, sg.ErrInvalidState)
	if guard.IsTLS() {
		t.Fatal("guard must not enable TLS without StartTLS phase")
	}
}
