package merge

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// ScanFile reads all complete records from a file. It returns the records,
// the offset just past the last complete record, and whether a truncated
// (partial) trailing record was found and skipped.
func ScanFile(path string) (recs []Record, endOffset int64, truncated bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, err
	}
	var off int64
	for off < int64(len(data)) {
		rec, n, derr := DecodeRecord(data[off:])
		if derr != nil {
			if errors.Is(derr, ErrIncomplete) {
				return recs, off, true, nil
			}
			return nil, 0, false, fmt.Errorf("%s at %d: %w", path, off, derr)
		}
		recs = append(recs, rec)
		off += int64(n)
	}
	return recs, off, false, nil
}

// Verify checks a merge output against a full, in-memory sort of all
// complete input records. If ck is non-nil it also checks that the
// checkpoint's confirmed offsets match reality: output offset/record count,
// and per-source consumed offsets/record counts. Any mismatch — a lost or
// duplicated record, or a wrong source offset — is an error.
func Verify(outputPath string, inputPaths []string, ck *Checkpoint) error {
	var want []Record
	perSrcOff := make([]int64, len(inputPaths))
	perSrcCnt := make([]uint64, len(inputPaths))
	for i, in := range inputPaths {
		recs, off, _, err := ScanFile(in)
		if err != nil {
			return err
		}
		want = append(want, recs...)
		perSrcOff[i] = off
		perSrcCnt[i] = uint64(len(recs))
	}
	sort.SliceStable(want, func(a, b int) bool { return Less(want[a], want[b]) })

	got, outOff, outTrunc, err := ScanFile(outputPath)
	if err != nil {
		return err
	}
	if outTrunc {
		return fmt.Errorf("output %s has a truncated tail", outputPath)
	}
	if len(got) != len(want) {
		return fmt.Errorf("record count mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !equalRecord(got[i], want[i]) {
			return fmt.Errorf("record %d mismatch: got (key=%q src=%d seq=%d), want (key=%q src=%d seq=%d)",
				i, got[i].Key, got[i].Source, got[i].Seq, want[i].Key, want[i].Source, want[i].Seq)
		}
	}
	if ck == nil {
		return nil
	}
	if !ck.Done {
		return fmt.Errorf("checkpoint not marked done")
	}
	if ck.OutputOffset != outOff {
		return fmt.Errorf("checkpoint output offset %d != actual %d", ck.OutputOffset, outOff)
	}
	if ck.OutputRecords != uint64(len(want)) {
		return fmt.Errorf("checkpoint output records %d != actual %d", ck.OutputRecords, len(want))
	}
	for i := range inputPaths {
		if ck.InputOffsets[i] != perSrcOff[i] {
			return fmt.Errorf("input %d: checkpoint offset %d != consumed offset %d",
				i, ck.InputOffsets[i], perSrcOff[i])
		}
		if ck.InputRecords[i] != perSrcCnt[i] {
			return fmt.Errorf("input %d: checkpoint records %d != consumed records %d",
				i, ck.InputRecords[i], perSrcCnt[i])
		}
	}
	return nil
}
