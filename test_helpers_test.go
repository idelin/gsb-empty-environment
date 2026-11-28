package lsm

import (
	"fmt"
	"testing"
)

func prescreenEntry(key string, seq SeqNum, value string) Entry {
	return Entry{Key: []byte(key), Seq: seq, Kind: KindPut, Value: []byte(value)}
}

func prescreenDelete(key string, seq SeqNum) Entry {
	return Entry{Key: []byte(key), Seq: seq, Kind: KindDelete}
}

func prescreenFile(id uint64, level int, size uint64, entries ...Entry) *FileMeta {
	f := &FileMeta{FileID: id, Level: level, Size: size, Entries: entries}
	f.Normalize()
	return f
}

func prescreenFileWithTombstone(id uint64, level int, size uint64, tomb RangeTombstone, entries ...Entry) *FileMeta {
	f := prescreenFile(id, level, size, entries...)
	f.RangeTombstones = []RangeTombstone{tomb}
	return f
}

func prescreenRequireState(t *testing.T, got, want WriteStallState) {
	t.Helper()
	if got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
}

func prescreenRequireValue(t *testing.T, files []*FileMeta, key string, seq SeqNum, want string) {
	t.Helper()
	val, found, deleted := Lookup(files, []byte(key), seq)
	if deleted {
		t.Fatalf("lookup %q at %d: unexpectedly deleted", key, seq)
	}
	if !found {
		t.Fatalf("lookup %q at %d: not found, want %q", key, seq, want)
	}
	if string(val) != want {
		t.Fatalf("lookup %q at %d = %q, want %q", key, seq, val, want)
	}
}

func prescreenRequireDeleted(t *testing.T, files []*FileMeta, key string, seq SeqNum) {
	t.Helper()
	_, found, deleted := Lookup(files, []byte(key), seq)
	if !found || !deleted {
		t.Fatalf("lookup %q at %d: found=%v deleted=%v, want deleted", key, seq, found, deleted)
	}
}

func prescreenDumpFiles(files []*FileMeta) string {
	out := ""
	for _, f := range files {
		out += fmt.Sprintf("file %d level %d [%s,%s] entries=%d tombs=%d\n",
			f.FileID, f.Level, f.Smallest.UserKey, f.Largest.UserKey,
			len(f.Entries), len(f.RangeTombstones))
	}
	return out
}
