package auditlog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const (
	sealedExt = ".log"
	openExt   = ".part"

	// maxPayloadSize bounds memory while authenticating a single record.
	maxPayloadSize = 64 << 20
)

var segmentNameRE = regexp.MustCompile(`^segment-([0-9]{16})\.(log|part)$`)

type parsedSegment struct {
	status          SegmentStatus
	records         []Record
	firstPrevDigest []byte
	lastDigest      []byte
	sealPrevDigest  []byte
	sealDigest      []byte
	torn            *TornTail
}

type byteReader struct {
	data   []byte
	offset int64
}

func (r *byteReader) read(size int) ([]byte, error) {
	if r.offset == int64(len(r.data)) && size > 0 {
		return nil, errCleanEOF
	}
	end := r.offset + int64(size)
	if end > int64(len(r.data)) {
		r.offset = int64(len(r.data))
		return nil, errTorn
	}
	buf := r.data[r.offset:end]
	r.offset = end
	return buf, nil
}

// ReadDir verifies every segment under dir and returns the authenticated
// records together with torn-tail evidence. It never creates or modifies
// files.
func ReadDir(ctx context.Context, dir string, keys Keys) (*Report, error) {
	if err := keys.validate(false); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type nameInfo struct {
		index uint64
		part  bool
		name  string
	}

	names := make([]nameInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := segmentNameRE.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, tamper(TamperOrder, 0, 0, entry.Name(), 0,
				"unrecognized file in log directory")
		}
		names = append(names, nameInfo{
			index: parseDecimalIndex(match[1]),
			part:  match[2] == "part",
			name:  entry.Name(),
		})
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].index != names[j].index {
			return names[i].index < names[j].index
		}
		return names[i].name < names[j].name
	})

	seen := make(map[uint64]string)
	parsed := make([]parsedSegment, 0, len(names))
	for i, info := range names {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if other, ok := seen[info.index]; ok {
			return nil, tamper(TamperOrder, info.index, 0, info.name, 0,
				fmt.Sprintf("duplicate segment index also used by %q", other))
		}
		seen[info.index] = info.name

		segment, err := parseSegment(
			filepath.Join(dir, info.name),
			info.index,
			info.part,
			i == len(names)-1,
			keys,
		)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, segment)
	}

	return verifyChain(parsed)
}

func parseDecimalIndex(text string) uint64 {
	var index uint64
	for i := 0; i < len(text); i++ {
		index = index*10 + uint64(text[i]-'0')
	}
	return index
}
