package repl

import (
	"crypto/sha256"
	"fmt"

	"snaprepl/sim"
)

// Stats records evidence counters for safety/liveness reports.
type Stats struct {
	Applied            int // log entries applied to the state machine
	DuplicatesSkipped  int // entries/chunks already covered, skipped
	ChunksReceived     int
	ChunkDuplicates    int
	StaleChunks        int
	Rollbacks          int // digest mismatches -> staging discarded
	SnapshotsInstalled int
	Crashes            int
}

func (s Stats) String() string {
	return fmt.Sprintf("applied=%d dupSkipped=%d chunks=%d chunkDup=%d staleChunk=%d rollbacks=%d snapshots=%d crashes=%d",
		s.Applied, s.DuplicatesSkipped, s.ChunksReceived, s.ChunkDuplicates, s.StaleChunks, s.Rollbacks, s.SnapshotsInstalled, s.Crashes)
}

type staging struct {
	meta   SnapshotMeta
	chunks map[int][]byte
	got    int
}

// Follower keeps a durable applied state and a volatile staging area.
// Reads only ever touch the applied state, so during install the old
// state is served consistently and never mixes with the new one.
type Follower struct {
	ID  string
	Net *sim.Net

	applied     map[string]string // durable
	lastApplied int               // durable
	gen         int               // durable: installed snapshot generation

	st      *staging      // volatile: install in progress
	pending map[int]Entry // volatile: out-of-order / during-install entries

	Stats Stats
}

func NewFollower(id string, net *sim.Net) *Follower {
	f := &Follower{
		ID:      id,
		Net:     net,
		applied: map[string]string{},
		pending: map[int]Entry{},
	}
	net.Register(id, f.Handle)
	return f
}

// Handle is the network delivery entry point.
func (f *Follower) Handle(msg interface{}) {
	switch m := msg.(type) {
	case Entry:
		f.onEntry(m)
	case Chunk:
		f.onChunk(m)
	}
}

func (f *Follower) onChunk(c Chunk) {
	// Snapshot already fully covered by our state: stale, ignore.
	if c.Meta.LastIndex <= f.lastApplied {
		f.Stats.StaleChunks++
		return
	}
	// A different snapshot supersedes the current staging only if it is
	// at least as new; otherwise the chunk is stale.
	if f.st != nil && f.st.meta.ID != c.Meta.ID && c.Meta.LastIndex < f.st.meta.LastIndex {
		f.Stats.StaleChunks++
		return
	}
	if f.st == nil || f.st.meta.ID != c.Meta.ID {
		f.st = &staging{meta: c.Meta, chunks: map[int][]byte{}}
	}
	if _, dup := f.st.chunks[c.Offset]; dup {
		f.Stats.ChunkDuplicates++
	} else {
		f.st.chunks[c.Offset] = c.Data
		f.st.got++
		f.Stats.ChunksReceived++
	}
	// Missing chunks => got < Chunks => never publish.
	if f.st.got == f.st.meta.Chunks {
		f.tryPublish()
	}
}

func (f *Follower) tryPublish() {
	st := f.st
	var buf []byte
	for i := 0; i < st.meta.Chunks; i++ {
		buf = append(buf, st.chunks[i]...)
	}
	if sha256.Sum256(buf) != st.meta.Digest {
		// Digest mismatch: rollback. Staging is discarded; the durable
		// applied state is byte-for-byte untouched.
		f.st = nil
		f.Stats.Rollbacks++
		return
	}
	m, err := DecodeState(buf)
	if err != nil {
		f.st = nil
		f.Stats.Rollbacks++
		return
	}
	// Atomic publish: one swap inside one simulator event.
	f.applied = m
	f.lastApplied = st.meta.LastIndex
	f.gen = st.meta.Gen
	f.st = nil
	f.Stats.SnapshotsInstalled++
	// Entries the snapshot already covers are dropped, never re-applied.
	for idx := range f.pending {
		if idx <= f.lastApplied {
			delete(f.pending, idx)
			f.Stats.DuplicatesSkipped++
		}
	}
	f.drain()
}

func (f *Follower) onEntry(e Entry) {
	// Already applied (or covered by an installed snapshot): skip.
	if e.Index <= f.lastApplied {
		f.Stats.DuplicatesSkipped++
		return
	}
	// During install, or a gap in the log: buffer for later.
	if f.st != nil || e.Index != f.lastApplied+1 {
		f.pending[e.Index] = e
		return
	}
	f.apply(e)
	f.drain()
}

func (f *Follower) drain() {
	for {
		e, ok := f.pending[f.lastApplied+1]
		if !ok {
			return
		}
		delete(f.pending, e.Index)
		f.apply(e)
	}
}

func (f *Follower) apply(e Entry) {
	f.applied[e.Key] = e.Value
	f.lastApplied = e.Index
	f.Stats.Applied++
}

// Crash models a follower restart: volatile staging and buffered entries
// are lost; the durable applied state survives.
func (f *Follower) Crash() {
	f.st = nil
	f.pending = map[int]Entry{}
	f.Stats.Crashes++
}

// ReadAll returns a copy of the applied state, its log index and
// snapshot generation. It never observes staging data.
func (f *Follower) ReadAll() (map[string]string, int, int) {
	out := make(map[string]string, len(f.applied))
	for k, v := range f.applied {
		out[k] = v
	}
	return out, f.lastApplied, f.gen
}

// LastApplied exposes the durable log index.
func (f *Follower) LastApplied() int { return f.lastApplied }

// Installing reports whether a snapshot install is in progress.
func (f *Follower) Installing() bool { return f.st != nil }
