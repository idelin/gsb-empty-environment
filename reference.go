package extsort

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"sort"
)

// ReferenceSort sorts the input segments in one pass in memory with a
// stable sort and returns the expected final output bytes. It is the
// ground truth the external merge is checked against.
func ReferenceSort(inputs []string) ([]byte, error) {
	var recs []Record
	var seq uint64
	for _, in := range inputs {
		f, err := os.Open(in)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			key, value := splitLine(sc.Bytes())
			recs = append(recs, Record{
				Key:   string(key),
				Seq:   seq,
				Value: append([]byte(nil), value...),
			})
			seq++
		}
		if err := sc.Err(); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
	}
	// Stable sort on the key alone: ties keep original input order, which
	// is exactly ascending Seq — equivalent to comparing (key, seq).
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Key < recs[j].Key })
	var out bytes.Buffer
	for i := range recs {
		out.Write(encodeRecord(&recs[i]))
	}
	return out.Bytes(), nil
}

// DecodeFile decodes a run/output file back into records (test and CLI use).
func DecodeFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := newRecordDecoder(f, 0)
	var recs []Record
	for {
		rec, err := dec.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		recs = append(recs, *rec)
	}
	return recs, nil
}
