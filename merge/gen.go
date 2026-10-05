package merge

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// GenerateInputs writes nSrc sorted run files into dir and returns their
// paths. Each run holds recsPerSrc records with random keys (zero-padded so
// lexicographic order matches numeric order), sorted by key, with sequence
// numbers assigned 0..n-1 in sorted order and random payloads.
func GenerateInputs(dir string, nSrc, recsPerSrc, maxKey, maxPayload int, rng *rand.Rand) ([]string, error) {
	if maxKey < 1 {
		maxKey = 1
	}
	width := len(strconv.Itoa(maxKey - 1))
	var paths []string
	for s := 0; s < nSrc; s++ {
		recs := make([]Record, recsPerSrc)
		for i := range recs {
			pl := make([]byte, rng.Intn(maxPayload+1))
			rng.Read(pl)
			recs[i] = Record{
				Key:     fmt.Sprintf("k%0*d", width, rng.Intn(maxKey)),
				Source:  uint32(s),
				Payload: pl,
			}
		}
		sort.SliceStable(recs, func(a, b int) bool { return recs[a].Key < recs[b].Key })
		for i := range recs {
			recs[i].Seq = uint64(i)
		}
		path := filepath.Join(dir, fmt.Sprintf("input-%03d.run", s))
		var buf []byte
		for _, r := range recs {
			buf = AppendRecord(buf, r)
		}
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}
