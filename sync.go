package imapsync

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FolderState is the persisted per-folder synchronization state.
type FolderState struct {
	UIDValidity uint64              `json:"uidvalidity"`
	ModSeq      uint64              `json:"modseq"`
	Flags       map[uint64][]string `json:"flags"` // UID -> flag list
}

func newFolderState(uidValidity uint64) *FolderState {
	return &FolderState{UIDValidity: uidValidity, Flags: map[uint64][]string{}}
}

// Store persists folder states as JSON files, one per folder.
type Store struct {
	Dir string
}

func (s *Store) path(folder string) string {
	name := base64.RawURLEncoding.EncodeToString([]byte(folder))
	return filepath.Join(s.Dir, name+".json")
}

// Load returns the stored state, or nil if the folder was never synced.
func (s *Store) Load(folder string) (*FolderState, error) {
	data, err := os.ReadFile(s.path(folder))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st FolderState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("imapsync: corrupt state for %q: %w", folder, err)
	}
	if st.Flags == nil {
		st.Flags = map[uint64][]string{}
	}
	return &st, nil
}

// SaveAtomic writes the state to a temporary file in the same directory,
// fsyncs it, and renames it over the target so a crash can never leave a
// partially written state file behind.
func (s *Store) SaveAtomic(folder string, st *FolderState) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	final := s.path(folder)
	tmp := final + ".tmp"
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, final)
}

// Syncer incrementally synchronizes IMAP folders. Dial establishes a fresh
// connection per folder, so a dropped connection only affects the folder
// currently in flight; its last atomically saved state is the resume point.
type Syncer struct {
	Dial  func() (io.ReadWriteCloser, error)
	Store *Store
}

// SyncFolder synchronizes one folder and atomically saves its new state.
func (s *Syncer) SyncFolder(name string) (*FolderState, error) {
	conn, err := s.Dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	c := NewClient(conn)
	if err := c.Greeting(); err != nil {
		return nil, err
	}
	info, err := c.Select(name)
	if err != nil {
		return nil, err
	}

	st, err := s.Store.Load(name)
	if err != nil {
		return nil, err
	}
	if st == nil || st.UIDValidity != info.uidValidity {
		// First sync, or the server reassigned UIDVALIDITY: all cached
		// UIDs are invalid, rebuild from scratch.
		st = newFolderState(info.uidValidity)
	}

	if err := pullChanges(c, st, info); err != nil {
		return nil, err
	}
	if err := reconcile(c, st); err != nil {
		return nil, err
	}
	if err := s.Store.SaveAtomic(name, st); err != nil {
		return nil, err
	}
	c.Logout()
	return st, nil
}

// SyncAll synchronizes folders in order, saving each folder's state before
// moving on. On failure it returns the folders completed so far.
func (s *Syncer) SyncAll(folders []string) (done []string, err error) {
	for _, f := range folders {
		if _, err := s.SyncFolder(f); err != nil {
			return done, fmt.Errorf("imapsync: sync %q: %w", f, err)
		}
		done = append(done, f)
	}
	return done, nil
}

// pullChanges applies flag changes and new arrivals to st. With a saved
// MODSEQ it issues CHANGEDSINCE; otherwise it falls back to a full fetch.
// Mail arriving mid-sync raises the untagged EXISTS count, which triggers
// another round until the folder is quiet.
func pullChanges(c *Client, st *FolderState, info *selectInfo) error {
	const maxRounds = 8
	for round := 0; ; round++ {
		existsBefore := c.Exists
		items, err := c.uidFetch(st.ModSeq)
		var cmdErr *CmdError
		if err != nil && st.ModSeq > 0 && errors.As(err, &cmdErr) {
			// The server may reject an ancient MODSEQ; rebuild fully.
			// Transport errors are fatal instead: retrying on a dead
			// connection cannot succeed.
			st.ModSeq = 0
			items, err = c.uidFetch(0)
		}
		if err != nil {
			return err
		}
		for _, it := range items {
			st.Flags[it.UID] = it.Flags
			if it.ModSeq > st.ModSeq {
				st.ModSeq = it.ModSeq
			}
		}
		if st.ModSeq < info.highestModSeq {
			st.ModSeq = info.highestModSeq
		}
		if c.Exists <= existsBefore || round >= maxRounds {
			return nil
		}
	}
}

// reconcile drops locally cached UIDs that no longer exist on the server.
// This is the authoritative cleanup for messages expunged mid-sync (seen
// as untagged EXPUNGE) or between syncs.
func reconcile(c *Client, st *FolderState) error {
	live, err := c.uidSearchAll()
	if err != nil {
		return err
	}
	for uid := range st.Flags {
		if !live[uid] {
			delete(st.Flags, uid)
		}
	}
	return nil
}
