package splitter

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Target is an injectable storage adapter for one routing destination.
type Target interface {
	// Put stores the record atomically; it must be safe to retry.
	Put(rec Record) error
	// Get fetches one version of a key; returns ErrNotFound if absent.
	Get(key string, version uint64) (Record, error)
	// Delete removes one version of a key; missing is not an error.
	Delete(key string, version uint64) error
	// List returns every record copy held by this target.
	List() ([]Record, error)
}

// encodeKey makes a primary key safe for use as a path element.
func encodeKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// writeFileAtomic writes data to path via tmp+rename and fsyncs both.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

// FileTarget is a Target backed by a directory tree:
//
//	<dir>/<base64(key)>/<version>.json
type FileTarget struct {
	Dir string
}

func (t *FileTarget) path(key string, version uint64) string {
	return filepath.Join(t.Dir, encodeKey(key), strconv.FormatUint(version, 10)+".json")
}

func (t *FileTarget) Put(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return writeFileAtomic(t.path(rec.Key, rec.Version), data)
}

func (t *FileTarget) Get(key string, version uint64) (Record, error) {
	data, err := os.ReadFile(t.path(key, version))
	if os.IsNotExist(err) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (t *FileTarget) Delete(key string, version uint64) error {
	err := os.Remove(t.path(key, version))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (t *FileTarget) List() ([]Record, error) {
	var out []Record
	keyDirs, err := os.ReadDir(t.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, kd := range keyDirs {
		if !kd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(t.Dir, kd.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(t.Dir, kd.Name(), f.Name()))
			if err != nil {
				return nil, err
			}
			var rec Record
			if err := json.Unmarshal(data, &rec); err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// FaultHooks let tests inject failures and simulated crashes into a Target.
type FaultHooks struct {
	// BeforePut runs before the write hits disk; returning an error aborts the write.
	BeforePut func(rec Record) error
	// AfterPutBeforeAck runs after the write is durable but before the caller
	// sees success; returning an error simulates a lost acknowledgement.
	AfterPutBeforeAck func(rec Record) error
}

// FaultTarget wraps a Target with fault-injection hooks.
type FaultTarget struct {
	Inner Target
	Hooks FaultHooks
}

func (t *FaultTarget) Put(rec Record) error {
	if t.Hooks.BeforePut != nil {
		if err := t.Hooks.BeforePut(rec); err != nil {
			return err
		}
	}
	if err := t.Inner.Put(rec); err != nil {
		return err
	}
	if t.Hooks.AfterPutBeforeAck != nil {
		if err := t.Hooks.AfterPutBeforeAck(rec); err != nil {
			return err
		}
	}
	return nil
}

func (t *FaultTarget) Get(key string, version uint64) (Record, error) {
	return t.Inner.Get(key, version)
}

func (t *FaultTarget) Delete(key string, version uint64) error {
	return t.Inner.Delete(key, version)
}

func (t *FaultTarget) List() ([]Record, error) { return t.Inner.List() }
