package taskpkg

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
)

var testQuestion = Question{Name: "example.com", Type: 1, Class: 1}

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func newSession(t *testing.T, pub ed25519.PublicKey) *Session {
	t.Helper()
	s, err := NewSession(testQuestion, "ns1.trusted.example", pub)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func sign(s *Session, priv ed25519.PrivateKey, answer Answer) []byte {
	return ed25519.Sign(priv, s.SigningPayload(answer))
}

func TestValidSignatureDelivered(t *testing.T) {
	pub, priv := newKeyPair(t)
	s := newSession(t, pub)
	answer := Answer{Records: []string{"192.0.2.1", "192.0.2.2"}}

	res, published, err := s.Submit(TransportTCP, answer, sign(s, priv, answer))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !published {
		t.Fatal("valid response was not published")
	}
	if res.Transport != TransportTCP {
		t.Fatalf("result source = %q, want %q", res.Transport, TransportTCP)
	}
	if len(res.Answer.Records) != 2 || res.Answer.Records[0] != "192.0.2.1" {
		t.Fatalf("unexpected answer records: %v", res.Answer.Records)
	}
	got, ok := s.Result()
	if !ok || got.Transport != TransportTCP {
		t.Fatalf("Result() = %+v, %v", got, ok)
	}
}

func TestSubstitutedQuestionRejected(t *testing.T) {
	pub, priv := newKeyPair(t)
	s := newSession(t, pub)
	answer := Answer{Records: []string{"198.51.100.7"}}

	// Attacker holds a signature the upstream made for a *different* question
	// and tries to spend it on this round.
	other, err := NewSession(Question{Name: "attacker.example", Type: 1, Class: 1}, "ns1.trusted.example", pub)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	forged := ed25519.Sign(priv, other.SigningPayload(answer))

	if _, _, err := s.Submit(TransportUDP, answer, forged); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit with substituted-question signature err = %v, want %v", err, ErrBadSignature)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("forged response must not be delivered")
	}
}

func TestOldChallengeReplayRejected(t *testing.T) {
	pub, priv := newKeyPair(t)
	answer := Answer{Records: []string{"203.0.113.9"}}

	// Previous round: upstream legitimately signed the answer.
	old := newSession(t, pub)
	replayed := sign(old, priv, answer)

	// New round for the same question has a fresh challenge; the old
	// signature must not authenticate anything here.
	current := newSession(t, pub)
	if _, _, err := current.Submit(TransportTCP, answer, replayed); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit with replayed challenge err = %v, want %v", err, ErrBadSignature)
	}
	if _, ok := current.Result(); ok {
		t.Fatal("replayed response must not be delivered")
	}
}

func TestTruncationThenFakeAnswer(t *testing.T) {
	pub, priv := newKeyPair(t)
	s := newSession(t, pub)

	// UDP response arrives truncated: no trusted data, only a TCP retry hint.
	if !s.NoteTruncated(TransportUDP) {
		t.Fatal("truncated UDP response should advise a TCP retry")
	}
	if !s.UDPTruncated() {
		t.Fatal("truncation was not recorded")
	}
	if _, ok := s.Result(); ok {
		t.Fatal("truncation must not produce a deliverable result")
	}

	// Attacker races in with a fake UDP answer before the TCP query finishes.
	fake := Answer{Records: []string{"6.6.6.6"}}
	if _, _, err := s.Submit(TransportUDP, fake, []byte("not-a-signature")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Submit fake answer err = %v, want %v", err, ErrBadSignature)
	}
	if _, ok := s.Result(); ok {
		t.Fatal("fake UDP answer must not end the query")
	}

	// The genuine TCP response still completes the round.
	real := Answer{Records: []string{"192.0.2.55"}}
	res, published, err := s.Submit(TransportTCP, real, sign(s, priv, real))
	if err != nil || !published {
		t.Fatalf("Submit real TCP answer published=%v err=%v", published, err)
	}
	if res.Transport != TransportTCP || res.Answer.Records[0] != "192.0.2.55" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestRaceSingleDelivery(t *testing.T) {
	pub, priv := newKeyPair(t)
	s := newSession(t, pub)
	udpAnswer := Answer{Records: []string{"192.0.2.10"}}
	tcpAnswer := Answer{Records: []string{"192.0.2.20"}}
	udpSig := sign(s, priv, udpAnswer)
	tcpSig := sign(s, priv, tcpAnswer)

	const attempts = 32
	var publishedCount int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		transport, answer, sig := TransportUDP, udpAnswer, udpSig
		if i%2 == 1 {
			transport, answer, sig = TransportTCP, tcpAnswer, tcpSig
		}
		wg.Add(1)
		go func(tr Transport, ans Answer, sg []byte) {
			defer wg.Done()
			if _, published, err := s.Submit(tr, ans, sg); err == nil && published {
				mu.Lock()
				publishedCount++
				mu.Unlock()
			}
		}(transport, answer, sig)
	}
	wg.Wait()

	if publishedCount != 1 {
		t.Fatalf("published %d results, want exactly 1", publishedCount)
	}
	res, ok := s.Result()
	if !ok {
		t.Fatal("no result published")
	}
	// The published result is immutable: a later valid response on the other
	// transport is accepted as authentic but never replaces it.
	other, otherSig := tcpAnswer, tcpSig
	otherTransport := TransportTCP
	if res.Transport == TransportTCP {
		other, otherSig, otherTransport = udpAnswer, udpSig, TransportUDP
	}
	again, published, err := s.Submit(otherTransport, other, otherSig)
	if err != nil {
		t.Fatalf("Submit after delivery: %v", err)
	}
	if published {
		t.Fatal("second valid response must not be published")
	}
	if again.Transport != res.Transport {
		t.Fatalf("result source changed from %q to %q", res.Transport, again.Transport)
	}
	final, _ := s.Result()
	if final.Transport != res.Transport || final.Answer.Records[0] != res.Answer.Records[0] {
		t.Fatalf("result mutated after delivery: %+v -> %+v", res, final)
	}
}

func TestTruncationAfterDeliveryDoesNotAdviseRetry(t *testing.T) {
	pub, priv := newKeyPair(t)
	s := newSession(t, pub)
	answer := Answer{Records: []string{"192.0.2.99"}}
	if _, _, err := s.Submit(TransportTCP, answer, sign(s, priv, answer)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if s.NoteTruncated(TransportUDP) {
		t.Fatal("truncation after delivery must not advise another retry")
	}
}
