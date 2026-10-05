package catchup

import "hash/crc32"

// Leader 持有最新状态机与日志，传输快照期间日志继续追加。
type Leader struct {
	gen       uint64
	state     []string
	log       []LogEntry
	ChunkSize int
}

// NewLeader 创建一个 Leader，chunkSize 为快照分块字节数。
func NewLeader(chunkSize int) *Leader {
	if chunkSize <= 0 {
		chunkSize = 8
	}
	return &Leader{ChunkSize: chunkSize}
}

// Append 追加一条日志并应用到状态机。
func (l *Leader) Append(term uint64, cmd string) LogEntry {
	e := LogEntry{Index: uint64(len(l.log)) + 1, Term: term, Command: cmd}
	l.log = append(l.log, e)
	l.state = append(l.state, cmd)
	return e
}

// Snapshot 递增代次，把当前状态切成带校验和的块。
// 返回的每个 Chunk 都携带同一份 Meta（含代次与快照索引）。
func (l *Leader) Snapshot() []Chunk {
	l.gen++
	data := encodeState(l.state)
	var lastTerm uint64
	if len(l.log) > 0 {
		lastTerm = l.log[len(l.log)-1].Term
	}
	n := (len(data) + l.ChunkSize - 1) / l.ChunkSize
	if n == 0 {
		n = 1
	}
	meta := SnapshotMeta{
		Generation:        l.gen,
		LastIncludedIndex: uint64(len(l.log)),
		LastIncludedTerm:  lastTerm,
		NumChunks:         n,
		DataChecksum:      crc32.ChecksumIEEE(data),
	}
	chunks := make([]Chunk, n)
	for i := 0; i < n; i++ {
		start := i * l.ChunkSize
		end := start + l.ChunkSize
		if end > len(data) {
			end = len(data)
		}
		var piece []byte
		if start < end {
			piece = data[start:end]
		}
		sum := crc32.ChecksumIEEE(piece)
		meta.ChunkChecksums = append(meta.ChunkChecksums, sum)
		chunks[i] = Chunk{Index: i, Data: piece, Checksum: sum}
	}
	for i := range chunks {
		chunks[i].Meta = meta
	}
	return chunks
}

// Entries 返回从 fromIndex（不含）之后的日志，用于快照安装后的增量重放。
func (l *Leader) Entries(fromIndex uint64) []LogEntry {
	var out []LogEntry
	for _, e := range l.log {
		if e.Index > fromIndex {
			out = append(out, e)
		}
	}
	return out
}
