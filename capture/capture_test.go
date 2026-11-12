package capture

import (
	"bytes"
	"strings"
	"testing"
)

func writeString(t *testing.T, c *Capture, s string) {
	t.Helper()
	if n, err := c.Write([]byte(s)); err != nil || n != len(s) {
		t.Fatalf("Write(%q) = %d, %v; want %d, nil", s, n, err, len(s))
	}
}

func TestNewDefaults(t *testing.T) {
	c := New(Config{})
	if c.maxTotal != defaultMaxTotalBytes || c.maxLine != defaultMaxLineBytes {
		t.Fatalf("defaults = %d/%d, want %d/%d",
			c.maxTotal, c.maxLine, defaultMaxTotalBytes, defaultMaxLineBytes)
	}
}

func TestWriteSplitsLines(t *testing.T) {
	c := New(Config{MaxTotalBytes: 1024, MaxLineBytes: 1024})
	writeString(t, c, "alpha\nbeta\ngamma")
	c.Close()

	snap := c.Snapshot()
	got := make([]string, len(snap.Lines))
	for i, line := range snap.Lines {
		got[i] = string(line.Data)
	}
	want := []string{"alpha", "beta", "gamma"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %v, want %v", got, want)
	}
	if snap.ReceivedBytes != int64(len("alpha\nbeta\ngamma")) {
		t.Fatalf("ReceivedBytes = %d", snap.ReceivedBytes)
	}
}

func TestWriteFragmentsAcrossWrites(t *testing.T) {
	c := New(Config{MaxTotalBytes: 1024, MaxLineBytes: 1024})
	writeString(t, c, "sp")
	writeString(t, c, "lit ")
	writeString(t, c, "line\nnext")
	writeString(t, c, "tail\n")
	c.Close()

	snap := c.Snapshot()
	if len(snap.Lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(snap.Lines), snap.Lines)
	}
	if string(snap.Lines[0].Data) != "split line" || snap.Lines[0].Truncated {
		t.Fatalf("line 0 = %q truncated=%v", snap.Lines[0].Data, snap.Lines[0].Truncated)
	}
	if string(snap.Lines[1].Data) != "nexttail" || snap.Lines[1].Truncated {
		t.Fatalf("line 1 = %q truncated=%v", snap.Lines[1].Data, snap.Lines[1].Truncated)
	}
}

func TestLineCapMarksTruncationButDrains(t *testing.T) {
	const lineCap = 16
	c := New(Config{MaxTotalBytes: 1024, MaxLineBytes: lineCap})

	head := strings.Repeat("H", lineCap)
	tail := strings.Repeat("X", 100)
	writeString(t, c, head+tail+"\n")
	writeString(t, c, "ok\n")
	c.Close()

	snap := c.Snapshot()
	if len(snap.Lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(snap.Lines))
	}
	first := snap.Lines[0]
	if string(first.Data) != head || !first.Truncated {
		t.Fatalf("first line = %q truncated=%v, want %q true", first.Data, first.Truncated, head)
	}
	if snap.TruncatedLines != 1 {
		t.Fatalf("TruncatedLines = %d, want 1", snap.TruncatedLines)
	}
	if snap.DroppedLineBytes != int64(len(tail)) {
		t.Fatalf("DroppedLineBytes = %d, want %d", snap.DroppedLineBytes, len(tail))
	}
	if string(snap.Lines[1].Data) != "ok" || snap.Lines[1].Truncated {
		t.Fatalf("second line should be intact, got %+v", snap.Lines[1])
	}
	// Newline and the discarded overflow were all observed.
	if snap.ReceivedBytes != int64(lineCap+len(tail)+1+3) {
		t.Fatalf("ReceivedBytes = %d", snap.ReceivedBytes)
	}
}

func TestTotalCapKeepsTail(t *testing.T) {
	// Lines are 3 bytes each ("Lxx"); retain roughly the last three.
	c := New(Config{MaxTotalBytes: 10, MaxLineBytes: 1024})
	for i := 0; i < 10; i++ {
		writeString(t, c, "L"+twoDigits(i)+"\n")
	}
	c.Close()

	snap := c.Snapshot()
	if !snap.TotalTruncated {
		t.Fatal("TotalTruncated = false, want true")
	}
	if snap.RetainedBytes > 10 {
		t.Fatalf("RetainedBytes = %d exceeds cap 10", snap.RetainedBytes)
	}
	// The youngest lines must survive.
	last := snap.Lines[len(snap.Lines)-1]
	if string(last.Data) != "L09" {
		t.Fatalf("last retained line = %q, want L09", last.Data)
	}
	if snap.DroppedTotalBytes <= 0 {
		t.Fatal("DroppedTotalBytes = 0, want > 0")
	}
	if snap.ReceivedBytes != 40 {
		t.Fatalf("ReceivedBytes = %d, want 40", snap.ReceivedBytes)
	}
}

func twoDigits(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestCRLFStripped(t *testing.T) {
	c := New(Config{MaxTotalBytes: 1024, MaxLineBytes: 1024})
	writeString(t, c, "win\r\nnix\n")
	c.Close()

	snap := c.Snapshot()
	if len(snap.Lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(snap.Lines))
	}
	if string(snap.Lines[0].Data) != "win" || string(snap.Lines[1].Data) != "nix" {
		t.Fatalf("lines = %q, %q", snap.Lines[0].Data, snap.Lines[1].Data)
	}
}

func TestWriteAfterClose(t *testing.T) {
	c := New(Config{})
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done channel not closed after Close")
	}
	if _, err := c.Write([]byte("x")); err != ErrClosed {
		t.Fatalf("Write after Close err = %v, want ErrClosed", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
}

func TestWriterNeverBlocksAndStaysBounded(t *testing.T) {
	const (
		totalCap = 1024
		lineCap  = 64
		streams  = 200_000
	)
	c := New(Config{MaxTotalBytes: totalCap, MaxLineBytes: lineCap})
	block := make(chan struct{})
	go func() {
		defer close(block)
		chunk := bytes.Repeat([]byte("Z"), 1024)
		for i := 0; i < streams; i++ {
			if _, err := c.Write(chunk); err != nil {
				return
			}
			if i%4096 == 0 {
				// Newline occasionally to exercise line rotation.
				_, _ = c.Write([]byte("\n"))
			}
		}
	}()

	select {
	case <-block:
	case <-timeoutAfter():
		t.Fatal("Write blocked under unbounded producer")
	}
	c.Close()

	snap := c.Snapshot()
	hardBound := totalCap + lineCap
	if snap.RetainedBytes > hardBound {
		t.Fatalf("RetainedBytes = %d exceeds hard bound %d", snap.RetainedBytes, hardBound)
	}
	if snap.ReceivedBytes == 0 || snap.DroppedLineBytes == 0 {
		t.Fatalf("expected overflow, got %+v", snap)
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	c := New(Config{MaxTotalBytes: 1024, MaxLineBytes: 1024})
	writeString(t, c, "secret\n")

	snap := c.Snapshot()
	snap.Lines[0].Data[0] = 'X'
	snap.Lines[0].Truncated = true

	again := c.Snapshot()
	if string(again.Lines[0].Data) != "secret" || again.Lines[0].Truncated {
		t.Fatalf("snapshot aliases internal state: %+v", again.Lines[0])
	}
}
