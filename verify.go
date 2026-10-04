package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// parseSourceLine applies the input truncation rule shared by the merger and
// the verifier: a record is only valid if its line ends with '\n'. A trailing
// partial line (file truncated mid-record) is discarded.
func parseSourceLine(line []byte) (string, bool) {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return "", false
	}
	return string(line[:len(line)-1]), true
}

// readSourceRecords loads one source exactly the way the merger interprets it.
func readSourceRecords(path string, srcIdx int) (recs []Record, discardedBytes int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	seq := 0
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			if key, ok := parseSourceLine(line); ok {
				recs = append(recs, Record{Key: key, Source: srcIdx, Seq: seq})
				seq++
			} else {
				discardedBytes += len(line)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, 0, rerr
		}
	}
	return recs, discardedBytes, nil
}

func readOutputRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	var recs []Record
	lineNo := 0
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			s := strings.TrimSuffix(string(line), "\n")
			parts := strings.Split(s, "\t")
			if len(parts) != 3 {
				return nil, fmt.Errorf("output line %d: malformed %q", lineNo, s)
			}
			src, err1 := strconv.Atoi(parts[1])
			seq, err2 := strconv.Atoi(parts[2])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("output line %d: bad source/seq", lineNo)
			}
			recs = append(recs, Record{Key: parts[0], Source: src, Seq: seq})
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	return recs, nil
}

// Verify checks the output against a full in-memory sort of the sources:
//  1. output is strictly ordered by (key, source, seq);
//  2. output equals the full sort of all source records exactly (no loss, no dup);
//  3. per source, sequences cover 0..n-1 exactly (source offsets are consistent).
func Verify(outPath string, sources []string) error {
	outRecs, err := readOutputRecords(outPath)
	if err != nil {
		return err
	}
	for i := 1; i < len(outRecs); i++ {
		if !lessRec(outRecs[i-1], outRecs[i]) {
			return fmt.Errorf("output not strictly sorted at record %d: (%q,%d,%d) then (%q,%d,%d)",
				i, outRecs[i-1].Key, outRecs[i-1].Source, outRecs[i-1].Seq,
				outRecs[i].Key, outRecs[i].Source, outRecs[i].Seq)
		}
	}

	var expected []Record
	for i, src := range sources {
		recs, _, err := readSourceRecords(src, i)
		if err != nil {
			return err
		}
		expected = append(expected, recs...)
	}
	sort.Slice(expected, func(i, j int) bool { return lessRec(expected[i], expected[j]) })

	if len(outRecs) != len(expected) {
		return fmt.Errorf("record count mismatch: output has %d, full sort of sources has %d", len(outRecs), len(expected))
	}
	for i := range expected {
		if outRecs[i] != expected[i] {
			return fmt.Errorf("record %d mismatch: output (%q,%d,%d), expected (%q,%d,%d)",
				i, outRecs[i].Key, outRecs[i].Source, outRecs[i].Seq,
				expected[i].Key, expected[i].Source, expected[i].Seq)
		}
	}

	count := map[int]int{}
	maxSeq := map[int]int{}
	for _, r := range outRecs {
		count[r.Source]++
		if r.Seq > maxSeq[r.Source] {
			maxSeq[r.Source] = r.Seq
		}
	}
	for src, c := range count {
		if c != maxSeq[src]+1 {
			return fmt.Errorf("source %d: %d records but max sequence %d (gap or duplicate)", src, c, maxSeq[src])
		}
	}
	return nil
}
