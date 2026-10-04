package logasm

import (
	"math/rand"
	"reflect"
	"regexp"
	"testing"
)

// 多源多 epoch 独立重组：行号、记录 ID 互不影响。
func TestMultiSourceEpochIsolation(t *testing.T) {
	m, out := newTestMux(testRules(), 0, nil)
	m.FeedChunk("app", 1, "2026-10-04 app-e1-a\n")
	m.FeedChunk("svc", 1, "2026-10-04 svc-e1-a\n")
	m.FeedChunk("app", 2, "2026-10-04 app-e2-a\n")
	m.FeedChunk("app", 1, "2026-10-04 app-e1-b\n")
	m.Flush()

	byID := map[string]Record{}
	for _, r := range *out {
		byID[r.ID] = r
	}
	if len(byID) != 4 {
		t.Fatalf("want 4 independent records, got %d: %v", len(byID), *out)
	}
	for _, id := range []string{"app@1#000001", "app@1#000002", "app@2#000001", "svc@1#000001"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing record %s in %v", id, byID)
		}
	}
	if byID["app@2#000001"].StartLine != 1 {
		t.Fatal("epoch 2 must restart line numbering independently")
	}
}

// 序号缺口上报；迟到更正产生新修订、ID 稳定。
func TestSeqGapAndLateCorrection(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 0, rec)
	feed := func(seq uint64, no int, text string) {
		if err := m.Feed(Line{Source: "app", Epoch: 1, Seq: seq, LineNo: no, Text: text, CorrectsLineNo: -1}); err != nil {
			t.Fatal(err)
		}
	}
	feed(1, 1, "2026-10-04 ERROR boom")
	feed(2, 2, "    at com.example.Foo")
	feed(4, 4, "2026-10-04 INFO next") // 缺口：seq 3 缺失
	if rec.Count(EventSeqGap) != 1 {
		t.Fatalf("want seq_gap event, got %v", rec.Kinds())
	}
	m.Flush() // 缺口永久化，冲刷

	// 迟到更正：改写已闭合记录的第 1 行。
	err := m.Feed(Line{Source: "app", Epoch: 1, Seq: 5, Text: "2026-10-04 ERROR boom (corrected)", CorrectsLineNo: 1})
	if err != nil {
		t.Fatal(err)
	}

	var revised Record
	for _, r := range *out {
		if r.ID == "app@1#000001" {
			revised = r
		}
	}
	if revised.Revision != 1 {
		t.Fatalf("want revision 1, got %+v", revised)
	}
	if revised.Lines[0] != "2026-10-04 ERROR boom (corrected)" {
		t.Fatalf("correction not applied: %q", revised.Lines[0])
	}
	if revised.ID != "app@1#000001" {
		t.Fatal("record ID must stay stable across revisions")
	}
	if rec.Count(EventRecordRevised) != 1 {
		t.Fatalf("want record_revised event, got %v", rec.Kinds())
	}
}

// 迟到的续行必须修订其原始记录，绝不拼进另一条已结束记录。
func TestLateContinuationNotSplicedIntoOtherRecord(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 0, rec)
	feed := func(seq uint64, no int, text string) {
		if err := m.Feed(Line{Source: "app", Epoch: 1, Seq: seq, LineNo: no, Text: text, CorrectsLineNo: -1}); err != nil {
			t.Fatal(err)
		}
	}
	feed(1, 1, "2026-10-04 ERROR first")
	feed(3, 3, "2026-10-04 INFO second") // seq 2 缺口
	m.Flush()                            // 缺口永久化：second 关闭 first
	feed(2, 2, "    at com.example.Foo") // 真正的迟到续行

	if rec.Count(EventLateRevision) != 1 {
		t.Fatalf("want late_revision event, got %v", rec.Kinds())
	}
	latest := map[string]Record{}
	for _, r := range *out {
		latest[r.ID] = r
	}
	first := latest["app@1#000001"]
	second := latest["app@1#000002"]
	if first.Revision != 1 || len(first.Lines) != 2 || first.Lines[1] != "    at com.example.Foo" {
		t.Fatalf("late line must revise its original record: %+v", first)
	}
	if len(second.Lines) != 1 {
		t.Fatalf("late line must NOT be spliced into another closed record: %+v", second)
	}
	if !reflect.DeepEqual(first.LineNos, []int{1, 2}) {
		t.Fatalf("line identity broken: %v", first.LineNos)
	}
}

// 重复行被丢弃并上报。
func TestDuplicateDropped(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 0, rec)
	l := Line{Source: "app", Epoch: 1, Seq: 1, LineNo: 1, Text: "2026-10-04 A", CorrectsLineNo: -1}
	m.Feed(l)
	m.Flush()
	if err := m.Feed(l); err != nil {
		t.Fatal(err)
	}
	if rec.Count(EventDuplicateDropped) != 1 {
		t.Fatalf("want duplicate_dropped event, got %v", rec.Kinds())
	}
	if len(*out) != 1 {
		t.Fatalf("duplicate must not produce a new record, got %v", *out)
	}
}

// 规则热换：新规则作用于后续输入，原行身份与记录 ID 不变。
func TestRuleHotSwapPreservesIdentity(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(Rules{
		Start:              regexp.MustCompile(`^ERROR`),
		ContinuationIndent: true,
	}, 0, rec)
	m.FeedChunk("app", 1, "ERROR one\n  stack-line\n")
	m.SetRules("app", 1, Rules{
		Start:              regexp.MustCompile(`^WARN`),
		ContinuationIndent: true,
	})
	m.FeedChunk("app", 1, "WARN two\nERROR three\n")
	m.Flush()

	if rec.Count(EventRulesSwapped) != 1 {
		t.Fatalf("want rules_swapped event, got %v", rec.Kinds())
	}
	recs := *out
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d: %v", len(recs), recs)
	}
	// 热换后 "ERROR three" 不再命中起始模式，但非缩进 → 隐式起始行。
	wantIDs := []string{"app@1#000001", "app@1#000002", "app@1#000003"}
	for i, id := range wantIDs {
		if recs[i].ID != id {
			t.Fatalf("record %d: want ID %s, got %s", i, id, recs[i].ID)
		}
	}
	if recs[0].StartLine != 1 || recs[2].EndLine != 4 {
		t.Fatalf("line identity broken across hot-swap: %+v", recs)
	}
	if recs[0].Text() != "ERROR one\n  stack-line" {
		t.Fatalf("pre-swap record corrupted: %q", recs[0].Text())
	}
}

// 逐切块与乱序参考对照：整流、随机切块、乱序行式喂入，结果必须一致。
func TestChunkedAndOutOfOrderMatchReference(t *testing.T) {
	var text string
	for i := 1; i <= 20; i++ {
		text += "2026-10-04 10:00:00 INFO record-head\n"
		text += "    continuation line a\n    continuation line b\n"
	}
	lines := splitLines(text)

	reference := runChunks(t, testRules(), []string{text})

	// 随机切块（含切断行中间的边界）。
	rng := rand.New(rand.NewSource(42))
	var chunks []string
	for rest := text; len(rest) > 0; {
		n := 1 + rng.Intn(37)
		if n > len(rest) {
			n = len(rest)
		}
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	if got := runChunks(t, testRules(), chunks); !reflect.DeepEqual(got, reference) {
		t.Fatal("chunked feed diverges from whole-stream reference")
	}

	// 乱序行式喂入：首行锚定序号游标，其余全洗牌，结果不变。
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 0, rec)
	feedLine := func(i int) {
		err := m.Feed(Line{Source: "app", Epoch: 1, Seq: uint64(i + 1), LineNo: i + 1, Text: lines[i], CorrectsLineNo: -1})
		if err != nil {
			t.Fatal(err)
		}
	}
	feedLine(0)
	perm := rand.New(rand.NewSource(7)).Perm(len(lines) - 1)
	for _, idx := range perm {
		feedLine(idx + 1)
	}
	m.Flush()
	if !reflect.DeepEqual(*out, reference) {
		t.Fatalf("out-of-order feed diverges from reference\n got: %v\nwant: %v", *out, reference)
	}
}

func splitLines(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, text[start:i])
			start = i + 1
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

func runChunks(t *testing.T, rules Rules, chunks []string) []Record {
	m, out := newTestMux(rules, 0, nil)
	for _, c := range chunks {
		if err := m.FeedChunk("app", 1, c); err != nil {
			t.Fatal(err)
		}
	}
	m.Flush()
	return *out
}

// 总缓冲额度耗尽时施加背压；释放后重试成功。
func TestBackpressureOnBudgetExhaustion(t *testing.T) {
	rec := &Recorder{}
	m, out := newTestMux(testRules(), 48, rec)
	if err := m.FeedChunk("app", 1, "2026-10-04 aaaaa\n"); err != nil {
		t.Fatal(err)
	}
	err := m.FeedChunk("app", 1, "    a fairly long continuation line\n")
	if err != ErrBackpressure {
		t.Fatalf("want ErrBackpressure, got %v", err)
	}
	if rec.Count(EventBackpressure) != 1 {
		t.Fatalf("want backpressure event, got %v", rec.Kinds())
	}
	m.Flush() // 发出待定记录，释放额度
	if err := m.FeedChunk("app", 1, "    a fairly long continuation line\n"); err != nil {
		t.Fatalf("retry after drain must succeed: %v", err)
	}
	m.Flush()
	if len(*out) != 2 {
		t.Fatalf("want 2 records after retry, got %d", len(*out))
	}
}
