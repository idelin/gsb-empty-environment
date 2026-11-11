package imapsync

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func greeting() scriptStep {
	return scriptStep{send: []string{line(`* OK [CAPABILITY IMAP4rev1 CONDSTORE] ready`)}}
}

func selectStep(folder, uidValidity, highestModSeq string, exists int) scriptStep {
	return scriptStep{
		expect: `^SELECT "` + folder + `" \(CONDSTORE\)$`,
		send: []string{
			line(`* FLAGS (\Answered \Seen \Deleted)`),
			line(`* ` + strconv.Itoa(exists) + ` EXISTS`),
			line(`* OK [UIDVALIDITY ` + uidValidity + `] valid`),
			line(`* OK [HIGHESTMODSEQ ` + highestModSeq + `] high`),
			line(`{TAG} OK Select completed`),
		},
	}
}

func logoutStep() scriptStep {
	return scriptStep{
		expect: `^LOGOUT$`,
		send:   []string{line(`* BYE bye`), line(`{TAG} OK Logout completed`)},
	}
}

func newSyncer(dir string, d *mockDialer) *Syncer {
	return &Syncer{Dial: d.Dial, Store: &Store{Dir: dir}}
}

func TestFullThenIncrementalSync(t *testing.T) {
	dir := t.TempDir()
	d := &mockDialer{scripts: [][]scriptStep{
		// Connection 1: initial full sync.
		{
			greeting(),
			selectStep("INBOX", "1000", "500", 2),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\)$`,
				send: []string{
					line(`* 1 FETCH (UID 101 FLAGS (\Seen) MODSEQ (450))`),
					line(`* 2 FETCH (UID 102 FLAGS () MODSEQ (500))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 101 102`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
		// Connection 2: incremental sync; a new message arrives and UID 101
		// is expunged by another client in the middle of the fetch.
		{
			greeting(),
			selectStep("INBOX", "1000", "700", 2),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\) \(CHANGEDSINCE 500\)$`,
				chunk:  7, // exercise reassembly across fragmented reads
				send: []string{
					line(`* 2 FETCH (UID 102 FLAGS (\Seen \Answered) MODSEQ (650))`),
					line(`* 3 EXISTS`),
					line(`* 1 EXPUNGE`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				// Second round picks up the message that arrived mid-sync.
				expect: `^UID FETCH 1:\* \(FLAGS\) \(CHANGEDSINCE 700\)$`,
				send: []string{
					line(`* 3 FETCH (UID 103 FLAGS () MODSEQ (720))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 102 103`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
	}}
	s := newSyncer(dir, d)

	st, err := s.SyncFolder("INBOX")
	if err != nil {
		t.Fatalf("full sync: %v", err)
	}
	if st.UIDValidity != 1000 || st.ModSeq != 500 {
		t.Fatalf("after full sync: uidvalidity=%d modseq=%d", st.UIDValidity, st.ModSeq)
	}
	want := map[uint64][]string{101: {"\\Seen"}, 102: nil}
	if !reflect.DeepEqual(st.Flags, want) {
		t.Fatalf("after full sync: flags=%v want %v", st.Flags, want)
	}

	st, err = s.SyncFolder("INBOX")
	if err != nil {
		t.Fatalf("incremental sync: %v", err)
	}
	if st.ModSeq != 720 {
		t.Fatalf("after incremental sync: modseq=%d want 720", st.ModSeq)
	}
	want = map[uint64][]string{102: {"\\Seen", "\\Answered"}, 103: nil}
	if !reflect.DeepEqual(st.Flags, want) {
		t.Fatalf("after incremental sync: flags=%v want %v", st.Flags, want)
	}
	d.wait(t)
}

func TestUIDValidityChangeTriggersRebuild(t *testing.T) {
	dir := t.TempDir()
	d := &mockDialer{scripts: [][]scriptStep{
		{
			greeting(),
			selectStep("INBOX", "1000", "500", 1),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\)$`,
				send: []string{
					line(`* 1 FETCH (UID 101 FLAGS (\Seen) MODSEQ (500))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 101`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
		// Server reassigned UIDVALIDITY: cached UIDs are meaningless.
		{
			greeting(),
			selectStep("INBOX", "2000", "9000", 1),
			{
				// Full re-fetch, no CHANGEDSINCE.
				expect: `^UID FETCH 1:\* \(FLAGS\)$`,
				send: []string{
					line(`* 1 FETCH (UID 501 FLAGS (\Deleted) MODSEQ (9000))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 501`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
	}}
	s := newSyncer(dir, d)

	if _, err := s.SyncFolder("INBOX"); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	st, err := s.SyncFolder("INBOX")
	if err != nil {
		t.Fatalf("sync after uidvalidity change: %v", err)
	}
	if st.UIDValidity != 2000 || st.ModSeq != 9000 {
		t.Fatalf("uidvalidity=%d modseq=%d", st.UIDValidity, st.ModSeq)
	}
	want := map[uint64][]string{501: {"\\Deleted"}}
	if !reflect.DeepEqual(st.Flags, want) {
		t.Fatalf("flags=%v want %v (old UIDs must be discarded)", st.Flags, want)
	}
	d.wait(t)
}

func TestResumeAfterConnectionDrop(t *testing.T) {
	dir := t.TempDir()
	folders := []string{"INBOX", "Archive"}
	inboxFull := []scriptStep{
		greeting(),
		selectStep("INBOX", "10", "100", 1),
		{
			expect: `^UID FETCH 1:\* \(FLAGS\)$`,
			send: []string{
				line(`* 1 FETCH (UID 1 FLAGS (\Seen) MODSEQ (100))`),
				line(`{TAG} OK Fetch completed`),
			},
		},
		{
			expect: `^UID SEARCH ALL$`,
			send:   []string{line(`* SEARCH 1`), line(`{TAG} OK Search completed`)},
		},
		logoutStep(),
	}
	inboxIncr := []scriptStep{
		greeting(),
		selectStep("INBOX", "10", "100", 1),
		{
			expect: `^UID FETCH 1:\* \(FLAGS\) \(CHANGEDSINCE 100\)$`,
			send:   []string{line(`{TAG} OK Fetch completed`)},
		},
		{
			expect: `^UID SEARCH ALL$`,
			send:   []string{line(`* SEARCH 1`), line(`{TAG} OK Search completed`)},
		},
		logoutStep(),
	}
	d := &mockDialer{scripts: [][]scriptStep{
		inboxFull,
		// Archive: initial full sync.
		{
			greeting(),
			selectStep("Archive", "20", "300", 1),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\)$`,
				send: []string{
					line(`* 1 FETCH (UID 7 FLAGS (\Seen) MODSEQ (300))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 7`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
		inboxIncr,
		// Archive: connection drops in the middle of the incremental fetch.
		{
			greeting(),
			selectStep("Archive", "20", "400", 2),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\) \(CHANGEDSINCE 300\)$`,
				send:   []string{line(`* 2 FETCH (UID 8 FLAGS () MODSEQ (400))`)},
				drop:   true,
			},
		},
		inboxIncr,
		// Archive: resumed sync picks up from the saved MODSEQ 300.
		{
			greeting(),
			selectStep("Archive", "20", "400", 2),
			{
				expect: `^UID FETCH 1:\* \(FLAGS\) \(CHANGEDSINCE 300\)$`,
				send: []string{
					line(`* 2 FETCH (UID 8 FLAGS () MODSEQ (400))`),
					line(`{TAG} OK Fetch completed`),
				},
			},
			{
				expect: `^UID SEARCH ALL$`,
				send:   []string{line(`* SEARCH 7 8`), line(`{TAG} OK Search completed`)},
			},
			logoutStep(),
		},
	}}
	s := newSyncer(dir, d)

	done, err := s.SyncAll(folders)
	if err != nil || len(done) != 2 {
		t.Fatalf("first SyncAll: done=%v err=%v", done, err)
	}

	done, err = s.SyncAll(folders)
	if err == nil {
		t.Fatal("second SyncAll: expected error from dropped connection")
	}
	if len(done) != 1 || done[0] != "INBOX" {
		t.Fatalf("second SyncAll: done=%v, want [INBOX]", done)
	}

	// The interrupted folder's state must be untouched: MODSEQ 300, UID 7
	// only, no partial writes, no leftover temp files.
	st, err := s.Store.Load("Archive")
	if err != nil {
		t.Fatalf("load Archive state: %v", err)
	}
	if st.ModSeq != 300 || !reflect.DeepEqual(st.Flags, map[uint64][]string{7: {"\\Seen"}}) {
		t.Fatalf("Archive state after drop: %+v", st)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}

	done, err = s.SyncAll(folders)
	if err != nil || len(done) != 2 {
		t.Fatalf("resumed SyncAll: done=%v err=%v", done, err)
	}
	st, err = s.Store.Load("Archive")
	if err != nil {
		t.Fatalf("load Archive state: %v", err)
	}
	want := map[uint64][]string{7: {"\\Seen"}, 8: nil}
	if st.ModSeq != 400 || !reflect.DeepEqual(st.Flags, want) {
		t.Fatalf("Archive state after resume: %+v", st)
	}
	if d.calls != 6 {
		t.Fatalf("connections used: %d, want 6", d.calls)
	}
	d.wait(t)
}

func TestStoreAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := &Store{Dir: filepath.Join(dir, "state")}
	st := &FolderState{UIDValidity: 42, ModSeq: 99, Flags: map[uint64][]string{5: {"\\Seen"}}}
	if err := store.SaveAtomic("Folder/With/Slashes", st); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
	got, err := store.Load("Folder/With/Slashes")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("got %+v want %+v", got, st)
	}
	missing, err := store.Load("never-synced")
	if err != nil || missing != nil {
		t.Fatalf("missing folder: %v %v", missing, err)
	}
	entries, _ := os.ReadDir(store.Dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}
