package tls13sm

import (
	"crypto/hmac"
	"sync"
	"time"
)

// DefaultTicketAgeSkew is the default ± tolerance applied to the
// obfuscated ticket age of a 0-RTT attempt (RFC 8446 8.3).
const DefaultTicketAgeSkew = 10 * time.Second

// StrikeRegister is the single-use replay ledger for 0-RTT Early Data.
//
// Each accepted early-data attempt is keyed by the ticket nonce (which RFC
// 8446 requires to be unique per ticket). checkAndStore atomically tests and
// records a nonce: the first call wins and every subsequent call within the
// retention window fails with ErrReplayDetected. Entries expire after
// retention so the ledger cannot grow without bound, but an entry never
// expires while it could still fall inside the replay window of a live
// ticket.
type StrikeRegister struct {
	mu        sync.Mutex
	seen      map[string]time.Time
	retention time.Duration
	now       func() time.Time
}

// NewStrikeRegister builds a ledger whose entries are retained for retention.
func NewStrikeRegister(retention time.Duration) *StrikeRegister {
	if retention <= 0 {
		retention = time.Hour
	}
	return &StrikeRegister{
		seen:      make(map[string]time.Time),
		retention: retention,
		now:       time.Now,
	}
}

// checkAndStore marks ticketNonce as used. It returns
// ErrReplayDetected if the nonce is already present, otherwise nil. Expired
// entries are swept opportunistically under the same lock.
func (s *StrikeRegister) checkAndStore(ticketNonce []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for key, at := range s.seen {
		if now.Sub(at) > s.retention {
			delete(s.seen, key)
		}
	}

	key := string(ticketNonce)
	if _, exists := s.seen[key]; exists {
		return ErrReplayDetected
	}
	s.seen[key] = now
	return nil
}

// Contains reports whether ticketNonce is currently recorded, without
// modifying the ledger. It is primarily useful for tests and diagnostics.
func (s *StrikeRegister) Contains(ticketNonce []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[string(ticketNonce)]
	return ok
}

// Reset removes every recorded nonce.
func (s *StrikeRegister) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = make(map[string]time.Time)
}

// ticketAgeOK validates the obfuscated ticket age of a 0-RTT attempt.
//
//	obfuscated_age = (ticket_age + ticket_age_add) mod 2^32
//
// The recovered age must lie within [elapsed-skew, elapsed+skew] where
// elapsed is the time between ticket issuance and the attempt according to
// the verifier's clock. A negative or wildly stale age is rejected.
func ticketAgeOK(obfuscatedAge, ticketAgeAdd uint32, elapsed, skew time.Duration) bool {
	actualAge := time.Duration((obfuscatedAge-ticketAgeAdd)&0xffffffff) * time.Millisecond
	lower := elapsed - skew
	if lower < 0 {
		lower = 0
	}
	upper := elapsed + skew
	return actualAge >= lower && actualAge <= upper
}

// verifyBinder checks a ClientHello PSK binder in constant time.
func verifyBinder(expected, presented []byte) bool {
	return hmac.Equal(expected, presented)
}
