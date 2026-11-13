package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
)

func mustField(t *testing.T, p uint32) Field {
	t.Helper()
	f, err := NewField(p)
	if err != nil {
		t.Fatalf("NewField(%d): %v", p, err)
	}
	return f
}

func mustShares(t *testing.T, f Field, x uint32) AdditiveShares {
	t.Helper()
	s, err := f.SplitSecret(x, rand.Reader)
	if err != nil {
		t.Fatalf("SplitSecret(%d): %v", x, err)
	}
	return s
}

func mustTriple(t *testing.T, f Field, id string, a, b, c uint32) Triple {
	t.Helper()
	triple := Triple{
		ID: id,
		A:  mustShares(t, f, a),
		B:  mustShares(t, f, b),
		C:  mustShares(t, f, c),
	}
	if err := f.ValidateTriple(triple); err != nil {
		t.Fatalf("ValidateTriple: %v", err)
	}
	return triple
}

func mustHonestTriple(t *testing.T, provider TripleProvider, id string) Triple {
	t.Helper()
	tr, err := provider.Triple(id)
	if err != nil {
		t.Fatalf("provider.Triple(%q): %v", id, err)
	}
	return tr
}

func mustKeys(t *testing.T) [3]PartyKey {
	t.Helper()
	var keys [3]PartyKey
	for i := range keys {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		keys[i] = PartyKey{Private: priv, Public: pub}
	}
	return keys
}

func newTestHost(t *testing.T, p uint32, store Store, bus Bus, keys [3]PartyKey) *Host {
	t.Helper()
	h, err := NewHostWithConfig(HostConfig{P: p, Store: store, Bus: bus, Keys: keys})
	if err != nil {
		t.Fatalf("NewHostWithConfig: %v", err)
	}
	return h
}

func shareBytes(v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return buf[:]
}

func bytesShare(b []byte) uint32 {
	return binary.BigEndian.Uint32(b)
}

func mustResign(t *testing.T, key PartyKey, f Field, src SignedMessage, value []byte) SignedMessage {
	t.Helper()
	m, err := SignMessage(key.Private, f, src.Round, src.Pair, src.Sender, src.Label, value)
	if err != nil {
		t.Fatalf("SignMessage: %v", err)
	}
	return m
}

// testBus is an honest loopback with an optional per-message rewrite hook. A
// hook can return zero, one, or many replacement messages, which lets tests
// model drops, duplicates, forks, and signature corruption without reaching
// into the host.
type testBus struct {
	inner *LoopbackBus
	mu    sync.Mutex
	hooks map[string]func(SignedMessage) []SignedMessage
	fail  func(SignedMessage) error
}

func newTestBus() *testBus {
	return &testBus{inner: NewLoopbackBus(), hooks: make(map[string]func(SignedMessage) []SignedMessage)}
}

func (b *testBus) setHook(label string, sender int, hook func(SignedMessage) []SignedMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := fmt.Sprintf("%s/%d", label, sender)
	if hook == nil {
		delete(b.hooks, key)
		return
	}
	b.hooks[key] = hook
}

func (b *testBus) setFail(fail func(SignedMessage) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = fail
}

func (b *testBus) Publish(m SignedMessage) error {
	b.mu.Lock()
	hook := b.hooks[fmt.Sprintf("%s/%d", m.Label, m.Sender)]
	fail := b.fail
	b.mu.Unlock()
	if fail != nil {
		if err := fail(m); err != nil {
			return err
		}
	}
	if hook == nil {
		return b.inner.Publish(m)
	}
	replacement := hook(m)
	for _, out := range replacement {
		if err := b.inner.Publish(out); err != nil {
			return err
		}
	}
	return nil
}

func (b *testBus) Messages(round RoundID, pair TriplePair, label string) []SignedMessage {
	return b.inner.Messages(round, pair, label)
}

func (b *testBus) all() []SignedMessage {
	return b.inner.All()
}
