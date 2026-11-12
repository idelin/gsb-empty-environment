package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"sync"
)

// ErrVersionConflict is returned by Store.CompareAndSwapVersion when the
// stored record version no longer matches the expected version.
var ErrVersionConflict = errors.New("netsec: record version conflict")

// ErrNoSuchUser is returned by Store implementations when the user is
// unknown.
var ErrNoSuchUser = errors.New("netsec: no such user")

// Record is a stored password record. Algorithm, Salt and Cost are pinned by
// the record itself, so old records keep verifying with their original
// parameters even after the service raises its target derivation cost.
type Record struct {
	Version   uint64
	Algorithm string
	Salt      []byte
	Cost      int
	Derived   []byte
}

// Verifier derives a key from a password. It is injected into Upgrader so
// hosts can plug in their own KDF (PBKDF2, argon2id, scrypt, ...).
type Verifier interface {
	Derive(algorithm string, password, salt []byte, cost int) ([]byte, error)
}

// Store persists password records.
type Store interface {
	Load(user string) (Record, error)
	// CompareAndSwapVersion stores next only when the current record for
	// user still has version expectVersion; otherwise it returns
	// ErrVersionConflict.
	CompareAndSwapVersion(user string, expectVersion uint64, next Record) error
}

// Upgrader verifies logins against stored records and opportunistically
// migrates them to the configured target algorithm and cost.
type Upgrader struct {
	Store    Store
	Verifier Verifier

	Algorithm string // target algorithm for migrated records
	Cost      int    // target derivation cost for migrated records
	SaltSize  int    // bytes of fresh salt generated for migrated records
}

// Login verifies password against the stored record. On success it attempts
// a cost upgrade; the upgrade never changes the login decision: any
// migration error (including a version conflict from a concurrent password
// change) is swallowed and the login still reports success.
func (u *Upgrader) Login(user string, password []byte) (bool, error) {
	rec, err := u.Store.Load(user)
	if err != nil {
		return false, err
	}

	derived, err := u.Verifier.Derive(rec.Algorithm, password, rec.Salt, rec.Cost)
	if err != nil {
		return false, err
	}
	ok := subtle.ConstantTimeCompare(derived, rec.Derived) == 1
	Zero(derived)
	if !ok {
		return false, nil
	}

	if u.needsUpgrade(rec) {
		u.migrate(user, rec, password)
	}
	return true, nil
}

func (u *Upgrader) needsUpgrade(rec Record) bool {
	return rec.Algorithm != u.Algorithm || rec.Cost != u.Cost
}

// migrate re-derives the password under the target parameters and writes it
// back only when the record version is unchanged, so a concurrently set new
// password is never overwritten. All errors are ignored by design: the login
// decision has already been made.
func (u *Upgrader) migrate(user string, rec Record, password []byte) {
	salt := make([]byte, u.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return
	}
	derived, err := u.Verifier.Derive(u.Algorithm, password, salt, u.Cost)
	if err != nil {
		Zero(salt)
		return
	}
	next := Record{
		Version:   rec.Version + 1,
		Algorithm: u.Algorithm,
		Salt:      salt,
		Cost:      u.Cost,
		Derived:   derived,
	}
	if err := u.Store.CompareAndSwapVersion(user, rec.Version, next); err != nil {
		// Concurrent change or store failure: drop the fresh secret
		// material instead of leaving it around.
		Zero(derived)
		Zero(salt)
	}
}

// Zero overwrites buf in place, releasing sensitive key material.
func Zero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

// NewRecord enrolls a password into a fresh Record with the given
// parameters. Hosts use it when users set or change their password.
func NewRecord(v Verifier, version uint64, algorithm string, password, salt []byte, cost int) (Record, error) {
	derived, err := v.Derive(algorithm, password, salt, cost)
	if err != nil {
		return Record{}, err
	}
	return Record{
		Version:   version,
		Algorithm: algorithm,
		Salt:      salt,
		Cost:      cost,
		Derived:   derived,
	}, nil
}

// MemoryStore is an in-memory Store, handy for tests and as a reference for
// the compare-and-swap semantics Upgrader relies on.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]Record)}
}

// Load implements Store.
func (s *MemoryStore) Load(user string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[user]
	if !ok {
		return Record{}, ErrNoSuchUser
	}
	return rec, nil
}

// CompareAndSwapVersion implements Store.
func (s *MemoryStore) CompareAndSwapVersion(user string, expectVersion uint64, next Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[user]
	if !ok {
		return ErrNoSuchUser
	}
	if rec.Version != expectVersion {
		return ErrVersionConflict
	}
	s.records[user] = next
	return nil
}

// Set stores rec as-is. Tests and enrollment paths use it to seed records or
// to simulate a concurrent password change.
func (s *MemoryStore) Set(user string, rec Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[user] = rec
}
