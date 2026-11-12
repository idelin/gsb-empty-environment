package netsec_test

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	chap "example.org/netsec/q0351"
)

const (
	initiatorSecret = "initiator-forward-secret"
	targetSecret    = "target-reverse-secret"
)

func newAuthenticator(t *testing.T, length int) *chap.Authenticator {
	t.Helper()

	auth, err := chap.NewAuthenticator(chap.Config{
		InitiatorSecret: initiatorSecret,
		TargetSecret:    targetSecret,
		ChallengeLength: length,
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return auth
}

func beginVerifiedSession(t *testing.T, auth *chap.Authenticator) (*chap.Session, chap.Challenge) {
	t.Helper()

	session := auth.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	challenge, err := session.ChallengeInitiator()
	if err != nil {
		t.Fatalf("ChallengeInitiator: %v", err)
	}
	response := chap.ResponseDigest(challenge.Identifier, []byte(initiatorSecret), challenge.Data)
	if err := session.VerifyInitiatorResponse(challenge.Identifier, chap.EncodeHex(response)); err != nil {
		t.Fatalf("VerifyInitiatorResponse: %v", err)
	}
	return session, challenge
}

func TestMutualCHAPWithBothResponseEncodings(t *testing.T) {
	auth := newAuthenticator(t, 32)

	tests := []struct {
		name   string
		encode func([]byte) string
	}{
		{name: "hex", encode: chap.EncodeHex},
		{name: "base64", encode: chap.EncodeBase64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session := auth.NewSession()
			defer session.Close()

			challenge, err := session.ChallengeInitiator()
			if err != nil {
				t.Fatalf("ChallengeInitiator: %v", err)
			}
			if len(challenge.Data) < chap.MinChallengeLength || len(challenge.Data) > chap.MaxChallengeLength {
				t.Fatalf("challenge length = %d", len(challenge.Data))
			}

			initiatorResponse := chap.ResponseDigest(
				challenge.Identifier,
				[]byte(initiatorSecret),
				challenge.Data,
			)
			if err := session.VerifyInitiatorResponse(
				challenge.Identifier,
				tc.encode(initiatorResponse),
			); err != nil {
				t.Fatalf("VerifyInitiatorResponse: %v", err)
			}

			reverseChallenge := bytes.Repeat([]byte{0x5a}, 48)
			reverseIdentifier := byte(0xa7)
			reply, err := session.HandleInitiatorChallenge(reverseIdentifier, reverseChallenge)
			if err != nil {
				t.Fatalf("HandleInitiatorChallenge: %v", err)
			}

			want := chap.ResponseDigest(reverseIdentifier, []byte(targetSecret), reverseChallenge)
			if !bytes.Equal(reply.Data, want) {
				t.Fatalf("target response = %x, want %x", reply.Data, want)
			}
			if reply.Identifier != reverseIdentifier {
				t.Fatalf("response identifier = %#x", reply.Identifier)
			}
			if got := tc.encode(reply.Data); got == "" {
				t.Fatal("encoded response is empty")
			}
		})
	}
}

func TestResponseDigestFormula(t *testing.T) {
	identifier := byte(0x42)
	secret := []byte("secret")
	challenge := []byte("challenge")

	h := md5.New()
	_, _ = h.Write([]byte{identifier})
	_, _ = h.Write(secret)
	_, _ = h.Write(challenge)
	want := h.Sum(nil)

	if got := chap.ResponseDigest(identifier, secret, challenge); !bytes.Equal(got, want) {
		t.Fatalf("ResponseDigest = %x, want %x", got, want)
	}
}

func TestSameConnectionChallengeReflectionRejected(t *testing.T) {
	auth := newAuthenticator(t, 32)
	session, challenge := beginVerifiedSession(t, auth)

	_, err := session.HandleInitiatorChallenge(challenge.Identifier, challenge.Bytes())
	if !errors.Is(err, chap.ErrReflectedChallenge) {
		t.Fatalf("error = %v, want ErrReflectedChallenge", err)
	}
}

func TestCrossConnectionChallengeReflectionRejected(t *testing.T) {
	auth := newAuthenticator(t, 32)
	sessionA, challengeA := beginVerifiedSession(t, auth)
	sessionB, challengeB := beginVerifiedSession(t, auth)

	if bytes.Equal(challengeA.Data, challengeB.Data) {
		t.Fatal("two independently generated challenges are equal")
	}

	_, err := sessionB.HandleInitiatorChallenge(challengeA.Identifier, challengeA.Bytes())
	if !errors.Is(err, chap.ErrReflectedChallenge) {
		t.Fatalf("cross-connection reflection error = %v, want ErrReflectedChallenge", err)
	}

	reverseChallenge := bytes.Repeat([]byte{0xc3}, 40)
	reply, err := sessionB.HandleInitiatorChallenge(challengeB.Identifier, reverseChallenge)
	if err != nil {
		t.Fatalf("session B could not answer an independent challenge: %v", err)
	}
	want := chap.ResponseDigest(challengeB.Identifier, []byte(targetSecret), reverseChallenge)
	if !bytes.Equal(reply.Data, want) {
		t.Fatalf("session B response = %x, want %x", reply.Data, want)
	}

	if err := sessionA.Close(); err != nil {
		t.Fatalf("Close session A: %v", err)
	}

	sessionC, _ := beginVerifiedSession(t, auth)
	_, err = sessionC.HandleInitiatorChallenge(challengeA.Identifier, challengeA.Bytes())
	if err != nil {
		t.Fatalf("challenge registration was not released: %v", err)
	}
}

func TestChallengeLengthBounds(t *testing.T) {
	for _, length := range []int{chap.MinChallengeLength, chap.MaxChallengeLength} {
		auth := newAuthenticator(t, length)
		session := auth.NewSession()
		challenge, err := session.ChallengeInitiator()
		if err != nil {
			t.Fatalf("length %d: ChallengeInitiator: %v", length, err)
		}
		if len(challenge.Data) != length {
			t.Fatalf("length %d: got challenge length %d", length, len(challenge.Data))
		}
		if err := session.Close(); err != nil {
			t.Fatalf("length %d: Close: %v", length, err)
		}
	}

	for _, length := range []int{chap.MinChallengeLength - 1, chap.MaxChallengeLength + 1} {
		if _, err := chap.NewAuthenticator(chap.Config{
			InitiatorSecret: initiatorSecret,
			TargetSecret:    targetSecret,
			ChallengeLength: length,
		}); !errors.Is(err, chap.ErrInvalidChallengeLength) {
			t.Fatalf("configured length %d: error = %v, want ErrInvalidChallengeLength", length, err)
		}
	}

	auth := newAuthenticator(t, 0)
	session := auth.NewSession()
	challenge, err := session.ChallengeInitiator()
	if err != nil {
		t.Fatalf("default length: ChallengeInitiator: %v", err)
	}
	if len(challenge.Data) != chap.DefaultChallengeLength {
		t.Fatalf("default length = %d, want %d", len(challenge.Data), chap.DefaultChallengeLength)
	}
	_ = session.Close()
}

func TestShortReverseChallengeRejected(t *testing.T) {
	auth := newAuthenticator(t, 32)
	session, _ := beginVerifiedSession(t, auth)

	for _, length := range []int{chap.MinChallengeLength - 1, chap.MaxChallengeLength + 1} {
		_, err := session.HandleInitiatorChallenge(7, bytes.Repeat([]byte{1}, length))
		if !errors.Is(err, chap.ErrInvalidChallengeLength) {
			t.Fatalf("challenge length %d: error = %v, want ErrInvalidChallengeLength", length, err)
		}
	}
}

func TestEncodedReverseChallengeAndReflection(t *testing.T) {
	auth := newAuthenticator(t, 32)
	_, challengeA := beginVerifiedSession(t, auth)
	sessionB, _ := beginVerifiedSession(t, auth)

	encoders := []struct {
		name   string
		encode func([]byte) string
	}{
		{name: "hex", encode: chap.EncodeHex},
		{name: "base64", encode: chap.EncodeBase64},
	}
	reverseChallenge := bytes.Repeat([]byte{0x6d}, 36)

	for _, tc := range encoders {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sessionB.HandleEncodedInitiatorChallenge(
				challengeA.Identifier,
				tc.encode(challengeA.Data),
			)
			if !errors.Is(err, chap.ErrReflectedChallenge) {
				t.Fatalf("encoded reflection error = %v, want ErrReflectedChallenge", err)
			}
		})
	}

	reply, err := sessionB.HandleEncodedInitiatorChallenge(0x19, chap.EncodeHex(reverseChallenge))
	if err != nil {
		t.Fatalf("HandleEncodedInitiatorChallenge: %v", err)
	}
	want := chap.ResponseDigest(0x19, []byte(targetSecret), reverseChallenge)
	if !bytes.Equal(reply.Data, want) {
		t.Fatalf("encoded reverse response = %x, want %x", reply.Data, want)
	}
}

func TestIdenticalSecretsRejected(t *testing.T) {
	_, err := chap.NewAuthenticator(chap.Config{
		InitiatorSecret: "same",
		TargetSecret:    "same",
	})
	if !errors.Is(err, chap.ErrSameSecrets) {
		t.Fatalf("error = %v, want ErrSameSecrets", err)
	}

	_, err = chap.NewAuthenticator(chap.Config{InitiatorSecret: "only-one"})
	if !errors.Is(err, chap.ErrEmptySecret) {
		t.Fatalf("empty secret error = %v, want ErrEmptySecret", err)
	}
}

func TestWrongResponseEncodingRejected(t *testing.T) {
	auth := newAuthenticator(t, 32)
	session := auth.NewSession()
	defer session.Close()

	challenge, err := session.ChallengeInitiator()
	if err != nil {
		t.Fatalf("ChallengeInitiator: %v", err)
	}

	for _, encoded := range []string{"", "not base64!", "0xzz", "0x", "AAAA\nAAAA"} {
		err := session.VerifyInitiatorResponse(challenge.Identifier, encoded)
		if !errors.Is(err, chap.ErrInvalidEncoding) {
			t.Fatalf("encoding %q: error = %v, want ErrInvalidEncoding", encoded, err)
		}
	}

	shortBase64 := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	err = session.VerifyInitiatorResponse(challenge.Identifier, shortBase64)
	if !errors.Is(err, chap.ErrInvalidResponseLength) {
		t.Fatalf("short response: error = %v, want ErrInvalidResponseLength", err)
	}
}

func TestWrongResponseAndIdentifierRejected(t *testing.T) {
	auth := newAuthenticator(t, 32)
	session := auth.NewSession()
	defer session.Close()

	challenge, err := session.ChallengeInitiator()
	if err != nil {
		t.Fatalf("ChallengeInitiator: %v", err)
	}

	err = session.VerifyInitiatorResponse(challenge.Identifier, chap.EncodeHex(bytes.Repeat([]byte{0}, md5.Size)))
	if !errors.Is(err, chap.ErrInvalidResponse) {
		t.Fatalf("wrong response: error = %v, want ErrInvalidResponse", err)
	}

	valid := chap.ResponseDigest(challenge.Identifier, []byte(initiatorSecret), challenge.Data)
	err = session.VerifyInitiatorResponse(challenge.Identifier+1, chap.EncodeBase64(valid))
	if !errors.Is(err, chap.ErrIdentifierMismatch) {
		t.Fatalf("wrong identifier: error = %v, want ErrIdentifierMismatch", err)
	}
}

func TestConcurrentChallengesAreUnique(t *testing.T) {
	auth := newAuthenticator(t, chap.MinChallengeLength)
	const sessions = 256

	challenges := make(chan chap.Challenge, sessions)
	var wg sync.WaitGroup
	wg.Add(sessions)
	for i := 0; i < sessions; i++ {
		go func() {
			defer wg.Done()
			session := auth.NewSession()
			defer session.Close()
			challenge, err := session.ChallengeInitiator()
			if err != nil {
				t.Errorf("ChallengeInitiator: %v", err)
				return
			}
			challenges <- challenge
		}()
	}
	wg.Wait()
	close(challenges)

	seen := make(map[string]struct{}, sessions)
	for challenge := range challenges {
		key := string(challenge.Data)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate challenge generated: %x", challenge.Data)
		}
		seen[key] = struct{}{}
	}
}
