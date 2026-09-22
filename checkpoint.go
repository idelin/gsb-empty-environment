package correlator

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Checkpoint support: Snapshot serializes all correlator state (pending
// requests with their original start times, plus the tombstone set) into
// a stable, byte-comparable form; Restore loads it back, so a restarted
// process keeps matching responses against the original start times.

type snapshot struct {
	Pending    []pendingSnap   `json:"pending"`
	Tombstones []tombstoneSnap `json:"tombstones"`
}

type pendingSnap struct {
	Epoch         uint64 `json:"epoch"`
	Seq           uint64 `json:"seq"`
	StartUnixNano int64  `json:"start_unix_nano"`
}

type tombstoneSnap struct {
	Epoch          uint64 `json:"epoch"`
	Seq            uint64 `json:"seq"`
	ExpiryUnixNano int64  `json:"expiry_unix_nano"`
}

// Snapshot encodes the correlator's full state as JSON. Entries are
// sorted so identical state always produces identical bytes.
func (c *Correlator) Snapshot() ([]byte, error) {
	snap := snapshot{
		Pending:    make([]pendingSnap, 0, len(c.pending)),
		Tombstones: make([]tombstoneSnap, 0, len(c.tombstones)),
	}
	for k, st := range c.pending {
		snap.Pending = append(snap.Pending, pendingSnap{
			Epoch:         k.epoch,
			Seq:           k.seq,
			StartUnixNano: st.start.UnixNano(),
		})
	}
	for _, k := range c.tombOrder {
		if at, ok := c.tombstones[k]; ok {
			snap.Tombstones = append(snap.Tombstones, tombstoneSnap{
				Epoch:          k.epoch,
				Seq:            k.seq,
				ExpiryUnixNano: at.UnixNano(),
			})
		}
	}
	sort.Slice(snap.Pending, func(i, j int) bool {
		a, b := snap.Pending[i], snap.Pending[j]
		return a.Epoch < b.Epoch || (a.Epoch == b.Epoch && a.Seq < b.Seq)
	})
	return json.Marshal(snap)
}

// Restore replaces the correlator's state with a previously taken
// Snapshot. It validates the data and enforces the configured bounds;
// on error the current state is left untouched.
func (c *Correlator) Restore(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("correlator: bad checkpoint: %w", err)
	}
	if len(snap.Pending) > c.maxPending {
		return fmt.Errorf("correlator: checkpoint holds %d pending, limit is %d",
			len(snap.Pending), c.maxPending)
	}
	if len(snap.Tombstones) > c.maxTombstones {
		return fmt.Errorf("correlator: checkpoint holds %d tombstones, limit is %d",
			len(snap.Tombstones), c.maxTombstones)
	}
	pending := make(map[key]requestState, len(snap.Pending))
	for _, p := range snap.Pending {
		k := key{epoch: p.Epoch, seq: p.Seq}
		if _, dup := pending[k]; dup {
			return errors.New("correlator: checkpoint has duplicate pending key")
		}
		pending[k] = requestState{start: time.Unix(0, p.StartUnixNano)}
	}
	tombstones := make(map[key]time.Time, len(snap.Tombstones))
	tombOrder := make([]key, 0, len(snap.Tombstones))
	for _, t := range snap.Tombstones {
		k := key{epoch: t.Epoch, seq: t.Seq}
		if _, dup := tombstones[k]; dup {
			return errors.New("correlator: checkpoint has duplicate tombstone key")
		}
		tombstones[k] = time.Unix(0, t.ExpiryUnixNano)
		tombOrder = append(tombOrder, k)
	}
	c.pending = pending
	c.tombstones = tombstones
	c.tombOrder = tombOrder
	return nil
}
