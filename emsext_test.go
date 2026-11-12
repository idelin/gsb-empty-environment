package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func fake48Secret() []byte {
	s := make([]byte, masterSecretLength)
	for i := range s {
		s[i] = byte(0xA0 + i%16)
	}
	return s
}

// recordingPRF wraps a real TLS 1.2 PRF and captures the last invocation so
// tests can assert the label and seed the package feeds into the injected
// PRF without having to re-derive key material themselves.
type recordingPRF struct {
	inner      PRF
	err        error
	lastSecret []byte
	lastLabel  string
	lastSeed   []byte
	lastLength int
	callCount  int
}

func (r *recordingPRF) prf(secret []byte, label string, seed []byte, length int) ([]byte, error) {
	r.callCount++
	r.lastSecret = append([]byte(nil), secret...)
	r.lastLabel = label
	r.lastSeed = append([]byte(nil), seed...)
	r.lastLength = length
	if r.err != nil {
		return nil, r.err
	}
	return r.inner(secret, label, seed, length)
}

// independentPRF is a deliberately separate, straight-from-the-spec
// implementation used to cross-check TLS12PRF.
func independentPRF(newHash func() hash.Hash, secret []byte, label string, seed []byte, n int) []byte {
	labeled := append([]byte(label), seed...)
	half := (len(secret) + 1) / 2
	s1, s2 := secret[:half], secret[len(secret)-half:]

	pHash := func(key []byte) []byte {
		out := make([]byte, 0, n)
		a := labeled
		for len(out) < n {
			m := hmac.New(newHash, key)
			m.Write(a)
			a = m.Sum(nil)
			m = hmac.New(newHash, key)
			m.Write(a)
			m.Write(labeled)
			out = append(out, m.Sum(nil)...)
		}
		return out[:n]
	}

	a, b := pHash(s1), pHash(s2)
	out := make([]byte, n)
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

type helloExt struct {
	typ  uint16
	data []byte
}

func extBlock(entries ...helloExt) []byte {
	var buf bytes.Buffer
	for _, e := range entries {
		var hdr [4]byte
		binary.BigEndian.PutUint16(hdr[0:2], e.typ)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(e.data)))
		buf.Write(hdr[:])
		buf.Write(e.data)
	}
	return buf.Bytes()
}

func emsEntry() helloExt { return helloExt{typ: ExtensionExtendedMasterSecret} }

func otherEntry() helloExt { return helloExt{typ: 0x000A, data: []byte{1, 2, 3}} }

// ---- RFC 7627 section 5.3: full 2x2 resumption admission matrix ----

func TestAdmitAllCombinations(t *testing.T) {
	emsRecord := &SessionRecord{Version: VersionTLS12, UsedEMS: true, MasterSecret: fake48Secret()}
	legacyRecord := &SessionRecord{Version: VersionTLS12, UsedEMS: false, MasterSecret: fake48Secret()}

	tests := []struct {
		name      string
		record    *SessionRecord
		offersEMS bool
		want      Decision
		wantErr   bool
	}{
		{
			name:      "ems session resumed with ems",
			record:    emsRecord,
			offersEMS: true,
			want:      Decision{Action: ActionResume, UseEMS: true},
		},
		{
			name:      "ems session resumed without ems must abort",
			record:    emsRecord,
			offersEMS: false,
			want:      Decision{Action: ActionAbort, UseEMS: true, Alert: AlertHandshakeFailure},
		},
		{
			name:      "legacy session resumed with ems forces full handshake",
			record:    legacyRecord,
			offersEMS: true,
			want:      Decision{Action: ActionFull, UseEMS: true},
		},
		{
			name:      "legacy session resumed without ems",
			record:    legacyRecord,
			offersEMS: false,
			want:      Decision{Action: ActionResume, UseEMS: false},
		},
		{
			name:      "no session no extension starts legacy full handshake",
			record:    nil,
			offersEMS: false,
			want:      Decision{Action: ActionFull, UseEMS: false},
		},
		{
			name:      "no session with extension starts ems full handshake",
			record:    nil,
			offersEMS: true,
			want:      Decision{Action: ActionFull, UseEMS: true},
		},
		{
			name:      "non tls 1.2 record rejected",
			record:    &SessionRecord{Version: 0x0302, UsedEMS: true, MasterSecret: fake48Secret()},
			offersEMS: true,
			wantErr:   true,
		},
		{
			name:      "corrupt secret length rejected",
			record:    &SessionRecord{Version: VersionTLS12, UsedEMS: true, MasterSecret: make([]byte, 16)},
			offersEMS: true,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Admit(tc.record, tc.offersEMS)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Admit = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ---- Session record persistence keeps the EMS flag ----

func TestSessionRecordRoundTrip(t *testing.T) {
	for _, usedEMS := range []bool{true, false} {
		rec := SessionRecord{Version: VersionTLS12, UsedEMS: usedEMS, MasterSecret: fake48Secret()}
		data, err := rec.Marshal()
		if err != nil {
			t.Fatalf("Marshal(usedEMS=%v): %v", usedEMS, err)
		}
		if len(data) != sessionRecordFixedSize {
			t.Fatalf("marshaled size = %d, want %d", len(data), sessionRecordFixedSize)
		}
		got, err := UnmarshalSessionRecord(data)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got.Version != rec.Version || got.UsedEMS != rec.UsedEMS ||
			!bytes.Equal(got.MasterSecret, rec.MasterSecret) {
			t.Fatalf("round trip mismatch: got %+v", got)
		}
		// Unmarshaled secret must be an independent copy.
		got.MasterSecret[0] ^= 0xFF
		if rec.MasterSecret[0] == got.MasterSecret[0] {
			t.Fatal("UnmarshalSessionRecord aliased the master secret buffer")
		}
	}
}

func TestSessionRecordRejectsBadInput(t *testing.T) {
	if _, err := (SessionRecord{Version: 0x0302, MasterSecret: fake48Secret()}).Marshal(); err == nil {
		t.Fatal("Marshal accepted non-TLS-1.2 record")
	}
	if _, err := (SessionRecord{Version: VersionTLS12, MasterSecret: make([]byte, 16)}).Marshal(); err == nil {
		t.Fatal("Marshal accepted short master secret")
	}

	good, err := (SessionRecord{Version: VersionTLS12, UsedEMS: true, MasterSecret: fake48Secret()}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := UnmarshalSessionRecord(good[:len(good)-1]); err == nil {
		t.Fatal("accepted truncated record")
	}
	badMagic := append([]byte(nil), good...)
	badMagic[0] = 'X'
	if _, err := UnmarshalSessionRecord(badMagic); err == nil {
		t.Fatal("accepted bad magic")
	}
	badFlag := append([]byte(nil), good...)
	badFlag[6] = 9
	if _, err := UnmarshalSessionRecord(badFlag); err == nil {
		t.Fatal("accepted malformed EMS flag")
	}
	badVersion := append([]byte(nil), good...)
	badVersion[4] = 0x03
	badVersion[5] = 0x02
	if _, err := UnmarshalSessionRecord(badVersion); err == nil {
		t.Fatal("accepted non-TLS-1.2 version")
	}
}

// ---- Master secret derivation: labels, seeds, and injected PRF ----

func TestDeriveMasterSecretPRFWiring(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	clientRandom := mustHex(t, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	serverRandom := mustHex(t, "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff000102030405060708090a0b0c0d0e0f")
	sessionHash := sha256.Sum256([]byte("handshake transcript through ServerHelloDone"))
	preMaster := mustHex(t, "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	t.Run("ems uses session hash seed and extended label", func(t *testing.T) {
		rec := &recordingPRF{inner: prf}
		out, err := DeriveMasterSecret(rec.prf, true, preMaster, clientRandom, serverRandom, sessionHash[:])
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != masterSecretLength {
			t.Fatalf("master secret length = %d", len(out))
		}
		if rec.lastLabel != labelExtendedMasterSec {
			t.Fatalf("label = %q, want %q", rec.lastLabel, labelExtendedMasterSec)
		}
		if !bytes.Equal(rec.lastSeed, sessionHash[:]) {
			t.Fatal("EMS seed must be the session hash only")
		}
		if rec.lastLength != masterSecretLength || !bytes.Equal(rec.lastSecret, preMaster) {
			t.Fatalf("PRF called with length=%d secret=%x", rec.lastLength, rec.lastSecret)
		}
		if bytes.Contains(rec.lastSeed, clientRandom) || bytes.Contains(rec.lastSeed, serverRandom) {
			t.Fatal("EMS seed unexpectedly contains hello randoms")
		}
	})

	t.Run("legacy uses randoms seed and classic label", func(t *testing.T) {
		rec := &recordingPRF{inner: prf}
		if _, err := DeriveMasterSecret(rec.prf, false, preMaster, clientRandom, serverRandom, nil); err != nil {
			t.Fatal(err)
		}
		if rec.lastLabel != labelMasterSecret {
			t.Fatalf("label = %q, want %q", rec.lastLabel, labelMasterSecret)
		}
		wantSeed := append(append([]byte(nil), clientRandom...), serverRandom...)
		if !bytes.Equal(rec.lastSeed, wantSeed) {
			t.Fatal("legacy seed must be clientRandom || serverRandom")
		}
	})

	t.Run("ems and legacy master secrets differ for the same connection", func(t *testing.T) {
		ems, err := DeriveMasterSecret(prf, true, preMaster, clientRandom, serverRandom, sessionHash[:])
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := DeriveMasterSecret(prf, false, preMaster, clientRandom, serverRandom, sessionHash[:])
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(ems, legacy) {
			t.Fatal("EMS and legacy derivations produced identical master secrets")
		}
	})
}

func TestDeriveMasterSecretValidationAndPRFErrors(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	hashBytes := mustHex(t, "ab")
	random := mustHex(t, "cd")
	preMaster := mustHex(t, "ee")

	if _, err := DeriveMasterSecret(nil, true, preMaster, random, random, hashBytes); err == nil {
		t.Fatal("expected error for nil PRF")
	}
	if _, err := DeriveMasterSecret(prf, true, nil, random, random, hashBytes); err == nil {
		t.Fatal("expected error for empty pre-master secret")
	}
	if _, err := DeriveMasterSecret(prf, true, preMaster, random, random, nil); err == nil {
		t.Fatal("EMS derivation without session hash must fail")
	}
	if _, err := DeriveMasterSecret(prf, false, preMaster, nil, random, nil); err == nil {
		t.Fatal("legacy derivation without client random must fail")
	}
	if _, err := DeriveMasterSecret(prf, false, preMaster, random, nil, nil); err == nil {
		t.Fatal("legacy derivation without server random must fail")
	}

	wantErr := errors.New("injected prf failure")
	rec := &recordingPRF{inner: prf, err: wantErr}
	if _, err := DeriveMasterSecret(rec.prf, true, preMaster, random, random, hashBytes); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want injected error", err)
	}
	if rec.callCount != 1 {
		t.Fatalf("PRF call count = %d, want 1", rec.callCount)
	}
}

// A tampered session hash must propagate all the way through the EMS
// derivation: different master secret and therefore different Finished
// verify_data. This is the transcript-binding property that defeats the
// triple handshake splice.
func TestTamperedSessionHashIsDetected(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	preMaster := mustHex(t, "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	clientRandom := mustHex(t, "1111111111111111111111111111111111111111111111111111111111111111")
	serverRandom := mustHex(t, "2222222222222222222222222222222222222222222222222222222222222222")

	transcriptA := []byte("legitimate handshake transcript seen by the peers")
	transcriptB := append([]byte(nil), transcriptA...)
	transcriptB[len(transcriptB)-1] = 'X' // attacker splices a different handshake

	hashA := sha256.Sum256(transcriptA)
	hashB := sha256.Sum256(transcriptB)
	if bytes.Equal(hashA[:], hashB[:]) {
		t.Fatal("test setup error: hashes collided")
	}

	masterA, err := DeriveMasterSecret(prf, true, preMaster, clientRandom, serverRandom, hashA[:])
	if err != nil {
		t.Fatal(err)
	}
	masterB, err := DeriveMasterSecret(prf, true, preMaster, clientRandom, serverRandom, hashB[:])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(masterA, masterB) {
		t.Fatal("EMS master secret did not change when the session hash was tampered")
	}

	finishedHash := sha256.Sum256(append(append([]byte(nil), transcriptA...), 0x99))
	verifyA, err := VerifyData(prf, masterA, finishedHash[:], true, 12)
	if err != nil {
		t.Fatal(err)
	}
	verifyB, err := VerifyData(prf, masterB, finishedHash[:], true, 12)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(verifyA, verifyB) {
		t.Fatal("Finished verify_data identical despite tampered session hash")
	}

	// Conversely, the legacy master secret is transcript-independent, which
	// demonstrates exactly why resumption consistency enforcement matters.
	legacyA, err := DeriveMasterSecret(prf, false, preMaster, clientRandom, serverRandom, hashA[:])
	if err != nil {
		t.Fatal(err)
	}
	legacyB, err := DeriveMasterSecret(prf, false, preMaster, clientRandom, serverRandom, hashB[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyA, legacyB) {
		t.Fatal("legacy master secret unexpectedly depends on the transcript")
	}
}

func TestVerifyDataLabelsAndValidation(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	secret := fake48Secret()
	finishedHash := mustHex(t, "010203")

	client, err := VerifyData(prf, secret, finishedHash, true, 12)
	if err != nil {
		t.Fatal(err)
	}
	server, err := VerifyData(prf, secret, finishedHash, false, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(client) != 12 || len(server) != 12 {
		t.Fatalf("verify_data lengths = %d/%d", len(client), len(server))
	}
	if bytes.Equal(client, server) {
		t.Fatal("client and server verify_data must use different labels")
	}
	wantClient := independentPRF(sha256.New, secret, labelClientFinished, finishedHash, 12)
	if !bytes.Equal(client, wantClient) {
		t.Fatal("client verify_data does not match the RFC 5246 computation")
	}

	if _, err := VerifyData(nil, secret, finishedHash, true, 12); err == nil {
		t.Fatal("nil PRF must fail")
	}
	if _, err := VerifyData(prf, secret[:16], finishedHash, true, 12); err == nil {
		t.Fatal("short master secret must fail")
	}
	if _, err := VerifyData(prf, secret, nil, true, 12); err == nil {
		t.Fatal("empty finished hash must fail")
	}
	if _, err := VerifyData(prf, secret, finishedHash, true, 0); err == nil {
		t.Fatal("non-positive length must fail")
	}
}

// Cross-check the exported TLS 1.2 PRF against an independent
// implementation across several output lengths and odd/even key lengths.
func TestTLS12PRFMatchesIndependentImplementation(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	cases := []struct {
		secret string
		label  string
		seed   string
		n      int
	}{
		{"01", "master secret", "aabbccdd", 48},
		{"010203", "extended master secret", "ccdd", 100}, // odd key length splits unevenly
		{
			"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
			"key expansion",
			"aabbccddeeff00112233445566778899",
			72,
		},
	}
	for i, tc := range cases {
		secret, seed := mustHex(t, tc.secret), mustHex(t, tc.seed)
		got, err := prf(secret, tc.label, seed, tc.n)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		want := independentPRF(sha256.New, secret, tc.label, seed, tc.n)
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d: PRF output mismatch", i)
		}
		if len(got) != tc.n {
			t.Fatalf("case %d: length = %d, want %d", i, len(got), tc.n)
		}
	}

	if _, err := prf(mustHex(t, "01"), "x", nil, 0); err == nil {
		t.Fatal("non-positive output length must fail")
	}
}

func TestExtensionParsing(t *testing.T) {
	t.Run("ems present alone", func(t *testing.T) {
		offered, err := ClientOffersEMS(extBlock(emsEntry()))
		if err != nil || !offered {
			t.Fatalf("offered=%v err=%v", offered, err)
		}
		ok, err := HasExtension([]Extension{{Type: ExtensionExtendedMasterSecret}})
		if err != nil || !ok {
			t.Fatalf("HasExtension ok=%v err=%v", ok, err)
		}
	})

	t.Run("ems present among other extensions", func(t *testing.T) {
		offered, err := ClientOffersEMS(extBlock(otherEntry(), emsEntry(), otherEntry()))
		if err != nil || !offered {
			t.Fatalf("offered=%v err=%v", offered, err)
		}
	})

	t.Run("ems absent", func(t *testing.T) {
		offered, err := ClientOffersEMS(extBlock(otherEntry()))
		if err != nil || offered {
			t.Fatalf("offered=%v err=%v", offered, err)
		}
		ok, err := HasExtension(nil)
		if err != nil || ok {
			t.Fatalf("HasExtension ok=%v err=%v", ok, err)
		}
	})

	t.Run("non-empty ems extension data is malformed", func(t *testing.T) {
		bad := extBlock(helloExt{typ: ExtensionExtendedMasterSecret, data: []byte{0}})
		if _, err := ClientOffersEMS(bad); err == nil {
			t.Fatal("expected malformed extension error")
		}
		if _, err := HasExtension([]Extension{{
			Type: ExtensionExtendedMasterSecret,
			Data: []byte{0},
		}}); err == nil {
			t.Fatal("expected malformed extension error")
		}
	})

	t.Run("truncated block is malformed", func(t *testing.T) {
		block := extBlock(otherEntry(), emsEntry())
		if _, err := ClientOffersEMS(block[:len(block)-1]); err == nil {
			t.Fatal("expected truncation error")
		}
		if _, err := ClientOffersEMS(block[:3]); err == nil {
			t.Fatal("expected truncation error")
		}
	})
}

// End-to-end exercise of a full handshake followed by every resumption
// path, including the attacker's downgrade attempt and the fallback case.
func TestFullHandshakeThenResumptionLifecycle(t *testing.T) {
	prf := TLS12PRF(sha256.New)
	clientRandom := bytes.Repeat([]byte{0x11}, 32)
	serverRandom := bytes.Repeat([]byte{0x22}, 32)
	preMaster := mustHex(t, "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")

	// 1. Original full handshake with EMS negotiated.
	decision, err := Admit(nil, true)
	if err != nil || decision.Action != ActionFull || !decision.UseEMS {
		t.Fatalf("fresh handshake decision = %+v, %v", decision, err)
	}
	sessionHash := sha256.Sum256([]byte("full handshake transcript"))
	master, err := DeriveMasterSecret(prf, decision.UseEMS, preMaster, clientRandom, serverRandom, sessionHash[:])
	if err != nil {
		t.Fatal(err)
	}
	record := SessionRecord{Version: VersionTLS12, UsedEMS: decision.UseEMS, MasterSecret: master}
	cached, err := record.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// 2. Honest resumption with EMS: abbreviated handshake reuses the secret.
	restored, err := UnmarshalSessionRecord(cached)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = Admit(&restored, true)
	if err != nil || decision.Action != ActionResume || !decision.UseEMS {
		t.Fatalf("honest resumption = %+v, %v", decision, err)
	}

	// 3. Attacker strips EMS on resumption: fatal abort, no downgrade.
	decision, err = Admit(&restored, false)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionAbort || decision.Alert != AlertHandshakeFailure {
		t.Fatalf("downgrade attempt = %+v, want abort/handshake_failure", decision)
	}

	// 4. A legacy session whose client later upgrades to EMS forces a new
	//    full handshake instead of resuming.
	legacyHash := sha256.Sum256([]byte("legacy full handshake transcript"))
	legacyMaster, err := DeriveMasterSecret(prf, false, preMaster, clientRandom, serverRandom, legacyHash[:])
	if err != nil {
		t.Fatal(err)
	}
	legacy := SessionRecord{Version: VersionTLS12, UsedEMS: false, MasterSecret: legacyMaster}
	decision, err = Admit(&legacy, true)
	if err != nil || decision.Action != ActionFull || !decision.UseEMS {
		t.Fatalf("upgrade path = %+v, %v", decision, err)
	}

	// 5. The upgraded full handshake derives an EMS master secret from the
	//    new session hash and it differs from the legacy one.
	upgradedHash := sha256.Sum256([]byte("upgraded full handshake transcript"))
	upgradedMaster, err := DeriveMasterSecret(prf, decision.UseEMS, preMaster, clientRandom, serverRandom, upgradedHash[:])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(upgradedMaster, legacyMaster) {
		t.Fatal("upgraded EMS master secret must differ from the legacy one")
	}
	newRecord := SessionRecord{Version: VersionTLS12, UsedEMS: true, MasterSecret: upgradedMaster}
	data, err := newRecord.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := UnmarshalSessionRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if !again.UsedEMS {
		t.Fatal("persisted record lost the EMS flag")
	}
	decision, err = Admit(&again, true)
	if err != nil || decision.Action != ActionResume {
		t.Fatalf("resumption of upgraded session = %+v, %v", decision, err)
	}
}

func TestActionStringer(t *testing.T) {
	cases := map[Action]string{
		ActionFull:   "full-handshake",
		ActionResume: "resume",
		ActionAbort:  "abort",
		Action(99):   "unknown-action",
	}
	for action, want := range cases {
		if got := action.String(); got != want {
			t.Fatalf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}
}

func TestEmptyExtensionBlock(t *testing.T) {
	offered, err := ClientOffersEMS(nil)
	if err != nil {
		t.Fatalf("empty extension block: %v", err)
	}
	if offered {
		t.Fatal("empty extension block cannot contain EMS")
	}
}
