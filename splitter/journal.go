package splitter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Journal is the write-ahead log of migration intents. Every state
// transition of an intent is appended (and fsynced) before the
// corresponding action, so a crash at any point leaves enough
// information to finish or safely discard the migration.
type Journal struct {
	path string
}

type journalLine struct {
	Intent Intent `json:"intent"`
}

// OpenJournal opens (creating if needed) the intent journal under dir.
func OpenJournal(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Journal{path: filepath.Join(dir, "intents.jsonl")}, nil
}

// Put appends the current state of an intent and fsyncs it.
func (j *Journal) Put(in Intent) error {
	rec, err := json.Marshal(journalLine{Intent: in})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(rec, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Complete drops the intent for key and compacts the log.
func (j *Journal) Complete(key string) error {
	intents, err := j.Intents()
	if err != nil {
		return err
	}
	if _, ok := intents[key]; !ok {
		return nil
	}
	delete(intents, key)
	return j.rewrite(intents)
}

// Intents returns the latest recorded state per key for all unfinished
// intents. A torn tail line (crash mid-append) is ignored: the previous
// fully-fsynced state is what recovery resumes from.
func (j *Journal) Intents() (map[string]Intent, error) {
	out := map[string]Intent{}
	f, err := os.Open(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec journalLine
		if err := json.Unmarshal(line, &rec); err != nil {
			break // torn tail: stop at first undecodable line
		}
		out[rec.Intent.Key] = rec.Intent
	}
	return out, sc.Err()
}

func (j *Journal) rewrite(intents map[string]Intent) error {
	keys := make([]string, 0, len(intents))
	for k := range intents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, k := range keys {
		if err := enc.Encode(journalLine{Intent: intents[k]}); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, j.path); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(j.path))
}
