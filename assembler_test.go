package logasm

import (
	"regexp"
	"strings"
	"testing"
)

func testRules() Rules {
	return Rules{
		Start:              regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`),
		ContinuationIndent: true,
	}
}

func newTestMux(rules Rules, budget int, sink Sink) (*Mux, *[]Record) {
	out := &[]Record{}
	m := NewMux(rules, budget, sink, func(r Record) { *out = append(*out, r) })
	return m, out
}

const stackLog = `2026-10-04 10:00:00 INFO  service starting
2026-10-04 10:00:01 ERROR request failed
    java.lang.IllegalStateException: boom
        at com.example.Foo.bar(Foo.java:42)
        at com.example.Main.main(Main.java:10)
2026-10-04 10:00:02 INFO  recovered
`

// 异常堆栈跨块仍是一条记录。
func TestExceptionStackAcrossChunks(t *testing.T) {
	m, out := newTestMux(testRules(), 0, nil)
	const chunk = 7 // 故意用不整除的块长切断堆栈行
	for i := 0; i < len(stackLog); i += chunk {
		end := i + chunk
		if end > len(stackLog) {
			end = len(stackLog)
		}
		if err := m.FeedChunk("app", 1, stackLog[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	m.Flush()

	recs := *out
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d: %v", len(recs), recs)
	}
	stack := recs[1]
	if len(stack.Lines) != 4 {
		t.Fatalf("stack record should have 4 lines, got %d: %q", len(stack.Lines), stack.Lines)
	}
	if !strings.Contains(stack.Text(), "IllegalStateException") || !strings.Contains(stack.Text(), "Main.java:10") {
		t.Fatalf("stack record incomplete: %q", stack.Text())
	}
	if !stack.Closed {
		t.Fatal("stack record should be closed by the following start line")
	}
}

// 新起始行关闭上一条记录。
func TestNewStartLineClosesPrevious(t *testing.T) {
	m, out := newTestMux(testRules(), 0, nil)
	m.FeedChunk("app", 1, "2026-10-04 A\n2026-10-04 B\n")
	m.Flush()
	recs := *out
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if !recs[0].Closed {
		t.Fatal("first record must be closed by the new start line")
	}
	if recs[0].ID == recs[1].ID {
		t.Fatal("record IDs must be distinct")
	}
}

// 超长记录截断：置标记并计丢弃字节，而非静默丢行。
func TestTruncationMarkedNotSilent(t *testing.T) {
	rules := testRules()
	rules.MaxRecordBytes = 20
	rec := &Recorder{}
	m, out := newTestMux(rules, 0, rec)
	long := "2026-10-04 10:00:01 ERROR this line is far too long"
	m.FeedChunk("app", 1, long+"\n    continuation also dropped\n2026-10-04 next\n")
	m.Flush()

	recs := *out
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	tr := recs[0]
	if !tr.Truncated {
		t.Fatal("oversized record must be marked truncated")
	}
	if tr.DroppedBytes == 0 {
		t.Fatal("dropped bytes must be accounted, not silently lost")
	}
	if got := len(tr.Lines[0]); got > 20 {
		t.Fatalf("stored content exceeds limit: %d bytes", got)
	}
	if rec.Count(EventRecordTruncated) != 1 {
		t.Fatalf("want 1 truncation event, got %d", rec.Count(EventRecordTruncated))
	}
}

// 不完整尾部：flush 时半行与未闭合记录都要发出并标记。
func TestIncompleteTailOnFlush(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 0, rec)
	m.FeedChunk("app", 1, "2026-10-04 INFO one\n2026-10-04 INFO tail-no-newline")
	m.Flush()

	recs := *out
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	tail := recs[1]
	if tail.Closed {
		t.Fatal("tail record must be marked not-closed")
	}
	if tail.Text() != "2026-10-04 INFO tail-no-newline" {
		t.Fatalf("tail text mismatch: %q", tail.Text())
	}
	if rec.Count(EventFlushIncomplete) != 1 {
		t.Fatalf("want flush_incomplete event, got %v", rec.Kinds())
	}
}

// 原始行号保留。
func TestOriginalLineNumbersPreserved(t *testing.T) {
	m, out := newTestMux(testRules(), 0, nil)
	m.FeedChunk("app", 1, stackLog)
	m.Flush()
	recs := *out
	want := [][2]int{{1, 1}, {2, 5}, {6, 6}}
	for i, w := range want {
		if recs[i].StartLine != w[0] || recs[i].EndLine != w[1] {
			t.Fatalf("record %d: want lines %d-%d, got %d-%d",
				i, w[0], w[1], recs[i].StartLine, recs[i].EndLine)
		}
	}
	stack := recs[1]
	for j, ln := range stack.LineNos {
		if ln != 2+j {
			t.Fatalf("stack LineNos[%d] = %d, want %d", j, ln, 2+j)
		}
	}
}
