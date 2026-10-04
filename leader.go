package snapcatchup

import "fmt"

// Entry is a replicated log record.
type Entry struct {
	Index uint64
	Term  uint64
	Cmd   string
}

// Leader owns the authoritative log and state machine. It keeps appending
// new entries while a snapshot transfer is in flight.
type Leader struct {
	term      uint64
	baseIndex uint64 // last index included in the latest snapshot
	baseTerm  uint64
	log       []Entry // entries with Index > baseIndex
	sm        *StateMachine
	snapGen   uint64
	snapData  []byte
	snapMeta  SnapshotMeta
	chunkSize int
}

func NewLeader(chunkSize int) *Leader {
	return &Leader{term: 1, sm: NewStateMachine(), chunkSize: chunkSize}
}

// Append replicates a command locally and returns its log entry.
func (l *Leader) Append(cmd string) Entry {
	e := Entry{Index: l.baseIndex + uint64(len(l.log)) + 1, Term: l.term, Cmd: cmd}
	l.log = append(l.log, e)
	l.sm.Apply(cmd)
	return e
}

// TakeSnapshot starts a new snapshot generation at the current last index
// and compacts the log up to that point.
func (l *Leader) TakeSnapshot() SnapshotMeta {
	lastIndex := l.baseIndex + uint64(len(l.log))
	lastTerm := l.term
	if len(l.log) > 0 {
		lastTerm = l.log[len(l.log)-1].Term
	}
	l.snapGen++
	l.snapData = l.sm.Encode()
	n := (len(l.snapData) + l.chunkSize - 1) / l.chunkSize
	if n == 0 {
		n = 1
	}
	l.snapMeta = SnapshotMeta{
		Generation: l.snapGen,
		LastIndex:  lastIndex,
		LastTerm:   lastTerm,
		NumChunks:  n,
		Checksum:   checksum(l.snapData),
	}
	l.baseIndex = lastIndex
	l.baseTerm = lastTerm
	l.log = nil
	return l.snapMeta
}

// SnapshotMeta returns the meta of the latest snapshot generation.
func (l *Leader) SnapshotMeta() SnapshotMeta { return l.snapMeta }

// State exposes the leader state machine for test assertions.
func (l *Leader) State() *StateMachine { return l.sm }

// Chunk serves one chunk of the given snapshot generation.
func (l *Leader) Chunk(gen uint64, seq int) (Chunk, error) {
	if gen != l.snapGen {
		return Chunk{}, fmt.Errorf("leader: generation %d not available (current %d)", gen, l.snapGen)
	}
	if seq < 0 || seq >= l.snapMeta.NumChunks {
		return Chunk{}, fmt.Errorf("leader: chunk seq %d out of range", seq)
	}
	start := seq * l.chunkSize
	end := start + l.chunkSize
	if end > len(l.snapData) {
		end = len(l.snapData)
	}
	data := l.snapData[start:end]
	cp := make([]byte, len(data))
	copy(cp, data)
	return Chunk{Meta: l.snapMeta, Seq: seq, Data: cp, Checksum: checksum(cp)}, nil
}

// EntriesAfter returns log entries with Index greater than index.
func (l *Leader) EntriesAfter(index uint64) []Entry {
	var out []Entry
	for _, e := range l.log {
		if e.Index > index {
			out = append(out, e)
		}
	}
	return out
}
