package diskpq

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sort"
)

const (
	walFileName      = "wal.log"
	snapshotFileName = "snapshot.json"
	snapshotTmpName  = "snapshot.json.tmp"
)

const (
	opEnqueue = "enq"
	opUpdate  = "upd"
	opDelete  = "del"
	opPop     = "pop"
)

// walRecord is one serialized, checksummed WAL record.
type walRecord struct {
	Op       string   `json:"op"`
	ID       string   `json:"id,omitempty"`
	Priority int64    `json:"pri,omitempty"`
	Seq      uint64   `json:"seq,omitempty"`
	IDs      []string `json:"ids,omitempty"`
}

type snapshotData struct {
	LastSeq uint64         `json:"last_seq"`
	Items   []snapshotItem `json:"items"`
}

type snapshotItem struct {
	ID       string `json:"id"`
	Priority int64  `json:"pri"`
	Seq      uint64 `json:"seq"`
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// appendWAL writes one record, fsyncs it, and only then returns so the caller
// can apply it to memory. A failure leaves memory untouched by the caller.
func (q *Queue) appendWAL(rec walRecord) error {
	if q.hooks.BeforeWALAppend != nil {
		if err := q.hooks.BeforeWALAppend(rec.Op); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	sum := crc32.Checksum(payload, crcTable)
	var line []byte
	line = append(line, padHex(sum)...)
	line = append(line, ' ')
	line = append(line, payload...)
	line = append(line, '\n')

	start := q.walSize
	if _, err := q.wal.Write(line); err != nil {
		return err
	}
	commitFailed := func(commitErr error) error {
		// The write is not durable and the call is failing, so roll the WAL
		// back to the last durable prefix. Memory is never applied on error,
		// keeping disk and the returned error consistent: failed commit == the
		// operation never happened.
		_ = q.wal.Truncate(start)
		_, _ = q.wal.Seek(start, io.SeekStart)
		return commitErr
	}
	if q.hooks.BeforeWALSync != nil {
		if err := q.hooks.BeforeWALSync(rec.Op); err != nil {
			return commitFailed(err)
		}
	}
	if err := q.wal.Sync(); err != nil {
		return commitFailed(err)
	}
	if q.hooks.AfterWALSync != nil {
		// Past this point the record is durable: the hook can abort the
		// current process (simulated crash) but cannot un-commit the record.
		if err := q.hooks.AfterWALSync(rec.Op); err != nil {
			q.walSize += int64(len(line))
			return err
		}
	}
	q.walSize += int64(len(line))
	return nil
}

func padHex(sum uint32) []byte {
	b := make([]byte, 8)
	hex.Encode(b, []byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
	return b
}

// openWALAndReplay opens the WAL (creating it if needed), applies every intact
// record to the in-memory index, truncates any torn tail, and positions the
// file for appends.
func (q *Queue) openWALAndReplay() error {
	f, err := os.OpenFile(q.walPath(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	q.wal = f

	reader := bufio.NewReader(f)
	var valid int64
	for {
		line, readErr := reader.ReadBytes('\n')
		complete := len(line) > 0 && line[len(line)-1] == '\n'
		if complete {
			rec, perr := parseWALRecord(bytes.TrimRight(line, "\n"))
			if perr == nil {
				q.applyRecord(rec)
				valid += int64(len(line))
			} else {
				// Bad CRC / malformed record: everything from here on is an
				// uncommitted tail (torn write on power loss) and is dropped.
				break
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
		if !complete {
			// Partial line with no newline: torn tail.
			break
		}
	}

	if err := f.Truncate(valid); err != nil {
		return err
	}
	if _, err := f.Seek(valid, io.SeekStart); err != nil {
		return err
	}
	q.walSize = valid
	return nil
}

func parseWALRecord(line []byte) (*walRecord, error) {
	sp := bytes.IndexByte(line, ' ')
	if sp != 8 {
		return nil, fmt.Errorf("bad record framing")
	}
	got, err := hex.DecodeString(string(line[:8]))
	if err != nil || len(got) != 4 {
		return nil, fmt.Errorf("bad crc")
	}
	payload := line[sp+1:]
	want := crc32.Checksum(payload, crcTable)
	if uint32(got[0])<<24|uint32(got[1])<<16|uint32(got[2])<<8|uint32(got[3]) != want {
		return nil, fmt.Errorf("crc mismatch")
	}
	var rec walRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, err
	}
	switch rec.Op {
	case opEnqueue, opUpdate, opDelete, opPop:
		return &rec, nil
	default:
		return nil, fmt.Errorf("unknown op %q", rec.Op)
	}
}

// applyRecord mutates only the id index. The heap is (re)built afterwards.
//
// Crucial rule for update: it only changes a task that currently exists. A
// stale update for a deleted or unknown id is ignored, so replaying an old
// update can never bring a task back.
func (q *Queue) applyRecord(rec *walRecord) {
	switch rec.Op {
	case opEnqueue:
		q.live[rec.ID] = &Item{ID: rec.ID, Priority: rec.Priority, Seq: rec.Seq}
		if rec.Seq > q.lastSeq {
			q.lastSeq = rec.Seq
		}
	case opUpdate:
		if it, ok := q.live[rec.ID]; ok {
			it.Priority = rec.Priority
		}
	case opDelete:
		delete(q.live, rec.ID)
	case opPop:
		for _, id := range rec.IDs {
			delete(q.live, id)
		}
	}
}

// loadSnapshot loads and verifies the snapshot file if present. On-disk layout:
//
//	<json payload>\n<8 hex digits crc32 of payload>\n
func (q *Queue) loadSnapshot() error {
	data, err := os.ReadFile(q.snapPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	const suffixLen = 1 + 8 + 1 // '\n' + crc + '\n'
	if len(data) < suffixLen {
		return fmt.Errorf("diskpq: snapshot too short")
	}
	payload := data[:len(data)-suffixLen]
	if data[len(payload)] != '\n' || data[len(data)-1] != '\n' {
		return fmt.Errorf("diskpq: bad snapshot framing")
	}
	got, err := hex.DecodeString(string(data[len(payload)+1 : len(data)-1]))
	if err != nil || len(got) != 4 {
		return fmt.Errorf("diskpq: bad snapshot crc")
	}
	want := crc32.Checksum(payload, crcTable)
	if uint32(got[0])<<24|uint32(got[1])<<16|uint32(got[2])<<8|uint32(got[3]) != want {
		return fmt.Errorf("diskpq: snapshot crc mismatch")
	}
	var snap snapshotData
	if err := json.Unmarshal(payload, &snap); err != nil {
		return err
	}
	q.lastSeq = snap.LastSeq
	for _, si := range snap.Items {
		q.live[si.ID] = &Item{ID: si.ID, Priority: si.Priority, Seq: si.Seq}
	}
	return nil
}

// maybeCompactLocked snapshots live state and truncates the WAL once it passes
// the threshold. A compaction failure never undoes an already-committed
// operation; it is returned so the caller can observe disk trouble while the
// queue keeps functioning from its current files.
func (q *Queue) maybeCompactLocked() error {
	if q.compactN <= 0 || q.walSize < q.compactN {
		return nil
	}
	return q.compactLocked()
}

// Compact forces a snapshot + WAL truncation.
func (q *Queue) Compact() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	return q.compactLocked()
}

func (q *Queue) compactLocked() error {
	ids := make([]string, 0, len(q.live))
	for id := range q.live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	snap := snapshotData{LastSeq: q.lastSeq}
	for _, id := range ids {
		it := q.live[id]
		snap.Items = append(snap.Items, snapshotItem{
			ID:       it.ID,
			Priority: it.Priority,
			Seq:      it.Seq,
		})
	}
	payload, err := json.Marshal(&snap)
	if err != nil {
		return err
	}
	sum := crc32.Checksum(payload, crcTable)
	content := make([]byte, 0, len(payload)+10)
	content = append(content, payload...)
	content = append(content, '\n')
	content = append(content, padHex(sum)...)
	content = append(content, '\n')

	// Write + fsync a temp file, then publish atomically with rename +
	// directory fsync. A crash at any moment leaves either the old snapshot or
	// the new one, never a half-written snapshot under the real name.
	tmp, err := os.OpenFile(q.snapTmpPath(), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if q.hooks.BeforeSnapshotRename != nil {
		if err := q.hooks.BeforeSnapshotRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(q.snapTmpPath(), q.snapPath()); err != nil {
		return err
	}
	if err := syncDir(q.dir); err != nil {
		return err
	}

	// Snapshot is durable: discard the old WAL. Truncation is itself fsynced
	// and the directory entry synced.
	if q.wal != nil {
		if err := q.wal.Close(); err != nil {
			return err
		}
		q.wal = nil
	}
	wal, err := os.OpenFile(q.walPath(), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := wal.Sync(); err != nil {
		wal.Close()
		return err
	}
	if err := syncDir(q.dir); err != nil {
		wal.Close()
		return err
	}
	q.wal = wal
	q.walSize = 0
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
