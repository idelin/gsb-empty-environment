package splitter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Journal entry states.
const (
	StateIntended      = "intended"       // intent persisted, target write not confirmed
	StateTargetWritten = "target_written" // destination write confirmed, pointer not committed
)

// journalEntry is a persisted migration intent plus its confirmation progress.
// It carries the full event so recovery can redo the target write if the
// destination lost it (e.g. write succeeded but the ack was lost, or the
// process died before writing).
type journalEntry struct {
	Seq       uint64   `json:"seq"`
	Key       string   `json:"key"`
	Version   uint64   `json:"version"`
	Content   string   `json:"content"`
	IsDeleted bool     `json:"is_deleted"`
	Hash      string   `json:"hash"`
	Dest      Location `json:"dest"`
	State     string   `json:"state"`
}

func (e journalEntry) record() Record {
	return Record{
		Key:       e.Key,
		Version:   e.Version,
		Content:   e.Content,
		IsDeleted: e.IsDeleted,
		Hash:      e.Hash,
	}
}

// journal is a directory of per-intent files, each rewritten atomically as
// the intent advances and deleted once the pointer commit is durable.
type journal struct {
	dir string
}

func (j *journal) path(e journalEntry) string {
	return filepath.Join(j.dir, fmt.Sprintf("%08d-%s.json", e.Seq, encodeKey(e.Key)))
}

func (j *journal) save(e journalEntry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeFileAtomic(j.path(e), data)
}

func (j *journal) remove(e journalEntry) error {
	err := os.Remove(j.path(e))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// load returns all pending entries ordered by sequence number.
func (j *journal) load() ([]journalEntry, error) {
	files, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names) // zero-padded seq sorts chronologically
	var out []journalEntry
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(j.dir, name))
		if err != nil {
			return nil, err
		}
		var e journalEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("corrupt journal entry %s: %v", name, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// maxSeq returns the highest sequence number present (0 when empty).
func (j *journal) maxSeq() (uint64, error) {
	files, err := os.ReadDir(j.dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var max uint64
	for _, f := range files {
		name := f.Name()
		if i := strings.IndexByte(name, '-'); i > 0 {
			if n, err := strconv.ParseUint(name[:i], 10, 64); err == nil && n > max {
				max = n
			}
		}
	}
	return max, nil
}
