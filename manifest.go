package manifestmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Segment describes one immutable data segment.
type Segment struct {
	ID     string `json:"id"`     // "seg-<hex>" content-addressed identifier
	Digest string `json:"digest"` // hex SHA-256 of the segment file content
	Size   int64  `json:"size"`   // content length in bytes
}

// Record is one committed version entry in the manifest log. It records the
// version number, the segments added and removed by this version, and the
// full resulting segment table with per-segment content digests.
type Record struct {
	Version    uint64    `json:"version"`
	PrevDigest string    `json:"prev_digest"` // SHA-256 of the previous record's payload; "" for v1
	Added      []Segment `json:"added"`
	Removed    []string  `json:"removed"`
	Segments   []Segment `json:"segments"` // full current set, sorted by ID
}

// SegmentID derives the content-addressed ID for a blob.
func SegmentID(content []byte) string {
	sum := sha256.Sum256(content)
	return "seg-" + hex.EncodeToString(sum[:8])
}

// DigestOf returns the hex SHA-256 of content.
func DigestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// segmentSet is a helper view of a segment list.
func segmentSet(segs []Segment) map[string]Segment {
	m := make(map[string]Segment, len(segs))
	for _, s := range segs {
		m[s.ID] = s
	}
	return m
}

func sortedSegments(m map[string]Segment) []Segment {
	out := make([]Segment, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// buildRecord computes the next record from the current one.
func buildRecord(prev *Record, adds []Segment, removes []string, prevDigest string) (*Record, error) {
	cur := map[string]Segment{}
	version := uint64(1)
	if prev != nil {
		cur = segmentSet(prev.Segments)
		version = prev.Version + 1
	}
	var added []Segment
	for _, s := range adds {
		if existing, ok := cur[s.ID]; ok {
			if existing.Digest != s.Digest {
				return nil, fmt.Errorf("segment %s digest conflict", s.ID)
			}
			continue // already present, not an add
		}
		cur[s.ID] = s
		added = append(added, s)
	}
	var removed []string
	for _, id := range removes {
		if _, ok := cur[id]; !ok {
			return nil, fmt.Errorf("cannot remove unknown segment %s", id)
		}
		delete(cur, id)
		removed = append(removed, id)
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	sort.Strings(removed)
	return &Record{
		Version:    version,
		PrevDigest: prevDigest,
		Added:      added,
		Removed:    removed,
		Segments:   sortedSegments(cur),
	}, nil
}
