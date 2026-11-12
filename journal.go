package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	journalVersion = 1
	maxJournalLine = 4 << 20

	kindCreate        = "create"
	kindAllocate      = "allocate"
	kindBeginErase    = "begin_erase"
	kindStripeChecked = "stripe_checked"
	kindClean         = "clean"
)

// journalEntry is one durable step in a region's lifecycle.  Each entry is a
// single JSON line, written with O_APPEND and fsynced before the in-memory
// state advances.
type journalEntry struct {
	Version        int    `json:"v"`
	Seq            uint64 `json:"seq"`
	Kind           string `json:"kind"`
	Region         string `json:"region"`
	Size           int64  `json:"size,omitempty"`
	CredentialHash string `json:"credential_hash,omitempty"`
	Tenant         string `json:"tenant,omitempty"`
	Stripe         int64  `json:"stripe"`
	StripeCount    int64  `json:"stripe_count"`
	Time           int64  `json:"time,omitempty"`
}

type journal struct {
	f      *os.File
	seq    uint64
	broken error
}

// openJournal reads all complete records and repairs a torn final record.
// Only the final line may be incomplete; corruption anywhere else is fatal.
func openJournal(path string) (*journal, []journalEntry, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("%w: empty journal path", ErrCorruptJournal)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	created := os.IsNotExist(err)

	entries, lastGood, truncate, needsNewline, err := parseJournal(data)
	if err != nil {
		return nil, nil, err
	}
	if truncate >= 0 {
		if err := os.Truncate(path, lastGood); err != nil {
			return nil, nil, err
		}
		created = false
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, nil, err
	}
	if needsNewline {
		if _, err := f.Write([]byte("\n")); err != nil {
			f.Close()
			return nil, nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	if created {
		if err := syncDir(filepath.Dir(path)); err != nil {
			f.Close()
			return nil, nil, err
		}
	}

	j := &journal{f: f}
	if len(entries) > 0 {
		j.seq = entries[len(entries)-1].Seq
	}
	return j, entries, nil
}

// parseJournal returns entries, the byte offset after the last complete record,
// a truncate offset (-1 means no repair needed) and whether a newline must be
// appended to a complete last record.
func parseJournal(data []byte) (entries []journalEntry, lastGood int64, truncate int64, needsNewline bool, err error) {
	truncate = -1
	var pos int64
	chunks := bytes.SplitAfter(data, []byte{'\n'})
	for i, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		line := bytes.TrimSuffix(chunk, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			pos += int64(len(chunk))
			lastGood = pos
			continue
		}
		var e journalEntry
		parsed := false
		if len(line) <= maxJournalLine {
			if unmarshalErr := json.Unmarshal(line, &e); unmarshalErr == nil {
				parsed = true
			}
		}
		if !parsed {
			if !lastNonEmpty(chunks, i) {
				return nil, lastGood, -1, false, fmt.Errorf("%w: invalid record at line %d", ErrCorruptJournal, i+1)
			}
			// A power cut may leave only the final record torn.  JSON records
			// written by this package always start with '{'; only such a
			// final suffix is eligible for torn-tail repair.  Anything else
			// in the final position is corruption.
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 || trimmed[0] != '{' {
				return nil, lastGood, -1, false, fmt.Errorf("%w: invalid final record", ErrCorruptJournal)
			}
			return entries, lastGood, lastGood, false, nil
		}
		if e.Version != journalVersion {
			return nil, lastGood, -1, false, fmt.Errorf("%w: unsupported journal version %d", ErrCorruptJournal, e.Version)
		}
		if e.Seq == 0 ||
			(len(entries) == 0 && e.Seq != 1) ||
			(len(entries) > 0 && e.Seq != entries[len(entries)-1].Seq+1) {
			return nil, lastGood, -1, false, fmt.Errorf("%w: invalid sequence at line %d", ErrCorruptJournal, i+1)
		}
		entries = append(entries, e)
		pos += int64(len(chunk))
		lastGood = pos
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		needsNewline = true
	}
	return entries, lastGood, -1, needsNewline, nil
}

func lastNonEmpty(chunks [][]byte, from int) bool {
	for i := from + 1; i < len(chunks); i++ {
		if len(bytes.TrimSpace(chunks[i])) > 0 {
			return false
		}
	}
	return true
}

func (j *journal) append(e journalEntry) error {
	if j.broken != nil {
		return j.broken
	}
	j.seq++
	e.Version = journalVersion
	e.Seq = j.seq
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	n, err := j.f.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		j.broken = fmt.Errorf("append journal: %w", err)
		return j.broken
	}
	if err := j.f.Sync(); err != nil {
		j.broken = fmt.Errorf("sync journal: %w", err)
		return j.broken
	}
	return nil
}

func (j *journal) close() error {
	if j == nil || j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}
