package risk

import (
	"fmt"
	"sync"
)

// Entry is one immutable audit record. RuleVersion identifies exactly which
// rule set produced the decision, so old/new versions coexist in the log
// during a hot swap.
type Entry struct {
	Timestamp   int64
	TxID        string
	SessionID   string
	Account     string
	Amount      float64
	Decision    Decision
	RuleVersion string
	Reason      string
}

func (e Entry) String() string {
	return fmt.Sprintf("t=%-6d tx=%-8s session=%-8s account=%-8s amount=%-10.2f decision=%-5s rule_version=%-4s %s",
		e.Timestamp, e.TxID, e.SessionID, e.Account, e.Amount, e.Decision, e.RuleVersion, e.Reason)
}

// AuditLog is a thread-safe in-memory audit trail.
type AuditLog struct {
	mu      sync.Mutex
	entries []Entry
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) Append(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

// Entries returns a copy of all recorded entries in append order.
func (l *AuditLog) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}
