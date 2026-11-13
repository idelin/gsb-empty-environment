package netsec

import (
	"bytes"
	"testing"
	"time"
)

func TestMemorySessionStoreLifecycle(t *testing.T) {
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	store := NewMemorySessionStore()
	store.now = func() time.Time { return base }

	session := &Session{
		ID:                   []byte("id-1"),
		MasterSecret:         bytes.Repeat([]byte{0x33}, MasterSecretLength),
		ExtendedMasterSecret: true,
		CreatedAt:            base,
		Lifetime:             time.Hour,
	}
	if err := store.Put(session); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get([]byte("id-1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ExtendedMasterSecret || !bytes.Equal(got.MasterSecret, session.MasterSecret) {
		t.Fatal("stored session did not round-trip")
	}

	got.MasterSecret[0] = 0xFF
	again, err := store.Get([]byte("id-1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.MasterSecret[0] == 0xFF {
		t.Fatal("Get must return a defensive copy")
	}

	if err := store.Delete([]byte("id-1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get([]byte("id-1")); err != ErrSessionUnknown {
		t.Fatalf("after delete err = %v, want %v", err, ErrSessionUnknown)
	}
}

func TestMemorySessionStoreExpiry(t *testing.T) {
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	now := base
	store := NewMemorySessionStore()
	store.now = func() time.Time { return now }

	session := &Session{
		ID:        []byte("expiring"),
		CreatedAt: base,
		Lifetime:  time.Minute,
	}
	if err := store.Put(session); err != nil {
		t.Fatalf("Put: %v", err)
	}

	now = base.Add(30 * time.Second)
	if _, err := store.Get([]byte("expiring")); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}

	now = base.Add(time.Minute + time.Second)
	if _, err := store.Get([]byte("expiring")); err != ErrSessionExpired {
		t.Fatalf("expired session err = %v, want %v", err, ErrSessionExpired)
	}
	if _, ok := store.sessions["expiring"]; ok {
		t.Fatal("expired session must be evicted on access")
	}
}

func TestMemorySessionStoreRejectsBadInput(t *testing.T) {
	store := NewMemorySessionStore()
	if err := store.Put(nil); err != ErrSessionUnknown {
		t.Fatalf("nil Put err = %v, want %v", err, ErrSessionUnknown)
	}
	if err := store.Put(&Session{}); err != ErrSessionUnknown {
		t.Fatalf("empty ID Put err = %v, want %v", err, ErrSessionUnknown)
	}
}

func TestMemoryClientSessionCache(t *testing.T) {
	cache := NewMemoryClientSessionCache()
	session := &ClientSession{
		Ticket:               []byte("ticket"),
		MasterSecret:         bytes.Repeat([]byte{0x66}, MasterSecretLength),
		ExtendedMasterSecret: true,
	}
	cache.Put("example.com:443", session)

	got, ok := cache.Get("example.com:443")
	if !ok || !got.ExtendedMasterSecret {
		t.Fatal("cached session did not round-trip")
	}
	got.MasterSecret[0] = 0xFF
	if cached, _ := cache.Get("example.com:443"); cached.MasterSecret[0] == 0xFF {
		t.Fatal("Get must return a defensive copy")
	}

	if _, ok := cache.Get("missing"); ok {
		t.Fatal("missing cache entry must report absent")
	}
	cache.Put("ignored", nil)
	if _, ok := cache.Get("ignored"); ok {
		t.Fatal("nil session must not be cached")
	}
}

func TestSessionValid(t *testing.T) {
	base := time.Now()
	if (&Session{}).Valid(base) {
		t.Fatal("zero-value session must be invalid")
	}
	session := &Session{CreatedAt: base, Lifetime: time.Second}
	if !session.Valid(base) {
		t.Fatal("session inside lifetime must be valid")
	}
	if session.Valid(base.Add(2 * time.Second)) {
		t.Fatal("session past lifetime must be invalid")
	}
	var nilSession *Session
	if nilSession.Valid(base) {
		t.Fatal("nil session must be invalid")
	}
}
