package netsec

import (
	"crypto/sha512"
	"errors"
	"testing"
)

func negotiate311(t *testing.T, cfg Config) (*Engine, []byte, []byte) {
	t.Helper()
	req := negotiateRequest(
		[]uint16{DialectSMB210, DialectSMB302, DialectSMB311},
		0,
		preauthContext(),
		encryptionContext([]uint16{CipherAES128GCM, CipherAES128CCM}),
	)
	resp := negotiateResponse()

	engine := NewEngine(cfg)
	if d := engine.HandleRequest(req); d.Action != ActionAllow {
		t.Fatalf("negotiate: action=%v reason=%v", d.Action, d.Reason)
	}
	if engine.Dialect() != DialectSMB311 {
		t.Fatalf("dialect = %#x, want 3.1.1", engine.Dialect())
	}
	if d := engine.ObserveNegotiateResponse(resp); d.Action != ActionAllow {
		t.Fatalf("observe response: action=%v reason=%v", d.Action, d.Reason)
	}
	return engine, req, resp
}

func expectedPreauthHash(req, resp []byte) []byte {
	h := make([]byte, preauthHashLen)
	sum := sha512.Sum512(append(h, req...))
	sum = sha512.Sum512(append(sum[:], resp...))
	return sum[:]
}

// --- Downgraded dialect -----------------------------------------------------

func TestDowngradeToSMB210RejectedWhenEncryptionRequired(t *testing.T) {
	engine := NewEngine(Config{
		RequireEncryption:  true,
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})

	// Attacker strips SMB 3.x dialects, leaving only SMB 2.1.
	req := negotiateRequest([]uint16{DialectSMB210}, 0)
	d := engine.HandleRequest(req)

	expectDecision(t, d, ActionDisconnect, ReasonEncryptedDialectUnsupported)
	if !engine.Closed() {
		t.Fatal("engine must be closed after a downgrade attempt")
	}
	if !errors.Is(d.Err, ErrEncryptedDialectUnsupported) {
		t.Fatalf("Err = %v, want ErrEncryptedDialectUnsupported", d.Err)
	}
}

func TestSMB210AllowedWhenEncryptionOptional(t *testing.T) {
	engine := NewEngine(Config{RequireEncryption: false})
	req := negotiateRequest([]uint16{DialectSMB210}, 0)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionAllow, ReasonOK)
	if engine.Dialect() != DialectSMB210 || engine.Cipher() != 0 {
		t.Fatalf("dialect/cipher = %#x/%#x", engine.Dialect(), engine.Cipher())
	}
}

func TestSMB302WithoutEncryptionCapabilityRejected(t *testing.T) {
	engine := NewEngine(Config{RequireEncryption: true})
	req := negotiateRequest([]uint16{DialectSMB302}, 0)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonNoEncryptionCipher)
}

func TestNoCommonDialectRejected(t *testing.T) {
	engine := NewEngine(Config{
		RequireEncryption: true,
		Dialects:          []uint16{DialectSMB311},
	})
	req := negotiateRequest([]uint16{DialectSMB210}, 0)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionReject, ReasonNoCommonDialect)
}

// --- Cipher list tampering --------------------------------------------------

func TestCipherNoIntersectionRejected(t *testing.T) {
	engine := NewEngine(Config{
		Ciphers:            []uint16{CipherAES128GCM},
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})

	// Attacker rewrites the cipher list to an algorithm the server rejects.
	req := negotiateRequest(
		[]uint16{DialectSMB311},
		0,
		preauthContext(),
		encryptionContext([]uint16{CipherAES256GCM}),
	)
	d := engine.HandleRequest(req)

	expectDecision(t, d, ActionReject, ReasonCipherNoIntersection)
	if !errors.Is(d.Err, ErrCipherNoIntersection) {
		t.Fatalf("Err = %v, want ErrCipherNoIntersection", d.Err)
	}
}

func TestEmptyCipherListRejected(t *testing.T) {
	engine := NewEngine(Config{})
	req := negotiateRequest(
		[]uint16{DialectSMB311},
		0,
		preauthContext(),
		encryptionContext(nil),
	)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonNoEncryptionCipher)
}

func TestServerPreferredCipherSelected(t *testing.T) {
	engine := NewEngine(Config{
		Ciphers:            []uint16{CipherAES128CCM, CipherAES128GCM},
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})
	req := negotiateRequest(
		[]uint16{DialectSMB311},
		0,
		preauthContext(),
		encryptionContext([]uint16{CipherAES128GCM, CipherAES128CCM}),
	)

	if d := engine.HandleRequest(req); d.Action != ActionAllow {
		t.Fatalf("negotiate: action=%v reason=%v", d.Action, d.Reason)
	}
	if engine.Cipher() != CipherAES128CCM {
		t.Fatalf("cipher = %#x, want server-preferred AES-128-CCM", engine.Cipher())
	}
}

func TestMissingPreauthContextRejected(t *testing.T) {
	engine := NewEngine(Config{})
	req := negotiateRequest(
		[]uint16{DialectSMB311},
		0,
		encryptionContext([]uint16{CipherAES128GCM}),
	)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonNoPreauthContext)
}

func TestOffered311WithoutContextsRejectedEvenWhenLowerDialectSelected(t *testing.T) {
	engine := NewEngine(Config{
		// Server prefers 3.0.2, but the client still advertises 3.1.1 and
		// must therefore include the 3.1.1 negotiate contexts.
		Dialects: []uint16{DialectSMB302, DialectSMB311},
	})
	req := negotiateRequest([]uint16{DialectSMB302, DialectSMB311}, 0)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonNoPreauthContext)
}

func TestSMB311WithoutSHA512Rejected(t *testing.T) {
	engine := NewEngine(Config{})

	// Pre-auth context that advertises an unknown hash id rather than SHA-512.
	ctx := negotiateContext(ctxPreauthIntegrity, []byte{0x01, 0x00, 0x00, 0x00, 0x09, 0x00})
	req := negotiateRequest(
		[]uint16{DialectSMB311},
		0,
		ctx,
		encryptionContext([]uint16{CipherAES128GCM}),
	)

	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonNoSHA512Preauth)
}

// --- Pre-auth integrity hash ------------------------------------------------

func TestPreauthHashCoversNegotiateRequestAndResponse(t *testing.T) {
	var seen []byte
	engine, req, resp := negotiate311(t, Config{
		VerifySessionSetup: acceptVerifier(&seen),
	})

	d := engine.HandleRequest(plainRequest(cmdSessionSetup))
	expectDecision(t, d, ActionAllow, ReasonOK)

	want := expectedPreauthHash(req, resp)
	if !bytesEqual(seen, want) {
		t.Fatalf("verifier hash = %x, want %x", seen, want)
	}
	if !bytesEqual(engine.PreauthHash(), want) {
		t.Fatalf("engine hash diverged from verifier hash")
	}
}

func TestSignatureMismatchDisconnects(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{
		VerifySessionSetup: rejectVerifier(),
	})

	d := engine.HandleRequest(plainRequest(cmdSessionSetup))
	expectDecision(t, d, ActionDisconnect, ReasonSignatureFailed)
	if !engine.Closed() {
		t.Fatal("engine must be closed after a signature failure")
	}
}

func TestMissingVerifierDisconnects(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{})

	d := engine.HandleRequest(plainRequest(cmdSessionSetup))
	expectDecision(t, d, ActionDisconnect, ReasonVerifierMissing)
}

func TestInitialPreauthHashIsSixtyFourZeroBytes(t *testing.T) {
	engine := NewEngine(Config{})
	hash := engine.PreauthHash()
	if len(hash) != preauthHashLen {
		t.Fatalf("hash length = %d, want %d", len(hash), preauthHashLen)
	}
	for i, b := range hash {
		if b != 0 {
			t.Fatalf("initial hash byte %d = %d, want 0", i, b)
		}
	}
}

// --- Unencrypted request after TREE_CONNECT ---------------------------------

func TestUnencryptedRequestAfterEncryptedTreeConnectDisconnects(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{
		RequireEncryption:  true,
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})

	if d := engine.HandleRequest(plainRequest(cmdSessionSetup)); d.Action != ActionAllow {
		t.Fatalf("session setup: %v/%v", d.Action, d.Reason)
	}

	treeReq := treeConnectRequest(`\\srv\secret`)
	if d := engine.HandleRequest(treeReq); d.Action != ActionAllow {
		t.Fatalf("tree connect: %v/%v", d.Action, d.Reason)
	}
	if !engine.EncryptionRequired() {
		t.Fatal("share should be marked encryption-required")
	}

	// Any later plaintext command (e.g. CREATE) on the encrypted share is fatal.
	d := engine.HandleRequest(plainRequest(0x0005))
	expectDecision(t, d, ActionDisconnect, ReasonUnencryptedAfterTreeConnect)
}

func TestEncryptedTransformAllowedAfterTreeConnect(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{
		RequireEncryption:  true,
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})
	engine.HandleRequest(plainRequest(cmdSessionSetup))
	engine.HandleRequest(treeConnectRequest(`\\srv\secret`))

	d := engine.HandleRequest(transformMessage())
	expectDecision(t, d, ActionAllow, ReasonOK)
}

func TestPerShareEncryptionPolicy(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{
		VerifySessionSetup: acceptVerifier(new([]byte)),
		ShareRequiresEncryption: func(path string) bool {
			return path == `\\srv\secret`
		},
	})
	engine.HandleRequest(plainRequest(cmdSessionSetup))

	publicTree := treeConnectRequest(`\\srv\public`)
	if d := engine.HandleRequest(publicTree); d.Action != ActionAllow {
		t.Fatalf("public tree connect: %v/%v", d.Action, d.Reason)
	}
	if engine.EncryptionRequired() {
		t.Fatal("public share must not require encryption")
	}
	if d := engine.HandleRequest(plainRequest(0x0005)); d.Action != ActionAllow {
		t.Fatalf("plaintext request on public share: %v/%v", d.Action, d.Reason)
	}
}

func TestEncryptedTreeConnectWithoutCipherRejected(t *testing.T) {
	// SMB 2.1 negotiated without encryption cannot bind an encrypted share.
	engine := NewEngine(Config{
		VerifySessionSetup:      acceptVerifier(new([]byte)),
		ShareRequiresEncryption: func(string) bool { return true },
	})
	req := negotiateRequest([]uint16{DialectSMB210}, 0)
	if d := engine.HandleRequest(req); d.Action != ActionAllow {
		t.Fatalf("negotiate: %v/%v", d.Action, d.Reason)
	}
	engine.HandleRequest(plainRequest(cmdSessionSetup))

	d := engine.HandleRequest(treeConnectRequest(`\\srv\secret`))
	expectDecision(t, d, ActionDisconnect, ReasonEncryptedDialectUnsupported)
}

// --- Duplicate NEGOTIATE and ordering ---------------------------------------

func TestSecondNegotiateDisconnects(t *testing.T) {
	engine, _, _ := negotiate311(t, Config{
		VerifySessionSetup: acceptVerifier(new([]byte)),
	})

	req := negotiateRequest([]uint16{DialectSMB311}, 0, preauthContext(),
		encryptionContext([]uint16{CipherAES128GCM}))
	d := engine.HandleRequest(req)
	expectDecision(t, d, ActionDisconnect, ReasonDuplicateNegotiate)
	if !engine.Closed() {
		t.Fatal("engine must be closed after a duplicate NEGOTIATE")
	}
}

func TestCommandBeforeNegotiateDisconnects(t *testing.T) {
	engine := NewEngine(Config{})
	d := engine.HandleRequest(plainRequest(0x0005))
	expectDecision(t, d, ActionDisconnect, ReasonUnexpectedCommand)
}

func TestUnknownProtocolDisconnects(t *testing.T) {
	engine := NewEngine(Config{})
	d := engine.HandleRequest([]byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x02})
	expectDecision(t, d, ActionDisconnect, ReasonUnknownProtocol)
}

func TestTransformWithoutCipherDisconnects(t *testing.T) {
	engine := NewEngine(Config{})
	d := engine.HandleRequest(transformMessage())
	expectDecision(t, d, ActionDisconnect, ReasonEncryptionNotNegotiated)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
