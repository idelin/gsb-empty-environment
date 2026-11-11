package passkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"
)

const (
	testRPID   = "example.com"
	testOrigin = "https://example.com"
)

type fixture struct {
	svc       *Service
	priv      *ecdsa.PrivateKey
	credID    []byte
	userID    []byte
	challenge []byte
	pendingID string
	now       time.Time
}

func newFixture(t *testing.T, backup bool, signCount uint32) *fixture {
	t.Helper()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubRaw := elliptic.Marshal(priv.PublicKey.Curve, priv.PublicKey.X, priv.PublicKey.Y)

	credID := []byte("credential-handle-1")
	userID := []byte("user-1234")
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		t.Fatalf("challenge: %v", err)
	}

	chStore := NewChallengeStore()
	credStore := NewCredentialStore()
	sessStore := NewSessionStore()
	chStore.Now = func() time.Time { return now }

	pendingID := "challenge-1"
	chStore.Put(PendingChallenge{
		ID:        pendingID,
		Challenge: challenge,
		RPID:      testRPID,
		Origin:    testOrigin,
		UserID:    userID,
		ExpiresAt: now.Add(5 * time.Minute),
	})
	credStore.Put(Credential{
		ID:             credID,
		UserID:         userID,
		RPID:           testRPID,
		PublicKey:      pubRaw,
		SignCount:      signCount,
		BackupEligible: backup,
	})

	return &fixture{
		svc: &Service{
			RP:          RelyingParty{ID: testRPID, Origin: testOrigin},
			Challenges:  chStore,
			Credentials: credStore,
			Sessions:    sessStore,
			Now:         func() time.Time { return now },
		},
		priv:      priv,
		credID:    credID,
		userID:    userID,
		challenge: challenge,
		pendingID: pendingID,
		now:       now,
	}
}

type assertOption struct {
	backup          bool
	count           uint32
	damageSig       bool
	customChallenge []byte
	origin          string
	rpID            string
	credID          []byte
	clientType      string
}

// buildAssertion independently signs a fresh assertion on every call, so two
// concurrent requests carry distinct ECDSA signatures over identical content.
func (f *fixture) buildAssertion(t *testing.T, opt assertOption) AssertionInput {
	t.Helper()
	flags := byte(flagUP | flagUV)
	if opt.backup {
		flags |= flagBE | flagBS
	}
	authData := make([]byte, authDataMinLen)
	rpIDHash := sha256.Sum256([]byte(opt.rpID))
	copy(authData[:32], rpIDHash[:])
	authData[32] = flags
	binary.BigEndian.PutUint32(authData[33:37], opt.count)

	ch := opt.customChallenge
	if ch == nil {
		ch = f.challenge
	}
	cd := clientData{
		Type:      opt.clientType,
		Challenge: base64.RawURLEncoding.EncodeToString(ch),
		Origin:    opt.origin,
	}
	if cd.Type == "" {
		cd.Type = "webauthn.get"
	}
	clientJSON, err := json.Marshal(cd)
	if err != nil {
		t.Fatalf("client data: %v", err)
	}

	clientHash := sha256.Sum256(clientJSON)
	signed := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(signed)

	r, s, err := ecdsa.Sign(rand.Reader, f.priv, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig := make([]byte, es256SigLen)
	rb := r.Bytes()
	sb := s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)
	if opt.damageSig {
		sig[0] ^= 0xFF
	}

	id := opt.credID
	if id == nil {
		id = f.credID
	}
	return AssertionInput{
		CredentialID:      base64.StdEncoding.EncodeToString(id),
		ChallengeID:       f.pendingID,
		AuthenticatorData: base64.StdEncoding.EncodeToString(authData),
		ClientDataJSON:    base64.StdEncoding.EncodeToString(clientJSON),
		Signature:         base64.StdEncoding.EncodeToString(sig),
	}
}

func (f *fixture) defaultOpt(backup bool, count uint32) assertOption {
	return assertOption{backup: backup, count: count, origin: testOrigin, rpID: testRPID}
}
