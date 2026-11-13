package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	logMagic      = "AGL1"
	logVersion    = byte(1)
	logHeaderSize = 116
	maxPayload    = 64 << 20
)

var (
	offPayloadLen  = 8
	offSeq         = 12
	offPayloadHash = 20
	offPrev        = 52
	offMAC         = 84
)

type journalFrame struct {
	record Record
	digest Digest
	start  int64
	end    int64
}

type txTrack struct {
	identity Digest
	stage    Stage
}

type parseInfo struct {
	frames     []journalFrame
	trustedEnd int64
	seq        uint64
	head       Digest
	torn       bool
	bad        error
	badOffset  int64
}

type journal struct {
	path string
	key  []byte
	mu   sync.Mutex
	file *os.File

	frames []journalFrame
	seq    uint64
	head   Digest
	tracks map[TxID]txTrack

	// poisoned is non-nil for a complete record failure, a fork or a
	// duplicate transaction id with different content.  No further records
	// are trusted or appended.
	poisoned error
	torn     bool
}

func openJournalRaw(path string, key []byte) (*journal, error) {
	if len(key) == 0 {
		return nil, ErrHMACKey
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	info := parseJournal(data, key)
	if info.torn && info.bad == nil {
		if err := os.Truncate(path, info.trustedEnd); err != nil {
			return nil, err
		}
		data = data[:info.trustedEnd]
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	j := &journal{
		path:   path,
		key:    append([]byte(nil), key...),
		file:   file,
		frames: info.frames,
		seq:    info.seq,
		head:   info.head,
		torn:   info.torn,
		tracks: make(map[TxID]txTrack),
	}
	for _, frame := range info.frames {
		if err := trackRecord(j.tracks, frame.record); err != nil {
			j.poisoned = fmt.Errorf("%w: %v", ErrLogCompromised, err)
			break
		}
	}
	if info.bad != nil {
		j.poisoned = fmt.Errorf("%w: %v (at byte %d)", ErrLogCompromised, info.bad, info.badOffset)
	}
	return j, nil
}

func parseJournal(data, key []byte) parseInfo {
	var out parseInfo
	var head Digest
	var seq uint64
	tracks := make(map[TxID]txTrack)
	for offset := int64(0); offset < int64(len(data)); {
		remaining := len(data) - int(offset)
		if remaining < logHeaderSize {
			out.torn = true
			break
		}
		header := data[offset : offset+logHeaderSize]
		if string(header[0:4]) != logMagic {
			out.bad = fmt.Errorf("bad magic")
			out.badOffset = offset
			break
		}
		if header[4] != logVersion {
			out.bad = fmt.Errorf("unsupported version %d", header[4])
			out.badOffset = offset
			break
		}
		if header[5] != 0 || header[6] != 0 || header[7] != 0 {
			out.bad = fmt.Errorf("unsupported flags")
			out.badOffset = offset
			break
		}
		payloadLen := uint64(binary.BigEndian.Uint32(header[offPayloadLen : offPayloadLen+4]))
		if payloadLen > maxPayload {
			out.bad = fmt.Errorf("payload too large: %d", payloadLen)
			out.badOffset = offset
			break
		}
		frameLen := uint64(logHeaderSize) + payloadLen
		if uint64(remaining) < frameLen {
			out.torn = true
			break
		}
		frameBytes := data[offset : offset+int64(frameLen)]
		payload := frameBytes[logHeaderSize:]
		payloadHash := frameBytes[offPayloadHash : offPayloadHash+32]
		gotPayloadHash := sha256.Sum256(payload)
		if !hmac.Equal(payloadHash, gotPayloadHash[:]) {
			out.bad = fmt.Errorf("payload digest mismatch")
			out.badOffset = offset
			break
		}
		mac := frameBytes[offMAC : offMAC+32]
		expectedMAC := computeFrameMAC(key, frameBytes[:offMAC])
		if !hmac.Equal(mac, expectedMAC[:]) {
			out.bad = fmt.Errorf("host HMAC mismatch")
			out.badOffset = offset
			break
		}
		var rec Record
		if err := json.Unmarshal(payload, &rec); err != nil {
			out.bad = fmt.Errorf("decode record: %w", err)
			out.badOffset = offset
			break
		}
		if rec.Validate() != nil {
			out.bad = rec.Validate()
			out.badOffset = offset
			break
		}
		headerSeq := binary.BigEndian.Uint64(frameBytes[offSeq : offSeq+8])
		if rec.Seq != headerSeq {
			out.bad = fmt.Errorf("sequence header/record mismatch")
			out.badOffset = offset
			break
		}
		if rec.Seq != seq+1 {
			out.bad = fmt.Errorf("non-contiguous sequence: got %d after %d", rec.Seq, seq)
			out.badOffset = offset
			break
		}
		var prev Digest
		copy(prev[:], frameBytes[offPrev:offPrev+32])
		if prev != head || rec.Prev != head {
			out.bad = fmt.Errorf("chain predecessor mismatch")
			out.badOffset = offset
			break
		}
		if err := trackRecord(tracks, rec); err != nil {
			out.bad = err
			out.badOffset = offset
			break
		}
		digest := frameDigest(frameBytes[:offMAC], payload)
		out.frames = append(out.frames, journalFrame{
			record: rec,
			digest: digest,
			start:  offset,
			end:    offset + int64(frameLen),
		})
		offset += int64(frameLen)
		out.trustedEnd = offset
		seq = rec.Seq
		head = digest
	}
	out.seq = seq
	out.head = head
	return out
}

func trackRecord(tracks map[TxID]txTrack, rec Record) error {
	identity, err := rec.TxDigest()
	if err != nil {
		return err
	}
	if previous, ok := tracks[rec.Identity.TxID]; ok {
		if previous.identity != identity {
			return fmt.Errorf("%w: txID %q repeated with different content", ErrInvalidRecord, rec.Identity.TxID)
		}
		switch previous.stage {
		case StagePrepare:
			if rec.Stage != StageDataStable {
				return fmt.Errorf("%w: txID %q expected DataStable after Prepare, got %s", ErrStageOrder, rec.Identity.TxID, rec.Stage)
			}
		case StageDataStable:
			if rec.Stage != StageCommit {
				return fmt.Errorf("%w: txID %q expected Commit after DataStable, got %s", ErrStageOrder, rec.Identity.TxID, rec.Stage)
			}
		case StageCommit:
			return fmt.Errorf("%w: txID %q has records after Commit", ErrStageOrder, rec.Identity.TxID)
		default:
			return fmt.Errorf("%w: txID %q has invalid prior stage", ErrStageOrder, rec.Identity.TxID)
		}
		tracks[rec.Identity.TxID] = txTrack{identity: identity, stage: rec.Stage}
		return nil
	}
	if rec.Stage != StagePrepare {
		return fmt.Errorf("%w: txID %q starts with %s", ErrStageOrder, rec.Identity.TxID, rec.Stage)
	}
	tracks[rec.Identity.TxID] = txTrack{identity: identity, stage: rec.Stage}
	return nil
}

func frameDigest(signedHeader, payload []byte) Digest {
	h := sha256.New()
	_, _ = h.Write(signedHeader)
	_, _ = h.Write(payload)
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

func computeFrameMAC(key, signedHeader []byte) Digest {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(signedHeader)
	var d Digest
	copy(d[:], m.Sum(nil))
	return d
}

func encodeFrame(rec Record, key []byte) ([]byte, Digest, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, ZeroDigest, err
	}
	if len(payload) > maxPayload {
		return nil, ZeroDigest, fmt.Errorf("record too large")
	}
	header := make([]byte, logHeaderSize)
	copy(header[0:4], logMagic)
	header[4] = logVersion
	binary.BigEndian.PutUint32(header[offPayloadLen:offPayloadLen+4], uint32(len(payload)))
	binary.BigEndian.PutUint64(header[offSeq:offSeq+8], rec.Seq)
	ph := sha256.Sum256(payload)
	copy(header[offPayloadHash:offPayloadHash+32], ph[:])
	copy(header[offPrev:offPrev+32], rec.Prev[:])
	digest := frameDigest(header[:offMAC], payload)
	mac := computeFrameMAC(key, header[:offMAC])
	copy(header[offMAC:offMAC+32], mac[:])
	frame := make([]byte, 0, logHeaderSize+len(payload))
	frame = append(frame, header...)
	frame = append(frame, payload...)
	return frame, digest, nil
}

func (j *journal) append(rec Record) (Record, Digest, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.poisoned != nil {
		return Record{}, ZeroDigest, j.poisoned
	}
	if j.file == nil {
		return Record{}, ZeroDigest, ErrClosed
	}
	rec.Seq = j.seq + 1
	rec.Prev = j.head
	if err := rec.Validate(); err != nil {
		return Record{}, ZeroDigest, err
	}
	if err := trackRecord(j.tracks, rec); err != nil {
		if errors.Is(err, ErrInvalidRecord) {
			return Record{}, ZeroDigest, fmt.Errorf("%w: %v", ErrTxIDExists, err)
		}
		return Record{}, ZeroDigest, err
	}
	frame, digest, err := encodeFrame(rec, j.key)
	if err != nil {
		return Record{}, ZeroDigest, err
	}
	if err := writeAll(j.file, frame); err != nil {
		j.poisoned = fmt.Errorf("%w: append failed: %v", ErrLogCompromised, err)
		return Record{}, ZeroDigest, err
	}
	if err := j.file.Sync(); err != nil {
		j.poisoned = fmt.Errorf("%w: sync failed: %v", ErrLogCompromised, err)
		return Record{}, ZeroDigest, err
	}
	start := int64(0)
	if len(j.frames) > 0 {
		start = j.frames[len(j.frames)-1].end
	}
	j.frames = append(j.frames, journalFrame{
		record: rec,
		digest: digest,
		start:  start,
		end:    start + int64(len(frame)),
	})
	j.seq = rec.Seq
	j.head = digest
	return rec, digest, nil
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func (j *journal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

func (j *journal) health() (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.poisoned != nil {
		return false, j.poisoned
	}
	return true, nil
}

func (j *journal) position() (uint64, Digest) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.seq, j.head
}

func (j *journal) trustedRecords() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, len(j.frames))
	for i, frame := range j.frames {
		out[i] = frame.record
	}
	return out
}

func (j *journal) trustedFrames() []journalFrame {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]journalFrame, len(j.frames))
	copy(out, j.frames)
	return out
}

func (j *journal) frameBySeq(seq uint64) (journalFrame, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, frame := range j.frames {
		if frame.record.Seq == seq {
			return frame, true
		}
	}
	return journalFrame{}, false
}

// Journal is an exported view of the signed append-only transaction log.  It
// is useful for incident inspection and for tests that need to exercise
// truncated, forked or corrupted logs.
type Journal struct {
	j *journal
}

// OpenJournal opens a log with the given host HMAC key.  If the log has a
// complete but unauthentic record, the returned Journal exposes only the
// trusted prefix and the error wraps ErrLogCompromised.
func OpenJournal(path string, key []byte) (*Journal, error) {
	j, err := openJournalRaw(path, key)
	if err != nil {
		return nil, err
	}
	if _, healthErr := j.health(); healthErr != nil {
		return &Journal{j: j}, healthErr
	}
	return &Journal{j: j}, nil
}

func (x *Journal) Close() error {
	return x.j.close()
}

func (x *Journal) Path() string {
	return x.j.path
}

func (x *Journal) Records() []Record {
	return x.j.trustedRecords()
}

func (x *Journal) Head() (uint64, Digest) {
	return x.j.position()
}

func (x *Journal) Health() (bool, error) {
	return x.j.health()
}

// Append adds a record using the next chain position.  It refuses to append
// to a compromised log and refuses duplicate transaction ids, stage reordering
// and receipt/identity mismatches.
func (x *Journal) Append(rec Record) (Record, Digest, error) {
	return x.j.append(rec)
}

// VerifyResult is a compact description of a log verification pass.
type VerifyResult struct {
	TrustedRecords int
	HeadSeq        uint64
	HeadDigest     Digest
	TornTail       bool
	Err            error
}

// Verify re-reads the log from disk.
func (x *Journal) Verify() VerifyResult {
	data, err := os.ReadFile(x.j.path)
	if err != nil {
		return VerifyResult{Err: err}
	}
	info := parseJournal(data, x.j.key)
	res := VerifyResult{
		TrustedRecords: len(info.frames),
		HeadSeq:        info.seq,
		HeadDigest:     info.head,
		TornTail:       info.torn,
	}
	if info.bad != nil {
		res.Err = fmt.Errorf("%w: %v (at byte %d)", ErrLogCompromised, info.bad, info.badOffset)
	}
	return res
}

// ParseJournalBytes is a helper for tests and forensic tooling.  It returns
// only the trusted prefix; a torn tail is reported separately.
func ParseJournalBytes(data, key []byte) ([]Record, VerifyResult) {
	info := parseJournal(data, key)
	records := make([]Record, len(info.frames))
	for i, frame := range info.frames {
		records[i] = frame.record
	}
	res := VerifyResult{
		TrustedRecords: len(records),
		HeadSeq:        info.seq,
		HeadDigest:     info.head,
		TornTail:       info.torn,
	}
	if info.bad != nil {
		res.Err = fmt.Errorf("%w: %v (at byte %d)", ErrLogCompromised, info.bad, info.badOffset)
	}
	return records, res
}

// Bytes returns the raw log bytes.
func (x *Journal) Bytes() ([]byte, error) {
	return os.ReadFile(x.j.path)
}
