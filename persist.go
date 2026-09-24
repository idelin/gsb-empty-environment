package paircache

import (
	"encoding/json"
	"fmt"
	"time"
)

const snapshotVersion = 1

type recMeta struct {
	Key  string `json:"k"`
	Size int64  `json:"s"`
	At   int64  `json:"t"` // unix nanos from the injected clock
}

// snapshot holds policy metadata only: queue membership, order, sizes and
// timestamps. Values are not serialized; restored entries are reloaded
// lazily through the configured Loader, so a restored cache replays the
// original access stream with identical hit/miss/eviction decisions.
type snapshot struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"` // "2q" or "lru"
	Cap       int64     `json:"cap"`
	RecentCap int64     `json:"recentCap,omitempty"`
	GhostCap  int       `json:"ghostCap,omitempty"`
	Entries   []recMeta `json:"entries,omitempty"` // lru: MRU -> LRU
	Recent    []recMeta `json:"recent,omitempty"`  // 2q A1in: newest -> oldest
	Freq      []recMeta `json:"freq,omitempty"`    // 2q Am: MRU -> LRU
	Ghost     []recMeta `json:"ghost,omitempty"`   // 2q A1out: newest -> oldest
}

// Snapshot serializes 2Q policy metadata.
func (c *TwoQ) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := snapshot{
		Version:   snapshotVersion,
		Kind:      "2q",
		Cap:       c.cap,
		RecentCap: c.recentCap,
		GhostCap:  c.ghostCap,
	}
	for el := c.recent.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		s.Recent = append(s.Recent, recMeta{e.key, e.size, e.at.UnixNano()})
	}
	for el := c.freq.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		s.Freq = append(s.Freq, recMeta{e.key, e.size, e.at.UnixNano()})
	}
	for el := c.ghost.Front(); el != nil; el = el.Next() {
		g := el.Value.(*ghostRec)
		s.Ghost = append(s.Ghost, recMeta{g.key, g.size, g.at.UnixNano()})
	}
	return json.Marshal(s)
}

// RestoreTwoQ rebuilds a TwoQ from a snapshot. Capacity parameters come from
// the snapshot; Loader, clock and hooks come from opt, so the restored cache
// is policy-identical to the original and a replayed access stream produces
// the same hit/miss/eviction sequence.
func RestoreTwoQ(data []byte, opt Options) (*TwoQ, error) {
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("paircache: bad snapshot: %w", err)
	}
	if s.Version != snapshotVersion {
		return nil, fmt.Errorf("paircache: unsupported snapshot version %d", s.Version)
	}
	if s.Kind != "2q" {
		return nil, fmt.Errorf("paircache: snapshot kind %q is not 2q", s.Kind)
	}
	opt.CapacityBytes = s.Cap
	opt.GhostLimit = s.GhostCap
	c := NewTwoQ(opt)
	c.recentCap = s.RecentCap
	for i := len(s.Recent) - 1; i >= 0; i-- {
		m := s.Recent[i]
		e := &entry{key: m.Key, size: m.Size, at: time.Unix(0, m.At)}
		c.recentIx[m.Key] = c.recent.PushFront(e)
		c.recentBytes += m.Size
		c.acct.alloc(m.Size)
	}
	for i := len(s.Freq) - 1; i >= 0; i-- {
		m := s.Freq[i]
		e := &entry{key: m.Key, size: m.Size, at: time.Unix(0, m.At)}
		c.freqIx[m.Key] = c.freq.PushFront(e)
		c.freqBytes += m.Size
		c.acct.alloc(m.Size)
	}
	for i := len(s.Ghost) - 1; i >= 0; i-- {
		m := s.Ghost[i]
		c.ghostIx[m.Key] = c.ghost.PushFront(&ghostRec{key: m.Key, size: m.Size, at: time.Unix(0, m.At)})
	}
	return c, nil
}

// Snapshot serializes LRU policy metadata.
func (c *LRU) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := snapshot{Version: snapshotVersion, Kind: "lru", Cap: c.cap}
	for el := c.lru.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		s.Entries = append(s.Entries, recMeta{e.key, e.size, e.at.UnixNano()})
	}
	return json.Marshal(s)
}

// RestoreLRU rebuilds an LRU from a snapshot; see RestoreTwoQ.
func RestoreLRU(data []byte, opt Options) (*LRU, error) {
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("paircache: bad snapshot: %w", err)
	}
	if s.Version != snapshotVersion {
		return nil, fmt.Errorf("paircache: unsupported snapshot version %d", s.Version)
	}
	if s.Kind != "lru" {
		return nil, fmt.Errorf("paircache: snapshot kind %q is not lru", s.Kind)
	}
	opt.CapacityBytes = s.Cap
	c := NewLRU(opt)
	for i := len(s.Entries) - 1; i >= 0; i-- {
		m := s.Entries[i]
		e := &entry{key: m.Key, size: m.Size, at: time.Unix(0, m.At)}
		c.ix[m.Key] = c.lru.PushFront(e)
		c.bytes += m.Size
		c.acct.alloc(m.Size)
	}
	return c, nil
}
