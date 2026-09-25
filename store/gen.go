package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// Gen creates a deterministic sample store in dir: two segments with
// a few keys (one key overwritten in the later segment), a manifest
// and a consistent index. Used by the CLI demo and by tests.
func Gen(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type rec struct{ k, v string }
	segments := []struct {
		name string
		recs []rec
	}{
		{"seg-0001.dat", []rec{{"alpha", "1"}, {"beta", "2"}, {"gamma", "3"}}},
		{"seg-0002.dat", []rec{{"beta", "22"}, {"delta", "4"}}},
	}
	var m Manifest
	m.Version = 1
	idx := &Index{Version: 1, Segments: map[string]ManifestSegment{}, Entries: map[string]IndexEntry{}}
	for _, seg := range segments {
		var data []byte
		for _, r := range seg.recs {
			off := int64(len(data))
			rec := encodeRecord([]byte(r.k), []byte(r.v))
			data = append(data, rec...)
			idx.Entries[r.k] = IndexEntry{Segment: seg.name, Offset: off, Length: int64(len(rec))}
		}
		path := filepath.Join(dir, seg.name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		sum, size, err := sha256File(path)
		if err != nil {
			return err
		}
		ms := ManifestSegment{Name: seg.name, Size: size, SHA256: sum}
		m.Segments = append(m.Segments, ms)
		idx.Segments[seg.name] = ms
	}
	mdata, err := marshalJSON(&m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), mdata, 0o644); err != nil {
		return err
	}
	idata, err := marshalJSON(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, IndexName), idata, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "generated sample store in %s (%d segments, %d keys)\n", dir, len(m.Segments), len(idx.Entries))
	return nil
}
