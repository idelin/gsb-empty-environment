package segmentfsm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	manifestFileName = "manifest.json"
	journalFileName  = "journal.json"
	tmpSuffix        = ".tmp"
)

// envelope wraps every on-disk JSON document with a checksum of its payload,
// so a torn or corrupted write is detected instead of being silently
// mis-parsed. Combined with tmp-write + atomic rename this should never
// trigger in practice; it is defense in depth.
type envelope struct {
	Payload  json.RawMessage `json:"payload"`
	Checksum string          `json:"checksum"`
}

func marshalEnvelope(payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return json.Marshal(envelope{Payload: raw, Checksum: hex.EncodeToString(sum[:])})
}

func unmarshalEnvelope(data []byte, payload any) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%w: unreadable envelope: %v", ErrCorruption, err)
	}
	sum := sha256.Sum256(env.Payload)
	if hex.EncodeToString(sum[:]) != env.Checksum {
		return fmt.Errorf("%w: envelope checksum mismatch", ErrCorruption)
	}
	if err := json.Unmarshal(env.Payload, payload); err != nil {
		return fmt.Errorf("%w: unreadable payload: %v", ErrCorruption, err)
	}
	return nil
}

// writeFileSync writes data to path, fsyncs, and closes. The caller is
// responsible for the rename + directory fsync that makes it durable.
func (f *FSM) writeFileSync(path string, data []byte) error {
	f.observe("write", path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	f.observe("sync", path)
	return file.Close()
}

// renameSync atomically renames src -> dst and fsyncs the parent directory.
// The directory fsync is what makes the rename itself durable across a
// power cut; without it the directory entry may be lost even though both
// file contents were fsynced.
func (f *FSM) renameSync(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	f.observe("rename", src+" -> "+dst)
	return f.syncDir()
}

func (f *FSM) removeSync(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f.observe("delete", path)
	return f.syncDir()
}

func (f *FSM) syncDir() error {
	dir, err := os.Open(f.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return err
	}
	f.observe("sync", f.dir)
	return nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func (f *FSM) manifestPath() string { return filepath.Join(f.dir, manifestFileName) }
func (f *FSM) journalPath() string  { return filepath.Join(f.dir, journalFileName) }
func (f *FSM) segmentPath(name string) string {
	return filepath.Join(f.dir, name)
}
