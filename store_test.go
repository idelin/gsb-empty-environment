package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	var buf []byte
	keys := []string{"alpha", "beta", "gamma"}
	for i, k := range keys {
		buf = append(buf, encodeRecord([]byte(k), []byte("val-"+k))...)
		_ = i
	}
	recs, corrupt := scanSegment(buf)
	if corrupt {
		t.Fatal("clean buffer reported as corrupt")
	}
	if len(recs) != len(keys) {
		t.Fatalf("got %d records, want %d", len(recs), len(keys))
	}
	off := int64(0)
	for i, r := range recs {
		if r.Key != keys[i] {
			t.Fatalf("record %d key = %q, want %q", i, r.Key, keys[i])
		}
		if r.Offset != off {
			t.Fatalf("record %d offset = %d, want %d", i, r.Offset, off)
		}
		off += r.Length
	}
}

func TestScanDetectsTruncation(t *testing.T) {
	buf := append(encodeRecord([]byte("a"), []byte("1")), encodeRecord([]byte("b"), []byte("2"))...)
	truncated := buf[:len(buf)-3]
	recs, corrupt := scanSegment(truncated)
	if !corrupt {
		t.Fatal("truncated buffer not flagged corrupt")
	}
	if len(recs) != 1 || recs[0].Key != "a" {
		t.Fatalf("got %+v, want only record a", recs)
	}
}

func TestScanDetectsBitRot(t *testing.T) {
	buf := encodeRecord([]byte("a"), []byte("payload"))
	buf[len(buf)-1] ^= 0xff
	if recs, corrupt := scanSegment(buf); !corrupt || len(recs) != 0 {
		t.Fatalf("bit rot not detected: recs=%d corrupt=%v", len(recs), corrupt)
	}
}

func TestStateSummaryDeterministicAndSensitive(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "s1")
	dir2 := filepath.Join(t.TempDir(), "s2")
	if err := Gen(dir1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if err := Gen(dir2, 2, 4); err != nil {
		t.Fatal(err)
	}
	sum1, err := StateSummary(dir1)
	if err != nil {
		t.Fatal(err)
	}
	sum2, err := StateSummary(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if sum1 != sum2 {
		t.Fatal("identical stores produced different summaries")
	}
	f, err := os.OpenFile(segmentPath(dir2, "seg-000001.dat"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff}, 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sum3, err := StateSummary(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if sum3 == sum1 {
		t.Fatal("summary did not change after segment modification")
	}
	if err := os.Remove(indexPath(dir1)); err != nil {
		t.Fatal(err)
	}
	sum4, err := StateSummary(dir1)
	if err != nil {
		t.Fatal(err)
	}
	if sum4 == sum1 {
		t.Fatal("summary did not change after index removal")
	}
}
