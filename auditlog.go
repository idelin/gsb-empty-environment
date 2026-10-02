package tracevault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// AuditEntry is one tamper-evident audit log record.
type AuditEntry struct {
	Seq       int
	Timestamp time.Time
	Action    string
	Issuer    string
	EntityID  string
	FromEpoch int64
	ToEpoch   int64
	Records   int
	Nonce     string
	PrevHash  string
	Hash      string
}

func (e *AuditEntry) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%d|%s|%s|%s|%d|%d|%d|%s|%s",
		e.Seq, e.Timestamp.Unix(), e.Action, e.Issuer, e.EntityID,
		e.FromEpoch, e.ToEpoch, e.Records, e.Nonce, e.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// AuditLog is an append-only, hash-chained audit trail.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

// Append adds a new entry to the chain and returns it.
func (l *AuditLog) Append(action, issuer, entityID string, fromEpoch, toEpoch int64, records int, nonce string, now time.Time) AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := ""
	if len(l.entries) > 0 {
		prev = l.entries[len(l.entries)-1].Hash
	}
	entry := AuditEntry{
		Seq:       len(l.entries),
		Timestamp: now,
		Action:    action,
		Issuer:    issuer,
		EntityID:  entityID,
		FromEpoch: fromEpoch,
		ToEpoch:   toEpoch,
		Records:   records,
		Nonce:     nonce,
		PrevHash:  prev,
	}
	entry.Hash = entry.computeHash()
	l.entries = append(l.entries, entry)
	return entry
}

// Verify recomputes the whole chain and reports any tampering.
func (l *AuditLog) Verify() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := ""
	for i := range l.entries {
		e := &l.entries[i]
		if e.PrevHash != prev || e.Hash != e.computeHash() {
			return false
		}
		prev = e.Hash
	}
	return true
}

// Entries returns a copy of all log entries.
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}
