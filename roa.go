package pathsec

import (
	"fmt"
	"sync"
	"time"
)

// ROA is a validated RPKI ROA-like prefix authorization with an optional
// validity interval. A zero ValidFrom means "already valid" and a zero
// ExpiresAt means "does not expire".
type ROA struct {
	Prefix    string    `json:"prefix"`
	MaxLength int       `json:"maxLength"`
	OriginASN uint32    `json:"originASN"`
	OriginAS  uint32    `json:"originAS,omitempty"`
	ValidFrom time.Time `json:"validFrom,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	// NotBefore and NotAfter are aliases for callers using X.509-style names.
	NotBefore time.Time `json:"notBefore,omitempty"`
	NotAfter  time.Time `json:"notAfter,omitempty"`
}

// ROARecord is a stored, normalized ROA.
type ROARecord struct {
	ROA
	parsed Prefix
}

// ROAResult is the outcome of origin/maxLength validation.
type ROAResult struct {
	Status    Status      `json:"status"`
	Prefix    string      `json:"prefix"`
	OriginASN uint32      `json:"originASN"`
	Matched   []ROARecord `json:"matched,omitempty"`
	Covering  []ROARecord `json:"covering,omitempty"`
	Reasons   []Reason    `json:"reasons,omitempty"`
}

// Valid reports whether ROA validation succeeded.
func (r ROAResult) Valid() bool { return r.Status == StatusValid }

// ROATable is a concurrency-safe, time-bounded collection of ROAs.
type ROATable struct {
	mu      sync.RWMutex
	records []ROARecord
	now     func() time.Time
}

// NewROATable creates an empty ROA table.
func NewROATable() *ROATable {
	return &ROATable{now: time.Now}
}

// NewROATableFromRecords builds a table and panics if any record is invalid.
func NewROATableFromRecords(records ...ROA) *ROATable {
	table := NewROATable()
	for _, record := range records {
		table.MustAdd(record)
	}
	return table
}

// SetClock replaces the time source. It is primarily useful in tests.
func (t *ROATable) SetClock(now func() time.Time) *ROATable {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now != nil {
		t.now = now
	}
	return t
}

// Add validates and stores a ROA.
func (t *ROATable) Add(roa ROA) error {
	roa.OriginASN = firstNonZero(roa.OriginASN, roa.OriginAS)
	roa.OriginAS = roa.OriginASN
	prefix, err := ParsePrefix(roa.Prefix)
	if err != nil {
		return err
	}
	if roa.MaxLength == 0 {
		roa.MaxLength = prefix.Length()
	}
	if roa.MaxLength < prefix.Length() || roa.MaxLength > prefix.MaxLength() {
		return fmt.Errorf("%w: maxLength %d outside %d..%d", ErrInvalidPrefix,
			roa.MaxLength, prefix.Length(), prefix.MaxLength())
	}
	if roa.ValidFrom.IsZero() && !roa.NotBefore.IsZero() {
		roa.ValidFrom = roa.NotBefore
	}
	if roa.ExpiresAt.IsZero() && !roa.NotAfter.IsZero() {
		roa.ExpiresAt = roa.NotAfter
	}
	if !roa.ValidFrom.IsZero() && !roa.ExpiresAt.IsZero() &&
		!roa.ExpiresAt.After(roa.ValidFrom) {
		return fmt.Errorf("pathsec: ROA expiration must follow validity start")
	}
	t.mu.Lock()
	t.records = append(t.records, ROARecord{ROA: roa, parsed: prefix})
	t.mu.Unlock()
	return nil
}

// MustAdd panics if roa is invalid. It is intended for tests and fixtures.
func (t *ROATable) MustAdd(roa ROA) *ROATable {
	if err := t.Add(roa); err != nil {
		panic(err)
	}
	return t
}

// AddPrefix is a concise helper for records expiring at expiresAt.
func (t *ROATable) AddPrefix(prefix string, maxLength int, originASN uint32, expiresAt ...time.Time) error {
	roa := ROA{Prefix: prefix, MaxLength: maxLength, OriginASN: originASN}
	if len(expiresAt) > 0 {
		roa.ExpiresAt = expiresAt[0]
	}
	return t.Add(roa)
}

// AddTTL adds a ROA valid from now until now+ttl.
func (t *ROATable) AddTTL(prefix string, maxLength int, originASN uint32, ttl time.Duration) error {
	now := t.currentTime()
	return t.Add(ROA{
		Prefix:    prefix,
		MaxLength: maxLength,
		OriginASN: originASN,
		ValidFrom: now,
		ExpiresAt: now.Add(ttl),
	})
}

func (t *ROATable) currentTime() time.Time {
	if t == nil || t.now == nil {
		return time.Now()
	}
	return t.now()
}

// Active returns records valid at at. A zero at means the table clock.
func (t *ROATable) Active(at ...time.Time) []ROARecord {
	if t == nil {
		return nil
	}
	now := t.currentTime()
	if len(at) > 0 && !at[0].IsZero() {
		now = at[0]
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]ROARecord, 0, len(t.records))
	for _, rec := range t.records {
		if !rec.ValidFrom.IsZero() && now.Before(rec.ValidFrom) {
			continue
		}
		if !rec.ExpiresAt.IsZero() && !now.Before(rec.ExpiresAt) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// PurgeExpired removes records that are no longer active and returns the
// number removed.
func (t *ROATable) PurgeExpired(at ...time.Time) int {
	now := t.currentTime()
	if len(at) > 0 && !at[0].IsZero() {
		now = at[0]
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	kept := t.records[:0]
	removed := 0
	for _, rec := range t.records {
		if !rec.ExpiresAt.IsZero() && !now.Before(rec.ExpiresAt) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	t.records = kept
	return removed
}

// Validate checks routePrefix and originASN against active ROAs.
func (t *ROATable) Validate(routePrefix string, originASN uint32, at ...time.Time) ROAResult {
	prefix, err := ParsePrefix(routePrefix)
	result := ROAResult{Prefix: routePrefix, OriginASN: originASN}
	if err != nil {
		result.Status = StatusInvalid
		result.Reasons = append(result.Reasons, Reason{Code: "invalid_prefix", Message: err.Error()})
		return result
	}
	result.Prefix = prefix.String()

	var now time.Time
	if len(at) > 0 {
		now = at[0]
	}
	for _, rec := range t.Active(now) {
		if !rec.parsed.Contains(prefix) {
			continue
		}
		result.Covering = append(result.Covering, rec)
		lengthOK := prefix.Length() <= rec.MaxLength
		originOK := rec.OriginASN != 0 && rec.OriginASN == originASN
		// AS0 ROAs explicitly deny every origin under the prefix.
		if rec.OriginASN == 0 {
			originOK = false
		}
		if lengthOK && originOK {
			result.Matched = append(result.Matched, rec)
		}
	}

	switch {
	case len(result.Matched) > 0:
		result.Status = StatusValid
	case len(result.Covering) > 0:
		result.Status = StatusInvalid
		for _, rec := range result.Covering {
			if prefix.Length() > rec.MaxLength {
				result.Reasons = append(result.Reasons, Reason{
					Code: "max_length_exceeded",
					Message: fmt.Sprintf("prefix length %d exceeds ROA maxLength %d",
						prefix.Length(), rec.MaxLength),
				})
			}
			if rec.OriginASN != originASN {
				result.Reasons = append(result.Reasons, Reason{
					Code: "origin_mismatch",
					Message: fmt.Sprintf("origin AS%d does not match ROA AS%d",
						originASN, rec.OriginASN),
					ASN: rec.OriginASN,
				})
			}
		}
	default:
		result.Status = StatusNotFound
		result.Reasons = append(result.Reasons, Reason{
			Code:    "roa_not_found",
			Message: "no active covering ROA",
		})
	}
	return result
}

// CheckROA is a package-level convenience wrapper. A nil table behaves as an
// empty table and therefore returns NotFound.
func CheckROA(table *ROATable, prefix string, originASN uint32, at ...time.Time) ROAResult {
	if table == nil {
		return NewROATable().Validate(prefix, originASN, at...)
	}
	return table.Validate(prefix, originASN, at...)
}
