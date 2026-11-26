package threshold

import "sync"

type BanReason string

const (
	BanInvalidRound1 BanReason = "invalid_round_one"
	BanBadShare      BanReason = "bad_feldman_share"
	BanReusedNonce   BanReason = "reused_nonce"
	BanBadPartial    BanReason = "invalid_partial_signature"
)

// BanRecord is a permanent local accountability record. Hosts may persist it
// outside the process; the in-memory Blacklist is safe for concurrent use.
type BanRecord struct {
	ID     ID        `json:"id"`
	Reason BanReason `json:"reason"`
	Detail string    `json:"detail,omitempty"`
}

type Blacklist struct {
	mu    sync.RWMutex
	bans  map[ID]BanRecord
	order []ID
}

func NewBlacklist() *Blacklist {
	return &Blacklist{bans: make(map[ID]BanRecord)}
}

func (b *Blacklist) Ban(id ID, reason BanReason, detail string) BanRecord {
	if b == nil {
		return BanRecord{ID: id, Reason: reason, Detail: detail}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if record, ok := b.bans[id]; ok {
		return record
	}
	record := BanRecord{ID: id, Reason: reason, Detail: detail}
	b.bans[id] = record
	b.order = append(b.order, id)
	return record
}

func (b *Blacklist) IsBanned(id ID) bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.bans[id]
	return ok
}

func (b *Blacklist) Record(id ID) (BanRecord, bool) {
	if b == nil {
		return BanRecord{}, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.bans[id]
	return r, ok
}

func (b *Blacklist) IDs() []ID {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]ID(nil), b.order...)
}

func (b *Blacklist) Records() []BanRecord {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BanRecord, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, b.bans[id])
	}
	return out
}
