package speckey

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
)

// checkpointVersion identifies the on-disk checkpoint format.
const checkpointVersion = 1

// ErrBadCheckpoint is returned when a checkpoint fails validation
// (wrong version, count mismatch, or digest mismatch).
var ErrBadCheckpoint = errors.New("speckey: invalid checkpoint")

// checkpoint is the serialized form of a Set. Count and SHA256 are
// redundant integrity fields so corruption is detected on load
// instead of silently restoring a partial or altered dedup set.
type checkpoint struct {
	Version  int               `json:"version"`
	Strategy ConflictStrategy  `json:"strategy"`
	Records  map[string]string `json:"records"`
	Count    int               `json:"count"`
	SHA256   string            `json:"sha256"`
}

// digestRecords computes a deterministic digest over the records map
// using length-prefixed pairs, so no key/content combination can
// produce an ambiguous digest stream.
func digestRecords(records map[string]string) string {
	keys := make([]string, 0, len(records))
	for k := range records {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		v := records[k]
		fmt.Fprintf(h, "%d:%s%d:%s", len(k), k, len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// snapshot builds a validated checkpoint from the set's current state.
func (s *Set) snapshot() checkpoint {
	records := make(map[string]string, len(s.records))
	for k, v := range s.records {
		records[k] = v
	}
	return checkpoint{
		Version:  checkpointVersion,
		Strategy: s.strategy,
		Records:  records,
		Count:    len(records),
		SHA256:   digestRecords(records),
	}
}

// Save writes a checkpoint of the set to w.
func (s *Set) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s.snapshot())
}

// SaveTo atomically writes a checkpoint to path (temp file + rename),
// so a crash mid-save cannot leave a truncated checkpoint behind.
func (s *Set) SaveTo(path string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := s.Save(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a checkpoint from r and validates it before building the
// set. Any integrity failure yields ErrBadCheckpoint and no set.
func Load(r io.Reader) (*Set, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("%v: %s", ErrBadCheckpoint, err)
	}
	if cp.Version != checkpointVersion {
		return nil, fmt.Errorf("%v: unsupported version %d", ErrBadCheckpoint, cp.Version)
	}
	if cp.Count != len(cp.Records) {
		return nil, fmt.Errorf("%v: count %d != %d records", ErrBadCheckpoint, cp.Count, len(cp.Records))
	}
	if digestRecords(cp.Records) != cp.SHA256 {
		return nil, fmt.Errorf("%v: digest mismatch", ErrBadCheckpoint)
	}
	records := make(map[string]string, len(cp.Records))
	for k, v := range cp.Records {
		records[k] = v
	}
	return &Set{strategy: cp.Strategy, records: records}, nil
}

// LoadFrom reads and validates a checkpoint file.
func LoadFrom(path string) (*Set, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}
