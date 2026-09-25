// Package store implements a read-only three-way consistency audit
// (manifest / segment files / index) plus an executable, resumable
// repair plan. Only the standard library is used.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// File layout inside a store directory:
//   manifest.json  - authoritative list of segments with size+sha256
//   seg-*.dat      - append-only record logs
//   index.json     - derived key->location index (rebuildable from segments)

const (
	ManifestName = "manifest.json"
	IndexName    = "index.json"
	JournalName  = ".apply.journal"
	tmpPrefix    = ".tmp-"
)

// Segment record wire format (append-only log):
//
//	u32 crc32(keyLen|valLen|key|value)
//	u32 keyLen
//	u32 valLen
//	key bytes, value bytes
const recordHeaderLen = 12

// Manifest is the authoritative description of the segment set.
type Manifest struct {
	Version  int               `json:"version"`
	Segments []ManifestSegment `json:"segments"`
}

type ManifestSegment struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Index maps keys to record locations. It is fully derived from the
// segments and therefore rebuildable as long as segments verify.
type Index struct {
	Version  int                        `json:"version"`
	Segments map[string]ManifestSegment `json:"segments"`
	Entries  map[string]IndexEntry      `json:"entries"`
}

type IndexEntry struct {
	Segment string `json:"segment"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
}

func encodeRecord(key, value []byte) []byte {
	buf := make([]byte, recordHeaderLen+len(key)+len(value))
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(key)))
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(value)))
	copy(buf[12:], key)
	copy(buf[12+len(key):], value)
	binary.BigEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(buf[4:]))
	return buf
}

// decodeRecordAt reads and validates one record at offset off.
// Returns key, value, total record length.
func decodeRecordAt(r io.ReaderAt, off int64) (key, value []byte, length int64, err error) {
	hdr := make([]byte, recordHeaderLen)
	if _, err = r.ReadAt(hdr, off); err != nil {
		return nil, nil, 0, fmt.Errorf("record header at %d: %v", off, err)
	}
	crc := binary.BigEndian.Uint32(hdr[0:4])
	keyLen := binary.BigEndian.Uint32(hdr[4:8])
	valLen := binary.BigEndian.Uint32(hdr[8:12])
	if keyLen > 1<<20 || valLen > 1<<30 {
		return nil, nil, 0, fmt.Errorf("record at %d: implausible lengths k=%d v=%d", off, keyLen, valLen)
	}
	body := make([]byte, 8+int64(keyLen)+int64(valLen))
	copy(body, hdr[4:])
	if _, err = r.ReadAt(body[8:], off+recordHeaderLen); err != nil {
		return nil, nil, 0, fmt.Errorf("record body at %d: %v", off, err)
	}
	if crc32.ChecksumIEEE(body) != crc {
		return nil, nil, 0, fmt.Errorf("record at %d: crc mismatch", off)
	}
	key = body[8 : 8+keyLen]
	value = body[8+keyLen:]
	return key, value, recordHeaderLen + int64(keyLen) + int64(valLen), nil
}

// scanSegment decodes every record in a segment file in order.
func scanSegment(path string, fn func(off, length int64, key, value []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var off int64
	for off < st.Size() {
		key, value, length, err := decodeRecordAt(f, off)
		if err != nil {
			return fmt.Errorf("%s: %v", filepath.Base(path), err)
		}
		if err := fn(off, length, key, value); err != nil {
			return err
		}
		off += length
	}
	if off != st.Size() {
		return fmt.Errorf("%s: trailing bytes", filepath.Base(path))
	}
	return nil
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %v", filepath.Base(path), err)
	}
	return nil
}

// marshalJSON produces deterministic JSON (sorted keys, indented).
func marshalJSON(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFileAtomic writes data to a temp file in dir, fsyncs it,
// atomically renames it onto target, and fsyncs the directory.
// Fault injection points surround each durability boundary.
func writeFileAtomic(dir, target string, data []byte, stepID string) error {
	tmp, err := os.CreateTemp(dir, tmpPrefix)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := runFault(FaultBeforeRename, stepID); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, target)); err != nil {
		return err
	}
	if err := runFault(FaultAfterRename, stepID); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// cleanTemps removes leftover temp files from interrupted applies.
func cleanTemps(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if len(e.Name()) >= len(tmpPrefix) && e.Name()[:len(tmpPrefix)] == tmpPrefix {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
