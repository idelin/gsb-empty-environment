package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Gen creates a consistent demo store: numSegments segment files with
// keysPerSegment records each, an index, and a manifest covering both.
func Gen(dir string, numSegments, keysPerSegment int) error {
	if err := os.MkdirAll(filepath.Join(dir, segmentsDir), 0o755); err != nil {
		return err
	}
	manifest := &Manifest{Version: 1}
	segData := map[string][]byte{}
	var names []string
	keyNo := 0
	for s := 0; s < numSegments; s++ {
		name := fmt.Sprintf("seg-%06d.dat", s+1)
		var buf []byte
		for k := 0; k < keysPerSegment; k++ {
			key := fmt.Sprintf("key-%08d", keyNo)
			val := fmt.Sprintf("value-%08d-seg-%d", keyNo, s+1)
			buf = append(buf, encodeRecord([]byte(key), []byte(val))...)
			keyNo++
		}
		if err := os.WriteFile(segmentPath(dir, name), buf, 0o644); err != nil {
			return err
		}
		manifest.Segments = append(manifest.Segments, SegmentMeta{
			Name: name, Size: int64(len(buf)), SHA256: sha256Hex(buf),
		})
		segData[name] = buf
		names = append(names, name)
	}
	idxData, err := json.MarshalIndent(buildIndex(segData, names), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(indexPath(dir), idxData, 0o644); err != nil {
		return err
	}
	manifest.Index = &IndexMeta{Name: indexName, Size: int64(len(idxData)), SHA256: sha256Hex(idxData)}
	mData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(dir), mData, 0o644)
}
