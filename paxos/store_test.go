package paxos

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tmpStore(t *testing.T, syncWrites bool) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.bin")
	s, err := OpenStore(path, syncWrites)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreRoundTrip(t *testing.T) {
	s, path := tmpStore(t, true)
	want := []Entry{
		{Term: 1, Node: 0, Index: 1, Cmd: []byte("a")},
		{Term: 1, Node: 0, Index: 2, Cmd: []byte("b")},
		{Term: 2, Node: 3, Index: 2, Cmd: []byte("b2")}, // overwrite slot 2
		{Term: 2, Node: 3, Index: 3, Cmd: bytes.Repeat([]byte{7}, 100000)},
	}
	for _, e := range want {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := OpenStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Index != want[i].Index || got[i].Term != want[i].Term ||
			got[i].Node != want[i].Node || !bytes.Equal(got[i].Cmd, want[i].Cmd) {
			t.Fatalf("record %d mismatch: %+v vs %+v", i, got[i], want[i])
		}
	}
	// LoadLog collapses slot 2 to the higher-ballot record.
	log, err := s2.LoadLog()
	if err != nil {
		t.Fatal(err)
	}
	if string(log[2].Cmd) != "b2" || log[2].Term != 2 {
		t.Fatalf("slot 2 = %+v, want term-2 overwrite", log[2])
	}
}

func TestStoreViewIsZeroCopy(t *testing.T) {
	s, _ := tmpStore(t, false)
	defer s.Close()
	if err := s.Append(Entry{Term: 1, Node: 1, Index: 1, Cmd: []byte("zero-copy")}); err != nil {
		t.Fatal(err)
	}
	// Map the file directly and prove DecodeRecord aliases the mapped page.
	fi, err := os.Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer fi.Close()
	st, _ := fi.Stat()
	buf, unmap, err := mapFile(fi, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer unmap()
	e, _, err := DecodeRecord(buf[HeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	cmdOff := HeaderSize + RecordHdrSize + payloadMin
	if &e.Cmd[0] != &buf[cmdOff] {
		t.Fatal("decoded Cmd does not alias the mapped buffer")
	}
	// View must expose the same data without copying errors.
	var seen int
	err = s.View(func(_ int64, v Entry) error {
		seen++
		if string(v.Cmd) != "zero-copy" {
			t.Fatalf("view cmd = %q", v.Cmd)
		}
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatalf("view err=%v seen=%d", err, seen)
	}
}

func TestStoreTruncatesTornTail(t *testing.T) {
	s, path := tmpStore(t, false)
	for i := uint64(1); i <= 3; i++ {
		if err := s.Append(Entry{Term: 1, Node: 0, Index: i, Cmd: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	goodSize := s.Size()
	s.Close()

	// Simulate a crash mid-write: half a record at the tail.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	torn := AppendRecord(nil, Entry{Term: 1, Node: 0, Index: 4, Cmd: []byte("torn-write")})
	if _, err := f.Write(torn[:len(torn)/2]); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s2, err := OpenStore(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Size() != goodSize {
		t.Fatalf("size %d, want truncated %d", s2.Size(), goodSize)
	}
	got, err := s2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d records, want 3", len(got))
	}
	// The store remains appendable after truncation.
	if err := s2.Append(Entry{Term: 2, Node: 1, Index: 4, Cmd: []byte("ok")}); err != nil {
		t.Fatal(err)
	}
	log, _ := s2.LoadLog()
	if string(log[4].Cmd) != "ok" {
		t.Fatalf("slot 4 = %q", log[4].Cmd)
	}
}

func TestStoreRejectsBadHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.bin")
	if err := os.WriteFile(path, []byte("NOTAPAXOSLOG....."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, false); err != ErrBadMagic {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
}

func TestStoreConcurrentAppendAndView(t *testing.T) {
	s, _ := tmpStore(t, false)
	defer s.Close()
	const writers = 8
	const perWriter = 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				e := Entry{
					Term:  uint64(i + 1),
					Node:  uint32(w),
					Index: uint64(w*perWriter + i + 1),
					Cmd:   []byte(fmt.Sprintf("w%d-%d", w, i)),
				}
				if err := s.Append(e); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	// Concurrent readers mapping the file while it grows.
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := 0; r < 2; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.View(func(_ int64, e Entry) error { return nil }); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	log, err := s.LoadLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != writers*perWriter {
		t.Fatalf("recovered %d slots, want %d", len(log), writers*perWriter)
	}
}

func TestStoreLoadLogKeepsHighestBallot(t *testing.T) {
	s, _ := tmpStore(t, false)
	defer s.Close()
	// Same slot appended with ballots (1,0), (3,1), (2,1): (3,1) must win.
	for _, e := range []Entry{
		{Term: 1, Node: 0, Index: 5, Cmd: []byte("old")},
		{Term: 3, Node: 1, Index: 5, Cmd: []byte("new")},
		{Term: 2, Node: 1, Index: 5, Cmd: []byte("mid")},
	} {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	log, err := s.LoadLog()
	if err != nil {
		t.Fatal(err)
	}
	if string(log[5].Cmd) != "new" || log[5].Term != 3 {
		t.Fatalf("slot 5 = %+v, want highest ballot", log[5])
	}
}
