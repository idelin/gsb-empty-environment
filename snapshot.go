package snaprepl

// snapshot is the leader-side retained snapshot used to bring
// lagging followers up to date.
type snapshot struct {
	Index  int
	Term   int
	Data   []byte
	Digest [32]byte
}

// snapSend tracks one in-flight snapshot stream to a follower.
// Chunks are retransmitted in full until the follower reports the
// stream installed or rolled back; per-chunk acks were considered and
// rejected: a stale ack from a rolled-back attempt can arrive after the
// leader cleared its state and silently poison the retransmission set.
type snapSend struct {
	index int
}

// staging is the follower-side buffer for an incoming snapshot.
// Staged bytes are never visible to readers: the state machine is
// swapped atomically only after every chunk arrived and the digest
// verified. On any error the staging area is discarded (rollback)
// and the old state remains untouched.
type staging struct {
	index    int
	term     int
	total    int
	digest   [32]byte
	chunks   map[int][]byte
	received int
}

func newStaging(index, term, total int, digest [32]byte) *staging {
	return &staging{index: index, term: term, total: total, digest: digest, chunks: map[int][]byte{}}
}

// consistent reports whether chunk metadata matches the staged snapshot.
func (s *staging) consistent(m Msg) bool {
	return m.Total == s.total && m.Digest == s.digest && m.SnapTerm == s.term
}

// add buffers one chunk. Duplicate chunks are idempotently ignored.
func (s *staging) add(off int, data []byte) {
	if _, ok := s.chunks[off]; ok {
		return
	}
	s.chunks[off] = append([]byte(nil), data...)
	s.received += len(data)
}

// complete reports whether every byte of the snapshot has arrived.
// A snapshot with missing chunks is never published.
func (s *staging) complete() bool { return s.received == s.total }

func (s *staging) assemble() []byte {
	b := make([]byte, 0, s.total)
	for off := 0; off < s.total; {
		c := s.chunks[off]
		b = append(b, c...)
		off += len(c)
	}
	return b
}
