package dedup

import "time"

// tombstone is the anti-duplicate marker left behind when a result
// entity is evicted. Its lifecycle is independent of the result's
// retention period: it survives until the source confirms a watermark
// at or beyond the recorded position, guaranteeing the original event
// cannot be redelivered.
type tombstone struct {
	pos       Position
	createdAt time.Time
}

// ConfirmWatermark records that the given source has durably
// acknowledged all events up to and including offset. Tombstones whose
// recorded position is covered by this watermark are dropped, because
// the source can no longer redeliver those events. It returns the
// number of tombstones dropped.
func (s *Store) ConfirmWatermark(source string, offset uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	for key, ts := range s.tombstones {
		if ts.pos.Source == source && ts.pos.Offset <= offset {
			delete(s.tombstones, key)
			dropped++
		}
	}
	return dropped
}
