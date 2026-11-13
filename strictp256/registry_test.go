package strictp256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
)

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func mustSig(t *testing.T, key *ecdsa.PrivateKey, msg []byte, high bool) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, key, DigestMessage(msg))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	n := p256Order()
	halfN := new(big.Int).Rsh(n, 1)
	if high {
		if s.Cmp(halfN) <= 0 {
			s.Sub(n, s)
		}
		if s.Cmp(halfN) <= 0 {
			t.Fatal("could not construct high-S signature")
		}
	} else {
		if s.Cmp(halfN) > 0 {
			s.Sub(n, s)
		}
	}
	return seq(derInt(r), derInt(s))
}

// TestVerificationFailureDoesNotOccupy confirms a bad signature never consumes
// a request ID: after a failed attempt the same ID still registers once.
func TestVerificationFailureDoesNotOccupy(t *testing.T) {
	key := mustKey(t)
	other := mustKey(t)
	msg := []byte("payment-1")
	forged := mustSig(t, other, msg, false)

	reg := NewRegistry[string]()
	var runs int32
	handler := func() (string, error) {
		atomic.AddInt32(&runs, 1)
		return "result-1", nil
	}

	if _, err := reg.Submit(&key.PublicKey, msg, forged, handler); !errors.Is(err, ErrVerification) {
		t.Fatalf("forged submit err = %v, want ErrVerification", err)
	}
	valid := mustSig(t, key, msg, false)
	res, err := reg.Submit(&key.PublicKey, msg, valid, handler)
	if err != nil || res != "result-1" {
		t.Fatalf("legit submit = (%q, %v)", res, err)
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("handler ran %d times, want 1 (failed attempt must not occupy)", got)
	}
}

// TestDuplicateReturnsOriginal verifies idempotent replay and that a handler
// error releases the slot for a later retry.
func TestDuplicateReturnsOriginal(t *testing.T) {
	key := mustKey(t)
	pub := &key.PublicKey
	msg := []byte("payment-2")
	sig := mustSig(t, key, msg, false)

	reg := NewRegistry[string]()
	var runs int32
	handler := func() (string, error) {
		atomic.AddInt32(&runs, 1)
		return "canonical-result", nil
	}
	for i := 0; i < 3; i++ {
		res, err := reg.Submit(pub, msg, sig, handler)
		if err != nil || res != "canonical-result" {
			t.Fatalf("submit %d = (%q, %v)", i, res, err)
		}
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("handler ran %d times, want 1", got)
	}

	failReg := NewRegistry[string]()
	boom := fmt.Errorf("downstream unavailable")
	var failCount int32
	failing := func() (string, error) {
		if atomic.AddInt32(&failCount, 1) == 1 {
			return "", boom
		}
		return "recovered", nil
	}
	if _, err := failReg.Submit(pub, msg, sig, failing); !errors.Is(err, boom) {
		t.Fatalf("first failing submit err = %v, want boom", err)
	}
	res, err := failReg.Submit(pub, msg, sig, failing)
	if err != nil || res != "recovered" {
		t.Fatalf("retry = (%q, %v), want recovered", res, err)
	}
}

// TestConcurrentDoubleSubmit proves atomic single registration under a race:
// many goroutines converge on one request ID and the handler runs exactly once.
func TestConcurrentDoubleSubmit(t *testing.T) {
	key := mustKey(t)
	pub := &key.PublicKey
	msg := []byte("payment-concurrent")
	lowSig := mustSig(t, key, msg, false)
	highSig := mustSig(t, key, msg, true)

	reg := NewRegistry[string]()
	if _, err := reg.Submit(pub, msg, highSig, func() (string, error) {
		t.Fatal("high-S submission must never reach handler")
		return "", nil
	}); !errors.Is(err, ErrHighS) {
		t.Fatalf("high-S err = %v, want ErrHighS", err)
	}

	var runs int32
	handler := func() (string, error) {
		atomic.AddInt32(&runs, 1)
		return "once", nil
	}
	const goroutines = 64
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	results := make(chan string, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			res, err := reg.Submit(pub, msg, lowSig, handler)
			errs <- err
			results <- res
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent submit err = %v", err)
		}
	}
	for res := range results {
		if res != "once" {
			t.Fatalf("concurrent result = %q, want once", res)
		}
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("handler ran %d times, want exactly 1", got)
	}
}

func appendBE64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}

func referenceID(pub *ecdsa.PublicKey, msg []byte) [RequestIDSize]byte {
	key, _ := CompressedPublicKey(pub)
	var buf []byte
	buf = append(buf, []byte(DomainLabel)...)
	buf = append(buf, key...)
	buf = appendBE64(buf, uint64(len(msg)))
	buf = append(buf, msg...)
	return sha256.Sum256(buf)
}
