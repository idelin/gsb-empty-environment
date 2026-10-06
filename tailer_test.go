package logtail

import (
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func checkLines(t *testing.T, got, want []Line) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// A partial line is only published once its newline arrives.
func TestPartialLinePublishedOnlyAfterNewline(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	must(t, fs.Create("app.log", []byte("hel")))
	tl.Poll()
	if len(tl.Lines) != 0 {
		t.Fatalf("published unterminated line: %+v", tl.Lines)
	}
	must(t, fs.Append("app.log", []byte("lo\nwor")))
	tl.Poll()
	checkLines(t, tl.Lines, []Line{{Gen: 1, Ino: 1, Text: "hello"}})
	must(t, fs.Append("app.log", []byte("ld\n")))
	tl.Poll()
	checkLines(t, tl.Lines, []Line{{Gen: 1, Ino: 1, Text: "hello"}, {Gen: 1, Ino: 1, Text: "world"}})
}

// After rotation the old handle is drained to EOF (in the old
// generation) before the replacement file is read; an unterminated
// tail of the old file is dropped, never published.
func TestRotationDrainsOldHandleFirst(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	must(t, fs.Create("app.log", []byte("a\n")))
	tl.Poll()

	must(t, fs.Rename("app.log", "app.log.1"))
	must(t, fs.Append("app.log.1", []byte("b\nc"))) // writer flushing its old fd
	must(t, fs.Create("app.log", []byte("n1\nn2\n")))
	tl.Poll()

	checkLines(t, tl.Lines, []Line{
		{Gen: 1, Ino: 1, Text: "a"},
		{Gen: 1, Ino: 1, Text: "b"}, // drained from the old handle, old gen
		{Gen: 2, Ino: 2, Text: "n1"},
		{Gen: 2, Ino: 2, Text: "n2"},
	})
	if n := fs.OpenHandles(); n != 1 {
		t.Fatalf("old handle not closed after rotation: %d open", n)
	}
	must(t, tl.Close())
	if n := fs.OpenHandles(); n != 0 {
		t.Fatalf("handle leaked after close: %d open", n)
	}
}

// Truncation starts a new generation of the same file identity; old
// lines are not re-delivered.
func TestTruncateStartsNewGeneration(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	must(t, fs.Create("app.log", []byte("a\nb\n")))
	tl.Poll()
	must(t, fs.Truncate("app.log"))
	must(t, fs.Append("app.log", []byte("c\n")))
	tl.Poll()

	checkLines(t, tl.Lines, []Line{
		{Gen: 1, Ino: 1, Text: "a"},
		{Gen: 1, Ino: 1, Text: "b"},
		{Gen: 2, Ino: 1, Text: "c"}, // same Ino, new generation
	})
}

// A different file appearing at the same path is a new generation.
func TestReplaceSamePath(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	must(t, fs.Create("app.log", []byte("x\n")))
	tl.Poll()
	must(t, fs.Remove("app.log"))
	must(t, fs.Create("app.log", []byte("y\n")))
	tl.Poll()

	checkLines(t, tl.Lines, []Line{
		{Gen: 1, Ino: 1, Text: "x"},
		{Gen: 2, Ino: 2, Text: "y"},
	})
	if n := fs.OpenHandles(); n != 1 {
		t.Fatalf("replaced handle not closed: %d open", n)
	}
}

// Rename onto the watched path atomically replaces the file; the old
// handle is drained first.
func TestRenameOntoWatchedPath(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	must(t, fs.Create("app.log", []byte("old\n")))
	must(t, fs.Create("app.log.new", []byte("new\n")))
	tl.Poll()
	must(t, fs.Rename("app.log.new", "app.log"))
	tl.Poll()

	checkLines(t, tl.Lines, []Line{
		{Gen: 1, Ino: 1, Text: "old"},
		{Gen: 2, Ino: 2, Text: "new"},
	})
}

// runOps applies ops one at a time, polling after each, and returns the
// delivered lines plus the number of handles still open after Close.
func runOps(ops []Op) ([]Line, int) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	for _, op := range ops {
		if err := fs.Apply(op); err != nil {
			panic(err)
		}
		tl.Poll()
	}
	tl.Close()
	return tl.Lines, fs.OpenHandles()
}

// A recorded session replays to exactly the same delivered lines.
func TestSessionIsReplayable(t *testing.T) {
	fs := NewFS()
	tl := NewTailer(fs, "app.log")
	script := []Op{
		{Kind: OpCreate, Path: "app.log", Data: []byte("a\nb")},
		{Kind: OpAppend, Path: "app.log", Data: []byte("\n")},
		{Kind: OpRename, Path: "app.log", To: "app.log.1"},
		{Kind: OpAppend, Path: "app.log.1", Data: []byte("c\n")},
		{Kind: OpCreate, Path: "app.log", Data: []byte("d\n")},
		{Kind: OpTruncate, Path: "app.log"},
		{Kind: OpAppend, Path: "app.log", Data: []byte("e\n")},
		{Kind: OpRemove, Path: "app.log"},
	}
	for _, op := range script {
		must(t, fs.Apply(op))
		tl.Poll()
	}
	tl.Close()

	replayed, open := runOps(fs.Ops())
	if open != 0 {
		t.Fatalf("replayed session leaked %d handles", open)
	}
	if !reflect.DeepEqual(tl.Lines, replayed) {
		t.Fatalf("replay diverged\nlive:     %+v\nreplayed: %+v", tl.Lines, replayed)
	}
}
