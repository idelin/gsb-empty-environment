package revco

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MemoryStore keeps the journal in process memory. It still provides
// idempotency within one coordinator lifetime but not crash recovery.
type MemoryStore struct {
	journal *Journal
}

func (m *MemoryStore) Load() (*Journal, error) {
	if m.journal == nil {
		return nil, nil
	}
	return cloneJournal(m.journal), nil
}

func (m *MemoryStore) Save(j *Journal) error {
	m.journal = cloneJournal(j)
	return nil
}

// FileStore persists the journal as JSON, replacing the file atomically so an
// interruption never leaves a torn journal.
type FileStore struct {
	Path string
}

func (f *FileStore) Load() (*Journal, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func (f *FileStore) Save(j *Journal) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cloneJournal(j), "", "  ")
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func cloneJournal(j *Journal) *Journal {
	cp := newJournal()
	for k, v := range j.Requests {
		req := *v
		req.Rights = append([]Right(nil), v.Rights...)
		cp.Requests[k] = &req
	}
	for k, v := range j.Receipts {
		rc := *v
		rc.Rights = append([]Right(nil), v.Rights...)
		rc.Revoked = append([]Range(nil), v.Revoked...)
		rc.Unknown = append([]Range(nil), v.Unknown...)
		rc.Blocked = append([]BlockedChannel(nil), v.Blocked...)
		rc.RevokedKeys = append([]HoldKey(nil), v.RevokedKeys...)
		rc.Watermarks = map[ChanID]Seq{}
		for ch, w := range v.Watermarks {
			rc.Watermarks[ch] = w
		}
		cp.Receipts[k] = &rc
	}
	for k, v := range j.Revoked {
		cp.Revoked[k] = append([]HoldKey(nil), v...)
	}
	return cp
}
