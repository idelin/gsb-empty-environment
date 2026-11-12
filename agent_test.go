package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

type testHost struct {
	name string
	priv ed25519.PrivateKey
	blob []byte
}

func newTestHost(t *testing.T, name string) *testHost {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate %s host key: %v", name, err)
	}
	blob := appendString(nil, []byte("ssh-ed25519"))
	blob = appendString(blob, pub)
	return &testHost{name: name, priv: priv, blob: blob}
}

func (h *testHost) binding(sessionID []byte, username string, forwarding bool) SessionBinding {
	sigBody := ed25519.Sign(h.priv, sessionID)
	sigBlob := appendString(nil, []byte("ssh-ed25519"))
	sigBlob = appendString(sigBlob, sigBody)
	return SessionBinding{
		Hostname:     h.name,
		Username:     username,
		HostKey:      copyBytes(h.blob),
		SessionID:    copyBytes(sessionID),
		Signature:    sigBlob,
		IsForwarding: forwarding,
	}
}

func mustBind(t *testing.T, sess *Session, h *testHost, sessionID []byte, username string, forwarding bool) {
	t.Helper()
	if err := sess.Bind(h.binding(sessionID, username, forwarding)); err != nil {
		t.Fatalf("bind %s: %v", h.name, err)
	}
}

func buildSignatureData(t *testing.T, sessionID []byte, username, method string, userKey, serverHostKey []byte) []byte {
	t.Helper()
	data := appendString(nil, sessionID)
	data = append(data, SSH2MsgUserAuthRequest)
	data = appendStringValue(data, username)
	data = appendStringValue(data, "ssh-connection")
	data = appendStringValue(data, method)
	data = appendBool(data, true)
	data = appendStringValue(data, "ssh-ed25519")
	data = appendString(data, userKey)
	if method == PublicKeyHostBoundMethod {
		data = appendString(data, serverHostKey)
	}
	return data
}

func buildSignRequest(t *testing.T, userKey, data []byte) []byte {
	t.Helper()
	body := []byte{AgentcSignRequest}
	body = appendString(body, userKey)
	body = appendString(body, data)
	body = appendUint32(body, 0)
	frame := appendUint32(nil, uint32(len(body)))
	return append(frame, body...)
}

func newCountedSigner(t *testing.T) (SignCallback, *int) {
	t.Helper()
	calls := 0
	cb := func(keyBlob, data []byte, flags uint32) ([]byte, error) {
		calls++
		if len(keyBlob) == 0 || len(data) == 0 {
			t.Fatalf("callback received empty key or data")
		}
		if flags != 0 {
			t.Fatalf("callback flags = %d, want 0", flags)
		}
		return []byte("signature"), nil
	}
	return cb, &calls
}

func TestOnlyAllowedForwardingPathsReachSigner(t *testing.T) {
	bastion := newTestHost(t, "bastion")
	dbA := newTestHost(t, "db-a")
	dbB := newTestHost(t, "db-b")
	dbC := newTestHost(t, "db-c")
	user := newTestHost(t, "user-key")

	agent := NewAgent()
	origin := Hop{Hostname: "origin"}
	if err := agent.RegisterKey(user.blob,
		Chain{origin, {Hostname: "bastion", Username: "ops", HostKey: bastion.blob},
			{Hostname: "db-a", Username: "alice", HostKey: dbA.blob}},
		Chain{origin, {Hostname: "bastion", Username: "ops", HostKey: bastion.blob},
			{Hostname: "db-b", Username: "bob", HostKey: dbB.blob}},
		Chain{origin, {Hostname: "db-a", Username: "alice", HostKey: dbA.blob}},
	); err != nil {
		t.Fatalf("register key: %v", err)
	}

	cb, calls := newCountedSigner(t)

	sign := func(sess *Session, username string, finalHost *testHost, sessionID []byte) {
		t.Helper()
		data := buildSignatureData(t, sessionID, username, PublicKeyHostBoundMethod, user.blob, finalHost.blob)
		out, err := agent.HandleSignRequest(sess, buildSignRequest(t, user.blob, data), cb)
		if err != nil {
			t.Fatalf("sign for %s: %v", finalHost.name, err)
		}
		if string(out) != "signature" {
			t.Fatalf("signature = %q", out)
		}
	}

	// Two allowed paths that share the same intermediate bastion host.
	sessA := NewSession()
	mustBind(t, sessA, bastion, bytes.Repeat([]byte{0x11}, 32), "ops", true)
	mustBind(t, sessA, dbA, bytes.Repeat([]byte{0x12}, 32), "alice", false)
	sign(sessA, "alice", dbA, bytes.Repeat([]byte{0x12}, 32))
	if *calls != 1 {
		t.Fatalf("after allowed path A: calls = %d, want 1", *calls)
	}

	sessB := NewSession()
	mustBind(t, sessB, bastion, bytes.Repeat([]byte{0x21}, 32), "ops", true)
	mustBind(t, sessB, dbB, bytes.Repeat([]byte{0x22}, 32), "bob", false)
	sign(sessB, "bob", dbB, bytes.Repeat([]byte{0x22}, 32))
	if *calls != 2 {
		t.Fatalf("after shared-bastion path B: calls = %d, want 2", *calls)
	}

	// A legitimate direct connection explicitly registered for this key.
	sessDirect := NewSession()
	mustBind(t, sessDirect, dbA, bytes.Repeat([]byte{0x31}, 32), "alice", false)
	sign(sessDirect, "alice", dbA, bytes.Repeat([]byte{0x31}, 32))
	if *calls != 3 {
		t.Fatalf("after direct path: calls = %d, want 3", *calls)
	}

	// Tampering with the final hop must not reach the signer, even though the
	// intermediate bastion and its username are valid.
	sessTampered := NewSession()
	mustBind(t, sessTampered, bastion, bytes.Repeat([]byte{0x41}, 32), "ops", true)
	mustBind(t, sessTampered, dbC, bytes.Repeat([]byte{0x42}, 32), "carol", false)
	tampered := buildSignatureData(t, bytes.Repeat([]byte{0x42}, 32), "carol", PublicKeyHostBoundMethod,
		user.blob, dbC.blob)
	_, err := agent.HandleSignRequest(sessTampered, buildSignRequest(t, user.blob, tampered), cb)
	if !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("tampered final hop error = %v, want ErrDestinationMismatch", err)
	}
	if *calls != 3 {
		t.Fatalf("tampered final hop reached signer: calls = %d, want 3", *calls)
	}

	// Declaring a different final host key in the signed request than the
	// actually bound final hop is also rejected.
	claimedTampered := buildSignatureData(t, bytes.Repeat([]byte{0x42}, 32), "carol",
		PublicKeyHostBoundMethod, user.blob, dbA.blob)
	_, err = agent.HandleSignRequest(sessTampered, buildSignRequest(t, user.blob, claimedTampered), cb)
	if !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("claimed final host error = %v, want ErrDestinationMismatch", err)
	}
	if *calls != 3 {
		t.Fatalf("claimed final host reached signer: calls = %d, want 3", *calls)
	}

	// The final host key is allowed, but the username in the signed request
	// is not. The agent must reject before signing.
	wrongUser := buildSignatureData(t, bytes.Repeat([]byte{0x12}, 32), "mallory",
		PublicKeyHostBoundMethod, user.blob, dbA.blob)
	_, err = agent.HandleSignRequest(sessA, buildSignRequest(t, user.blob, wrongUser), cb)
	if !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("wrong username error = %v, want ErrDestinationMismatch", err)
	}
	if *calls != 3 {
		t.Fatalf("wrong username reached signer: calls = %d, want 3", *calls)
	}
}

func TestMissingDuplicateAndMalformedRequestsAreRejected(t *testing.T) {
	bastion := newTestHost(t, "bastion")
	dest := newTestHost(t, "dest")
	user := newTestHost(t, "user-key")

	agent := NewAgent()
	if err := agent.RegisterKey(user.blob,
		Chain{{Hostname: "bastion", Username: "ops", HostKey: bastion.blob},
			{Hostname: "dest", Username: "alice", HostKey: dest.blob}},
		Chain{{Hostname: "dest", Username: "alice", HostKey: dest.blob}},
	); err != nil {
		t.Fatalf("register key: %v", err)
	}

	cb, calls := newCountedSigner(t)

	// No session bind at all.
	missing := buildSignatureData(t, bytes.Repeat([]byte{0x01}, 32), "alice",
		PublicKeyHostBoundMethod, user.blob, dest.blob)
	_, err := agent.HandleSignRequest(NewSession(), buildSignRequest(t, user.blob, missing), cb)
	if !errors.Is(err, ErrMissingSessionBinding) {
		t.Fatalf("missing binding error = %v, want ErrMissingSessionBinding", err)
	}

	// A repeated host cannot be inserted into the chain.
	sess := NewSession()
	mustBind(t, sess, bastion, bytes.Repeat([]byte{0x10}, 32), "ops", true)
	if err := sess.Bind(bastion.binding(bytes.Repeat([]byte{0x20}, 32), "ops", true)); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("duplicate host error = %v, want ErrDuplicateNode", err)
	}
	// Low-level chain validation rejects a loop as well.
	if err := agent.Keys.ValidateForwardChain(user.blob,
		Chain{{Hostname: "bastion", Username: "ops", HostKey: bastion.blob},
			{Hostname: "bastion", Username: "ops", HostKey: bastion.blob}}, "alice"); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("duplicate chain error = %v, want ErrDuplicateNode", err)
	}

	// Ordinary data is not an SSH userauth request, even if it is wrapped in
	// the agent sign-request envelope.
	ordinary := appendString(nil, []byte("this is not an ssh userauth request"))
	_, err = agent.HandleSignRequest(sess, buildSignRequest(t, user.blob, ordinary), cb)
	if !errors.Is(err, ErrMalformedMessage) && !errors.Is(err, ErrNotUserAuthRequest) {
		t.Fatalf("ordinary data error = %v, want malformed/userauth error", err)
	}

	// A wrong auth method is rejected rather than treated as publickey auth.
	passwordData := appendString(nil, bytes.Repeat([]byte{0x30}, 32))
	passwordData = append(passwordData, SSH2MsgUserAuthRequest)
	passwordData = appendStringValue(passwordData, "alice")
	passwordData = appendStringValue(passwordData, "ssh-connection")
	passwordData = appendStringValue(passwordData, "password")
	passwordData = appendBool(passwordData, false)
	passwordData = appendStringValue(passwordData, "")
	_, err = agent.HandleSignRequest(sess, buildSignRequest(t, user.blob, passwordData), cb)
	if !errors.Is(err, ErrUnsupportedAuthMethod) {
		t.Fatalf("password method error = %v, want ErrUnsupportedAuthMethod", err)
	}

	// Trailing bytes make the sign request malformed.
	body := []byte{AgentcSignRequest}
	body = appendString(body, user.blob)
	body = appendString(body, missing)
	body = appendUint32(body, 0)
	body = append(body, 0xff)
	_, err = agent.HandleSignRequest(sess, body, cb)
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("trailing sign request bytes error = %v, want ErrMalformedMessage", err)
	}

	if *calls != 0 {
		t.Fatalf("rejected requests reached signer: calls = %d, want 0", *calls)
	}

	// A valid direct request still reaches the signer after all rejections.
	sessDirect := NewSession()
	mustBind(t, sessDirect, dest, bytes.Repeat([]byte{0x40}, 32), "alice", false)
	valid := buildSignatureData(t, bytes.Repeat([]byte{0x40}, 32), "alice",
		PublicKeyHostBoundMethod, user.blob, dest.blob)
	if _, err := agent.HandleSignRequest(sessDirect, buildSignRequest(t, user.blob, valid), cb); err != nil {
		t.Fatalf("valid direct request: %v", err)
	}

	// Direct, single-hop use with the ordinary publickey method is also
	// legitimate; it has no host key field but is bound to the one session.
	sessPlain := NewSession()
	mustBind(t, sessPlain, dest, bytes.Repeat([]byte{0x41}, 32), "alice", false)
	plain := buildSignatureData(t, bytes.Repeat([]byte{0x41}, 32), "alice",
		PublicKeyMethod, user.blob, nil)
	if _, err := agent.HandleSignRequest(sessPlain, buildSignRequest(t, user.blob, plain), cb); err != nil {
		t.Fatalf("valid direct publickey request: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("valid direct requests calls = %d, want 2", *calls)
	}
}

func TestSessionIdentifierMustMatchFinalBinding(t *testing.T) {
	bastion := newTestHost(t, "bastion")
	dest := newTestHost(t, "dest")
	user := newTestHost(t, "user-key")

	agent := NewAgent()
	if err := agent.RegisterKey(user.blob,
		Chain{{Hostname: "bastion", Username: "ops", HostKey: bastion.blob},
			{Hostname: "dest", Username: "alice", HostKey: dest.blob}},
	); err != nil {
		t.Fatalf("register key: %v", err)
	}

	cb, calls := newCountedSigner(t)
	sess := NewSession()
	mustBind(t, sess, bastion, bytes.Repeat([]byte{0x51}, 32), "ops", true)
	mustBind(t, sess, dest, bytes.Repeat([]byte{0x52}, 32), "alice", false)

	// Signed data refers to the bastion session instead of the final hop.
	data := buildSignatureData(t, bytes.Repeat([]byte{0x51}, 32), "alice",
		PublicKeyHostBoundMethod, user.blob, dest.blob)
	_, err := agent.HandleSignRequest(sess, buildSignRequest(t, user.blob, data), cb)
	if !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("session mismatch error = %v, want ErrSessionMismatch", err)
	}
	if *calls != 0 {
		t.Fatalf("session mismatch reached signer: calls = %d, want 0", *calls)
	}
}

func TestSessionBindMessageAndSignatureVerification(t *testing.T) {
	host := newTestHost(t, "hop")
	sessionID := bytes.Repeat([]byte{0x61}, 32)
	b := host.binding(sessionID, "ops", true)

	msg := []byte{AgentcExtension}
	msg = appendStringValue(msg, SessionBindExtension)
	msg = appendString(msg, b.HostKey)
	msg = appendString(msg, b.SessionID)
	msg = appendString(msg, b.Signature)
	msg = appendBool(msg, b.IsForwarding)
	framed := appendUint32(nil, uint32(len(msg)))
	framed = append(framed, msg...)

	sess := NewSession()
	if err := sess.BindMessage(framed); err != nil {
		t.Fatalf("bind message: %v", err)
	}
	if got := sess.Chain(); len(got) != 1 || !bytes.Equal(got[0].HostKey, host.blob) {
		t.Fatalf("unexpected chain: %#v", got)
	}

	// A forged signature must not create a binding.
	forged := host.binding(sessionID, "ops", true)
	forged.Signature = appendString(nil, []byte("ssh-ed25519"))
	forged.Signature = appendString(forged.Signature, bytes.Repeat([]byte{0xaa}, ed25519.SignatureSize))
	forged.SessionID = bytes.Repeat([]byte{0x62}, 32)
	if err := NewSession().Bind(forged); !errors.Is(err, ErrBindingSignature) {
		t.Fatalf("forged bind error = %v, want ErrBindingSignature", err)
	}
}

func TestParseSignRequestRejectsWrongMessageType(t *testing.T) {
	user := newTestHost(t, "user-key")
	raw := []byte{AgentcExtension}
	raw = appendString(raw, user.blob)
	raw = appendString(raw, appendString(nil, bytes.Repeat([]byte{0x70}, 32)))
	raw = appendUint32(raw, 0)
	if _, err := ParseSignRequest(raw); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("wrong sign request type error = %v, want ErrMalformedMessage", err)
	}
}
