package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

const (
	formatVersion  = 1
	maxLogRecord   = 1 << 20
	logHeaderSize  = 4
	logChecksumLen = sha256.Size
)

type diskMeta struct {
	Version       int      `json:"version"`
	ChainID       string   `json:"chain_id"`
	Members       []Member `json:"members"`
	GenesisDigest Digest   `json:"genesis_digest"`
}

type journalEntry struct {
	MemberID string
	Share    Share
}

// OpenLedger opens or creates a crash-safe, file-backed ledger.
//
// The data directory contains ledger.json plus one write-ahead log and one
// atomic snapshot per member. The log is authoritative: if the process dies
// after a batch was journaled but before/during snapshotting, reopening the
// ledger replays the tail and reconstructs published shares, pending shares,
// and frozen evidence.
func OpenLedger(dir string, cfg Config) (*Ledger, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty data directory", ErrInvalidConfig)
	}
	normalized, members, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("netsec: create data directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "members"), 0o700); err != nil {
		return nil, fmt.Errorf("netsec: create member directory: %w", err)
	}
	if err := ensureMeta(dir, normalized); err != nil {
		return nil, err
	}

	l := &Ledger{
		cfg:    normalized,
		member: members,
		states: make(map[string]*memberState, len(normalized.Members)),
		dir:    dir,
	}
	for _, m := range normalized.Members {
		st, err := l.loadMemberState(m.ID)
		if err != nil {
			return nil, err
		}
		l.states[m.ID] = st
	}
	return l, nil
}

func ensureMeta(dir string, cfg Config) error {
	path := filepath.Join(dir, "ledger.json")
	want := diskMeta{
		Version:       formatVersion,
		ChainID:       cfg.ChainID,
		Members:       cfg.Members,
		GenesisDigest: cfg.GenesisDigest,
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var got diskMeta
		if err := json.Unmarshal(data, &got); err != nil {
			return fmt.Errorf("netsec: decode ledger metadata: %w", err)
		}
		if err := compareMeta(got, want); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("netsec: read ledger metadata: %w", err)
	}
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return fmt.Errorf("netsec: encode ledger metadata: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeFileAtomic(path, encoded, 0o600); err != nil {
		return fmt.Errorf("netsec: write ledger metadata: %w", err)
	}
	return nil
}

func compareMeta(got, want diskMeta) error {
	if got.Version != want.Version {
		return fmt.Errorf("%w: data format version %d, want %d", ErrInvalidConfig, got.Version, want.Version)
	}
	if got.ChainID != want.ChainID {
		return fmt.Errorf("%w: stored chain ID %q, configured %q", ErrInvalidConfig, got.ChainID, want.ChainID)
	}
	if got.GenesisDigest != want.GenesisDigest {
		return fmt.Errorf("%w: stored genesis digest %s, configured %s",
			ErrInvalidConfig, got.GenesisDigest, want.GenesisDigest)
	}
	if len(got.Members) != len(want.Members) {
		return fmt.Errorf("%w: stored member count %d, configured %d",
			ErrInvalidConfig, len(got.Members), len(want.Members))
	}
	stored := make(map[string]Member, len(got.Members))
	for _, m := range got.Members {
		stored[m.ID] = m
	}
	for _, m := range want.Members {
		other, ok := stored[m.ID]
		if !ok || !bytes.Equal(other.PublicKey, m.PublicKey) {
			return fmt.Errorf("%w: stored member %q does not match configured key", ErrInvalidConfig, m.ID)
		}
	}
	return nil
}

func (l *Ledger) loadMemberState(memberID string) (*memberState, error) {
	statePath := l.memberStatePath(memberID)
	st := newMemberState(memberID)
	if data, err := os.ReadFile(statePath); err == nil {
		var loaded memberState
		if json.Unmarshal(data, &loaded) == nil && loaded.MemberID == memberID {
			loaded.init()
			if loaded.Frozen == nil || l.evidenceValid(loaded.Frozen) {
				st = &loaded
			}
			// Invalid snapshots fall back to a log-only rebuild.
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("netsec: read member state %q: %w", memberID, err)
	}

	logPath := l.memberLogPath(memberID)
	validEnd, needsTruncate, err := l.replayLog(logPath, st)
	if err != nil {
		return nil, fmt.Errorf("netsec: replay member %q: %w", memberID, err)
	}
	if needsTruncate {
		if err := os.Truncate(logPath, validEnd); err != nil {
			return nil, fmt.Errorf("netsec: truncate torn log for member %q: %w", memberID, err)
		}
		if err := syncDir(filepath.Dir(logPath)); err != nil {
			return nil, fmt.Errorf("netsec: sync truncated log for member %q: %w", memberID, err)
		}
	}
	if err := l.writeMemberState(st); err != nil {
		return nil, err
	}
	return st, nil
}

func (l *Ledger) evidenceValid(e *DoubleSignEvidence) bool {
	m, ok := l.member[e.ShareA.MemberID]
	if !ok {
		return false
	}
	keys := map[string]Member{m.ID: m}
	return verifyEvidenceWithKeys(keys, *e) == nil
}

// replayLog applies every valid record in order. Records already present in
// the snapshot are skipped, but their claims are already in the snapshot.
func (l *Ledger) replayLog(path string, st *memberState) (validEnd int64, needsTruncate bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer f.Close()

	var offset int64
	for {
		recordStart := offset
		var lengthBuf [logHeaderSize]byte
		_, readErr := io.ReadFull(f, lengthBuf[:])
		if errors.Is(readErr, io.EOF) {
			return offset, false, nil
		}
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			return recordStart, true, nil
		}
		if readErr != nil {
			return offset, false, readErr
		}
		length := binary.BigEndian.Uint32(lengthBuf[:])
		if length == 0 || length > maxLogRecord {
			return recordStart, true, nil
		}
		payload := make([]byte, int(length)+logChecksumLen)
		if _, readErr := io.ReadFull(f, payload); readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return recordStart, true, nil
			}
			return offset, false, readErr
		}
		body := payload[:length]
		checksum := payload[length:]
		sum := sha256.Sum256(body)
		if !bytes.Equal(checksum, sum[:]) {
			return recordStart, true, nil
		}
		var s Share
		if json.Unmarshal(body, &s) != nil {
			return recordStart, true, nil
		}
		if err := l.validateShare(s); err != nil {
			return recordStart, true, nil
		}
		if _, seen := st.Seen[s.EventID()]; !seen {
			l.ingest(st, s)
		}
		offset = recordStart + int64(logHeaderSize) + int64(length) + logChecksumLen
	}
}

func (l *Ledger) journal(entries []journalEntry) error {
	if l.dir == "" || len(entries) == 0 {
		return nil
	}
	grouped := make(map[string][]Share)
	var order []string
	for _, entry := range entries {
		if _, ok := grouped[entry.MemberID]; !ok {
			order = append(order, entry.MemberID)
		}
		grouped[entry.MemberID] = append(grouped[entry.MemberID], entry.Share)
	}
	sort.Strings(order)

	type openedLog struct {
		path  string
		file  *os.File
		start int64
	}
	var opened []openedLog
	rollback := func() {
		for _, item := range opened {
			_ = item.file.Close()
			_ = os.Truncate(item.path, item.start)
		}
	}

	for _, memberID := range order {
		path := l.memberLogPath(memberID)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			rollback()
			return fmt.Errorf("netsec: create log directory for member %q: %w", memberID, err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			rollback()
			return fmt.Errorf("netsec: open log for member %q: %w", memberID, err)
		}
		start, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			_ = f.Close()
			rollback()
			return fmt.Errorf("netsec: seek log for member %q: %w", memberID, err)
		}
		item := openedLog{path: path, file: f, start: start}
		opened = append(opened, item)

		for _, s := range grouped[memberID] {
			record, err := encodeLogRecord(s)
			if err != nil {
				rollback()
				return fmt.Errorf("netsec: encode log record for member %q: %w", memberID, err)
			}
			if err := writeAll(f, record); err != nil {
				rollback()
				return fmt.Errorf("netsec: append log for member %q: %w", memberID, err)
			}
		}
		if err := f.Sync(); err != nil {
			rollback()
			return fmt.Errorf("netsec: sync log for member %q: %w", memberID, err)
		}
		if err := f.Close(); err != nil {
			rollback()
			return fmt.Errorf("netsec: close log for member %q: %w", memberID, err)
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			rollback()
			return fmt.Errorf("netsec: sync log directory for member %q: %w", memberID, err)
		}
	}
	return nil
}

func encodeLogRecord(s Share) ([]byte, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(body) > maxLogRecord {
		return nil, fmt.Errorf("netsec: log record too large: %d bytes", len(body))
	}
	sum := sha256.Sum256(body)
	record := make([]byte, logHeaderSize+len(body)+logChecksumLen)
	binary.BigEndian.PutUint32(record[:logHeaderSize], uint32(len(body)))
	copy(record[logHeaderSize:], body)
	copy(record[logHeaderSize+len(body):], sum[:])
	return record, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (l *Ledger) persistMembers(members map[string]bool) error {
	if l.dir == "" || len(members) == 0 {
		return nil
	}
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st, ok := l.states[id]
		if !ok {
			continue
		}
		if err := l.writeMemberState(st); err != nil {
			return err
		}
	}
	return nil
}

func (l *Ledger) writeMemberState(st *memberState) error {
	if l.dir == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("netsec: encode member state %q: %w", st.MemberID, err)
	}
	data = append(data, '\n')
	path := l.memberStatePath(st.MemberID)
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("netsec: write member state %q: %w", st.MemberID, err)
	}
	return nil
}

func (l *Ledger) memberLogPath(memberID string) string {
	return filepath.Join(l.memberDir(memberID), memberHash(memberID)+".events.log")
}

func (l *Ledger) memberStatePath(memberID string) string {
	return filepath.Join(l.memberDir(memberID), memberHash(memberID)+".state.json")
}

func (l *Ledger) memberDir(memberID string) string {
	return filepath.Join(l.dir, "members", memberHash(memberID))
}

func memberHash(memberID string) string {
	sum := sha256.Sum256([]byte(memberID))
	return hex.EncodeToString(sum[:])
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := writeAll(tmp, data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}
