package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	recordBegin  = "tx_begin"
	recordOp     = "tx_op"
	recordCommit = "tx_commit"
	recordRoll   = "tx_rollback"

	logGenesisDomain = "q0165-authenticated-log-genesis-v1"
	logMACDomain     = "q0165-authenticated-log-record-v1"
)

type logRecord struct {
	Seq     uint64          `json:"seq"`
	Prev    string          `json:"prev"`
	Tx      TransactionID   `json:"tx,omitempty"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

type macInput struct {
	Seq     uint64          `json:"seq"`
	Prev    string          `json:"prev"`
	Tx      TransactionID   `json:"tx,omitempty"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type recordSpec struct {
	tx      TransactionID
	kind    string
	payload any
}

type decodedRecord struct {
	tx      TransactionID
	kind    string
	payload json.RawMessage
}

type authLog struct {
	mu       sync.Mutex
	path     string
	key      []byte
	file     *os.File
	memory   []byte
	records  []decodedRecord
	lastHash string
	lastSeq  uint64
	size     int64
	genesis  string
}

func newAuthLog(path string, key []byte) (*authLog, int64, error) {
	if len(key) < 16 {
		return nil, 0, ErrMissingAuthKey
	}
	l := &authLog{path: path, key: append([]byte(nil), key...)}
	l.genesis = l.macHex([]byte(logGenesisDomain))
	l.lastHash = l.genesis

	if path == "" {
		return l, 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, 0, err
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, err
	}
	if created {
		syncDirectory(filepath.Dir(path))
	}
	l.file = f
	data, err := os.ReadFile(path)
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	validEnd, err := l.load(data)
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	truncated := int64(len(data)) - validEnd
	if truncated > 0 {
		if err := f.Truncate(validEnd); err != nil {
			_ = f.Close()
			return nil, 0, err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, 0, err
		}
	}
	l.size = validEnd
	return l, truncated, nil
}

func syncDirectory(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func (l *authLog) load(data []byte) (int64, error) {
	var (
		offset       int
		validEnd     int64
		expectedSeq  uint64 = 1
		expectedPrev        = l.genesis
	)
	for offset < len(data) {
		newline := bytes.IndexByte(data[offset:], '\n')
		if newline < 0 {
			break
		}
		line := data[offset : offset+newline]
		rec, err := l.decode(line)
		if err != nil {
			if errors.Is(err, ErrLogAuthentication) {
				return 0, fmt.Errorf("%w: sequence %d", ErrLogAuthentication, expectedSeq)
			}
			if offset+newline+1 < len(data) {
				return 0, fmt.Errorf("%w: %v", ErrLogCorrupt, err)
			}
			// A complete-looking final record with a bad MAC is corruption,
			// not a torn append. A torn append normally lacks the newline or
			// fails JSON decoding, handled below.
			return 0, fmt.Errorf("%w: %v", ErrLogCorrupt, err)
		}
		if rec.Seq != expectedSeq {
			return 0, fmt.Errorf("%w: sequence %d, want %d", ErrLogCorrupt, rec.Seq, expectedSeq)
		}
		if rec.Prev != expectedPrev {
			return 0, fmt.Errorf("%w: broken hash chain at sequence %d", ErrLogCorrupt, rec.Seq)
		}
		hash := hashHex(line)
		l.records = append(l.records, decodedRecord{tx: rec.Tx, kind: rec.Kind, payload: rec.Payload})
		l.lastHash = hash
		l.lastSeq = rec.Seq
		expectedPrev = hash
		expectedSeq++
		offset += newline + 1
		validEnd = int64(offset)
	}
	return validEnd, nil
}

func (l *authLog) decode(line []byte) (logRecord, error) {
	var rec logRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return rec, fmt.Errorf("%w: invalid JSON", ErrLogCorrupt)
	}
	if rec.Seq == 0 || rec.Prev == "" || rec.Kind == "" || rec.MAC == "" {
		return rec, fmt.Errorf("%w: missing record fields", ErrLogCorrupt)
	}
	input, err := json.Marshal(macInput{
		Seq:     rec.Seq,
		Prev:    rec.Prev,
		Tx:      rec.Tx,
		Kind:    rec.Kind,
		Payload: rec.Payload,
	})
	if err != nil {
		return rec, fmt.Errorf("%w: bad payload", ErrLogCorrupt)
	}
	want := l.macHex(append([]byte(logMACDomain), input...))
	if subtle.ConstantTimeCompare([]byte(rec.MAC), []byte(want)) != 1 {
		return rec, ErrLogAuthentication
	}
	return rec, nil
}

func (l *authLog) macHex(data []byte) string {
	m := hmac.New(sha256.New, l.key)
	_, _ = m.Write(data)
	return hex.EncodeToString(m.Sum(nil))
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// append encodes a transaction as an authenticated chain and makes it durable
// before returning. The in-memory chain is advanced only after the append and
// sync both succeed.
func (l *authLog) append(specs []recordSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil && l.path != "" {
		return ErrHostClosed
	}
	raw, nextHash, nextSeq, err := l.encode(specs)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}

	if l.file == nil {
		l.memory = append(l.memory, raw...)
	} else {
		oldSize := l.size
		n, err := l.file.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err != nil {
			_ = l.file.Truncate(oldSize)
			return fmt.Errorf("%w: %v", ErrLogAppend, err)
		}
		if err := l.file.Sync(); err != nil {
			_ = l.file.Truncate(oldSize)
			_ = l.file.Sync()
			return fmt.Errorf("%w: %v", ErrLogAppend, err)
		}
		l.size += int64(len(raw))
	}

	for _, spec := range specs {
		l.records = append(l.records, decodedRecord{
			tx:      spec.tx,
			kind:    spec.kind,
			payload: mustJSON(spec.payload),
		})
	}
	l.lastSeq = nextSeq
	l.lastHash = nextHash
	return nil
}

func (l *authLog) encode(specs []recordSpec) ([]byte, string, uint64, error) {
	var (
		buf  bytes.Buffer
		prev = l.lastHash
		seq  = l.lastSeq
	)
	for _, spec := range specs {
		payload, err := json.Marshal(spec.payload)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%w: %v", ErrLogAppend, err)
		}
		seq++
		input, err := json.Marshal(macInput{
			Seq:     seq,
			Prev:    prev,
			Tx:      spec.tx,
			Kind:    spec.kind,
			Payload: payload,
		})
		if err != nil {
			return nil, "", 0, fmt.Errorf("%w: %v", ErrLogAppend, err)
		}
		mac := l.macHex(append([]byte(logMACDomain), input...))
		retained := json.RawMessage(payload)
		rec := logRecord{
			Seq:     seq,
			Prev:    prev,
			Tx:      spec.tx,
			Kind:    spec.kind,
			Payload: retained,
			MAC:     mac,
		}
		line, err := json.Marshal(rec)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%w: %v", ErrLogAppend, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		prev = hashHex(line)
	}
	return buf.Bytes(), prev, seq, nil
}

func (l *authLog) raw() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path != "" {
		data, err := os.ReadFile(l.path)
		if err == nil {
			return data
		}
	}
	return append([]byte(nil), l.memory...)
}

func (l *authLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Sync()
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
