package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func segmentFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestWatermarkIsSlowestConsumer(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 100, Hooks{})
	if err := l.RegisterConsumer("fast", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("slow", 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Ack("fast", 10); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("slow", 3); err != nil {
		t.Fatal(err)
	}
	if got := l.Watermark(); got != 3 {
		t.Fatalf("watermark = %d, want 3 (slowest consumer)", got)
	}
}

func TestSlowConsumerBlocksSegmentReclaim(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	if err := l.RegisterConsumer("fast", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("slow", 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// Segments: [0..3] [4..7] [8..11].
	if n := len(segmentFiles(t, dir)); n != 3 {
		t.Fatalf("segment files = %d, want 3", n)
	}
	if err := l.Ack("fast", 12); err != nil {
		t.Fatal(err)
	}
	// slow consumed only offsets < 3; segment 0 ends at offset 4 and is
	// therefore still pinned.
	if err := l.Ack("slow", 3); err != nil {
		t.Fatal(err)
	}
	n, err := l.Reclaim()
	if err != nil || n != 0 {
		t.Fatalf("reclaim = (%d,%v), want (0,nil): slow consumer must block", n, err)
	}
	if n := len(segmentFiles(t, dir)); n != 3 {
		t.Fatalf("segment files = %d, want 3", n)
	}
	// slow catches up just enough to release segment 0 (ends at offset 4).
	if err := l.Ack("slow", 4); err != nil {
		t.Fatal(err)
	}
	if got := l.Watermark(); got != 4 {
		t.Fatalf("watermark = %d, want 4", got)
	}
	n, err = l.Reclaim()
	if err != nil || n != 1 {
		t.Fatalf("reclaim = (%d,%v), want (1,nil)", n, err)
	}
	if _, err := l.Read(0); err != ErrOffsetReclaimed {
		t.Fatalf("read reclaimed offset: %v, want ErrOffsetReclaimed", err)
	}
	if p, err := l.Read(4); err != nil || p[0] != 4 {
		t.Fatalf("read 4 = (%v,%v), want live record", p, err)
	}
}

func TestRemovingConsumerAdvancesWatermark(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	if err := l.RegisterConsumer("fast", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("slow", 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Ack("fast", 12); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("slow", 3); err != nil {
		t.Fatal(err)
	}
	if n, err := l.Reclaim(); err != nil || n != 0 {
		t.Fatalf("reclaim before removal = (%d,%v), want 0", n, err)
	}
	if err := l.RemoveConsumer("slow"); err != nil {
		t.Fatal(err)
	}
	if got := l.Watermark(); got != 12 {
		t.Fatalf("watermark after removal = %d, want 12", got)
	}
	// Segments 0 and 1 are fully below the watermark; active segment 2 stays.
	n, err := l.Reclaim()
	if err != nil || n != 2 {
		t.Fatalf("reclaim after removal = (%d,%v), want (2,nil)", n, err)
	}
	if got := l.FirstOffset(); got != 8 {
		t.Fatalf("firstOffset = %d, want 8", got)
	}
	if n := len(segmentFiles(t, dir)); n != 1 {
		t.Fatalf("segment files = %d, want 1", n)
	}
	// High watermark and append continuity are preserved after reclaim.
	if got := l.NextOffset(); got != 12 {
		t.Fatalf("nextOffset = %d, want 12", got)
	}
	if off, err := l.Append([]byte{12}); err != nil || off != 12 {
		t.Fatalf("append after reclaim = (%d,%v), want (12,nil)", off, err)
	}
	if p, err := l.Read(12); err != nil || p[0] != 12 {
		t.Fatalf("read new record = (%v,%v)", p, err)
	}
}

func TestNoConsumersNeverRemovesActiveSegment(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	for i := 0; i < 8; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// No consumers registered: nothing pins retention, but the active
	// segment must survive so appends keep working.
	n, err := l.Reclaim()
	if err != nil || n != 1 {
		t.Fatalf("reclaim = (%d,%v), want (1,nil)", n, err)
	}
	if n := len(segmentFiles(t, dir)); n != 1 {
		t.Fatalf("segment files = %d, want 1 (active kept)", n)
	}
	if got := l.FirstOffset(); got != 4 {
		t.Fatalf("firstOffset = %d, want 4", got)
	}
	if off, err := l.Append([]byte{8}); err != nil || off != 8 {
		t.Fatalf("append = (%d,%v), want (8,nil)", off, err)
	}
}

func TestReclaimCrashRecoveryIsConsistent(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	for i := 0; i < 12; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("c", 12); err != nil {
		t.Fatal(err)
	}
	n, err := l.Reclaim()
	if err != nil || n != 2 {
		t.Fatalf("reclaim = (%d,%v), want (2,nil)", n, err)
	}
	l.Close()
	// Reopen as if the process was killed at any point: files are source of
	// truth; remaining segments and cursors must reconcile.
	l2 := reopenTestLog(t, dir, 4, Hooks{})
	if got := l2.FirstOffset(); got != 8 {
		t.Fatalf("firstOffset = %d, want 8", got)
	}
	if got := l2.NextOffset(); got != 12 {
		t.Fatalf("nextOffset = %d, want 12", got)
	}
	if pos, _ := l2.Position("c"); pos != 12 {
		t.Fatalf("cursor = %d, want 12", pos)
	}
	if _, err := l2.Read(7); err != ErrOffsetReclaimed {
		t.Fatalf("read 7: %v, want ErrOffsetReclaimed", err)
	}
	if p, err := l2.Read(8); err != nil || p[0] != 8 {
		t.Fatalf("read 8 = (%v,%v)", p, err)
	}
	// Another reclaim cycle after reopen is a no-op and still safe.
	if n, err := l2.Reclaim(); err != nil || n != 0 {
		t.Fatalf("second reclaim = (%d,%v), want 0", n, err)
	}
}

func TestReopenedLogHasNoStaleSegments(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	for i := 0; i < 9; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	// Orphan file with a bogus base that breaks continuity is dropped.
	orphan := filepath.Join(dir, fmt.Sprintf("%020d.log", 1000))
	if err := os.WriteFile(orphan, []byte("junk"), filePermission); err != nil {
		t.Fatal(err)
	}
	l2 := reopenTestLog(t, dir, 4, Hooks{})
	if got := l2.NextOffset(); got != 9 {
		t.Fatalf("nextOffset = %d, want 9", got)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan segment not removed: %v", err)
	}
}
