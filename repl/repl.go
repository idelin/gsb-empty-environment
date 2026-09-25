// Package repl implements log replication with chunked snapshot install
// for lagging followers, on top of the sim event engine.
//
// Protocol summary:
//   - The leader appends log entries and broadcasts them.
//   - A lagging follower receives a snapshot as ordered chunks plus a
//     digest. Chunks are staged in a scratch area; the old applied state
//     stays fully readable during install (no mixed reads).
//   - When all chunks arrive, the digest is verified. On mismatch the
//     staging area is discarded (rollback) and the old state is untouched.
//   - On success the new state is published atomically (single swap inside
//     one simulator event). Log entries already covered by the snapshot
//     are skipped; later buffered entries are applied in order.
package repl

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"snaprepl/sim"
)

// Entry is one replicated log record (a KV write).
type Entry struct {
	Index int
	Term  int
	Key   string
	Value string
}

// SnapshotMeta describes a chunked snapshot.
type SnapshotMeta struct {
	ID        string
	Gen       int
	LastIndex int // last log index included in the snapshot
	Term      int
	Chunks    int
	Digest    [32]byte
}

// Chunk is one piece of a snapshot byte stream.
type Chunk struct {
	Meta   SnapshotMeta
	Offset int
	Data   []byte
}

// EncodeState serializes a KV map deterministically.
func EncodeState(m map[string]string) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return []byte(b.String())
}

// DecodeState parses EncodeState output.
func DecodeState(b []byte) (map[string]string, error) {
	m := map[string]string{}
	s := string(b)
	if s == "" {
		return m, nil
	}
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		i := strings.Index(line, "=")
		if i <= 0 {
			return nil, fmt.Errorf("corrupt snapshot line %q", line)
		}
		m[line[:i]] = line[i+1:]
	}
	return m, nil
}

// Leader owns the log and serves entries and snapshots.
type Leader struct {
	ID        string
	Net       *sim.Net
	Term      int
	Log       []Entry // 1-based: Log[i-1] has Index i
	State     map[string]string
	Peers     []string
	ChunkSize int
	gen       int
}

func NewLeader(id string, net *sim.Net, chunkSize int) *Leader {
	return &Leader{ID: id, Net: net, Term: 1, State: map[string]string{}, ChunkSize: chunkSize}
}

// CommitIndex is the last appended index (single-leader demo: append == commit).
func (l *Leader) CommitIndex() int { return len(l.Log) }

// Propose appends an entry and broadcasts it to all peers.
func (l *Leader) Propose(k, v string) Entry {
	e := Entry{Index: len(l.Log) + 1, Term: l.Term, Key: k, Value: v}
	l.Log = append(l.Log, e)
	l.State[k] = v
	for _, p := range l.Peers {
		l.Net.Send(l.ID, p, fmt.Sprintf("entry#%d", e.Index), e)
	}
	return e
}

// StateAt reconstructs the KV state after applying the first n log entries.
func (l *Leader) StateAt(n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n && i < len(l.Log); i++ {
		l.Log[i].applyTo(m)
	}
	return m
}

func (e Entry) applyTo(m map[string]string) { m[e.Key] = e.Value }

// SendSnapshot snapshots the state at lastIndex and sends it to `to`
// as independently delayed chunks (so chunks and log entries interleave).
func (l *Leader) SendSnapshot(to string, lastIndex int) SnapshotMeta {
	if lastIndex > len(l.Log) {
		lastIndex = len(l.Log)
	}
	data := EncodeState(l.StateAt(lastIndex))
	l.gen++
	meta := SnapshotMeta{
		ID:        fmt.Sprintf("%s-snap-%d", l.ID, l.gen),
		Gen:       l.gen,
		LastIndex: lastIndex,
		Term:      l.Term,
		Digest:    sha256.Sum256(data),
	}
	chunks := split(data, l.ChunkSize)
	meta.Chunks = len(chunks)
	for i, c := range chunks {
		l.Net.Send(l.ID, to, fmt.Sprintf("chunk %d/%d of %s", i, len(chunks), meta.ID),
			Chunk{Meta: meta, Offset: i, Data: c})
	}
	return meta
}

func split(b []byte, size int) [][]byte {
	if size <= 0 {
		size = 1
	}
	if len(b) == 0 {
		return [][]byte{{}} // empty state still needs one (empty) chunk
	}
	var out [][]byte
	for len(b) > 0 {
		n := size
		if n > len(b) {
			n = len(b)
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}
