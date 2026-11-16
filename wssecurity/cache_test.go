package wssecurity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0355/wssecurity"
)

func TestReplayAcrossValidatorsSharedCache(t *testing.T) {
	cache := wssecurity.NewMemoryNonceCache(wssecurity.DefaultNonceRetention)
	verifier := fullSignedVerifier()

	newValidator := func() *wssecurity.Validator {
		v, err := wssecurity.New(wssecurity.Config{
			VerifySignature: verifier.verify,
			NonceCache:      cache,
			Now:             func() time.Time { return fixedNow },
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return v
	}

	msg := validEnvelope("shared-cache-nonce")
	if err := newValidator().Validate(context.Background(), strings.NewReader(msg)); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}
	err := newValidator().Validate(context.Background(), strings.NewReader(msg))
	if !errors.Is(err, wssecurity.ErrReplayedNonce) {
		t.Fatalf("replay expected ErrReplayedNonce, got %v", err)
	}
}

func TestSameNonceDifferentCreatedAllowed(t *testing.T) {
	validator := newValidator(t, fullSignedVerifier(), nil)

	first := validEnvelope("same-nonce")
	if err := validator.Validate(context.Background(), strings.NewReader(first)); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}

	created := fixedNow.Add(-4 * time.Second)
	second := formatEnvelope(rfc3339(created), rfc3339(created.Add(100*time.Second)), "same-nonce")
	if err := validator.Validate(context.Background(), strings.NewReader(second)); err != nil {
		t.Fatalf("different Nonce/Created pair should pass, got %v", err)
	}
}

func TestMemoryNonceCacheExpiry(t *testing.T) {
	cache := wssecurity.NewMemoryNonceCache(time.Hour)
	key := "nonce|created"

	base := time.Now()
	first, err := cache.CheckAndStore(context.Background(), key, base, 40*time.Millisecond)
	if err != nil || !first {
		t.Fatalf("first CheckAndStore = %v, %v; want true, nil", first, err)
	}

	replay, err := cache.CheckAndStore(context.Background(), key, base, 40*time.Millisecond)
	if err != nil || replay {
		t.Fatalf("replay CheckAndStore = %v, %v; want false, nil", replay, err)
	}

	time.Sleep(50 * time.Millisecond)

	afterExpiry, err := cache.CheckAndStore(context.Background(), key, time.Now(), 40*time.Millisecond)
	if err != nil || !afterExpiry {
		t.Fatalf("post-expiry CheckAndStore = %v, %v; want true, nil", afterExpiry, err)
	}
}

func TestMemoryNonceCacheConcurrentAtMostOneWinner(t *testing.T) {
	cache := wssecurity.NewMemoryNonceCache(time.Hour)
	const goroutines = 64

	var wg sync.WaitGroup
	results := make(chan bool, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			first, err := cache.CheckAndStore(context.Background(), "race-key", fixedNow, time.Hour)
			if err != nil {
				t.Errorf("CheckAndStore: %v", err)
				return
			}
			results <- first
		}()
	}
	wg.Wait()
	close(results)

	winners := 0
	for first := range results {
		if first {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one first-use winner, got %d", winners)
	}
}

func TestNewRequiresSignatureVerifier(t *testing.T) {
	_, err := wssecurity.New(wssecurity.Config{})
	if !errors.Is(err, wssecurity.ErrSignatureVerify) {
		t.Fatalf("expected ErrSignatureVerify, got %v", err)
	}
}

func TestSignedReferenceNormalization(t *testing.T) {
	verifier := &fakeVerifier{refs: []string{" TS-1 ", "BODY-1"}, ok: true}
	validator := newValidator(t, verifier, nil)

	if err := validator.Validate(context.Background(), strings.NewReader(validEnvelope("nonce-normalize"))); err != nil {
		t.Fatalf("bare/whitespace references should be accepted, got %v", err)
	}
}

func TestSwappedTimestampAttackRejected(t *testing.T) {
	// Attacker removes the signed TS-1 and inserts a fresh TS-2 that is not
	// part of the signature. The structural two-timestamp rule already rejects
	// any message carrying both; this case keeps only the fresh, unsigned one.
	created := fixedNow.Add(-2 * time.Second)
	attackerTimestamp := `<wsu:Timestamp wsu:Id="TS-2">` +
		"<wsu:Created>" + rfc3339(created) + "</wsu:Created>" +
		"<wsu:Expires>" + rfc3339(created.Add(60*time.Second)) + "</wsu:Expires>" +
		`</wsu:Timestamp>`

	msg := strings.Replace(validEnvelope("nonce-swap-attack"),
		`<wsu:Timestamp wsu:Id="TS-1">
        <wsu:Created>`+rfc3339(fixedNow.Add(-5*time.Second))+`</wsu:Created>
        <wsu:Expires>`+rfc3339(fixedNow.Add(115*time.Second))+`</wsu:Expires>
      </wsu:Timestamp>`,
		attackerTimestamp, 1)

	// Verifier still only vouches for the original signed references.
	verifier := fullSignedVerifier()
	validator := newValidator(t, verifier, nil)

	err := validator.Validate(context.Background(), strings.NewReader(msg))
	if !errors.Is(err, wssecurity.ErrUnsignedTimestamp) {
		t.Fatalf("expected ErrUnsignedTimestamp for swapped timestamp, got %v", err)
	}
}
