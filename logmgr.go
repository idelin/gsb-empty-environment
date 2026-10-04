package waldb

import "sync"

// logMgr owns the WAL: a volatile tail buffer plus the durable log
// file. LSNs are strictly increasing, assigned sequentially.
type logMgr struct {
	ad *Adapter

	mu         sync.Mutex
	buf        []byte // appended records not yet durable
	bufLastLSN uint64 // LSN of the last record in buf
	nextLSN    uint64
	flushedLSN uint64 // every record <= flushedLSN is durable
	walLen     int64  // durable length of the log file
	records    map[uint64]*LogRecord
}

func newLogMgr(ad *Adapter) *logMgr {
	return &logMgr{ad: ad, nextLSN: 1, records: map[uint64]*LogRecord{}}
}

// append assigns the next LSN and buffers the record. The record is
// NOT durable until flush covers its LSN.
func (m *logMgr) append(r *LogRecord) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.LSN = m.nextLSN
	m.nextLSN++
	m.buf = append(m.buf, encodeRecord(r)...)
	m.bufLastLSN = r.LSN
	m.records[r.LSN] = r
	return r.LSN
}

// flush makes the buffered tail durable, guaranteeing every record
// with LSN <= upTo is persisted. This is the WAL durability boundary.
// A flush may be torn (crash mid-write): then only a prefix of the
// buffer became durable, and the durable frontier advances only over
// the complete records of that prefix. The remainder stays buffered
// and is rewritten at the corrected offset by the next flush, so the
// log never contains unreachable bytes.
func (m *logMgr) flush(upTo uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if upTo <= m.flushedLSN || len(m.buf) == 0 {
		return
	}
	n, err := m.ad.WriteSync(m.walLen, m.buf)
	if err != nil {
		return
	}
	recs, validLen := scanLog(m.buf[:n])
	m.walLen += int64(validLen)
	if len(recs) > 0 {
		m.flushedLSN = recs[len(recs)-1].LSN
	}
	m.buf = m.buf[validLen:]
}

// flushAll persists everything currently buffered.
func (m *logMgr) flushAll() {
	m.mu.Lock()
	upTo := m.bufLastLSN
	m.mu.Unlock()
	m.flush(upTo)
}

// resetVolatile drops the in-memory tail (called on crash).
func (m *logMgr) resetVolatile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buf = nil
}

// recoverInit scans the durable log after a crash: it drops any
// incomplete tail record, truncates the file to the valid prefix and
// rebuilds the record index and LSN counters so new appends continue
// with strictly increasing LSNs.
func (m *logMgr) recoverInit() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	size, err := m.ad.Size()
	if err != nil {
		return err
	}
	data := make([]byte, size)
	if size > 0 {
		if _, err := m.ad.ReadAt(data, 0); err != nil {
			return err
		}
	}
	recs, validLen := scanLog(data)
	m.records = map[uint64]*LogRecord{}
	var maxLSN uint64
	for _, r := range recs {
		m.records[r.LSN] = r
		if r.LSN > maxLSN {
			maxLSN = r.LSN
		}
	}
	m.nextLSN = maxLSN + 1
	m.flushedLSN = maxLSN
	m.walLen = int64(validLen)
	m.buf = nil
	return m.ad.Truncate(int64(validLen))
}
