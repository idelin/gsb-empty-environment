package logasm

import (
	"reflect"
	"testing"
)

// checkpoint 恢复必须保留半行与待定记录，续跑结果与不间断参考一致。
func TestCheckpointRestorePreservesPartialAndPending(t *testing.T) {
	part1 := "2026-10-04 INFO one\n    at com.Examp" // 结尾是半行
	part2 := "le.Foo(Foo.java:1)\n2026-10-04 INFO two\n"

	// 参考：不间断跑完。
	ref := runChunks(t, testRules(), []string{part1, part2})

	// 跑到一半做快照，全新 Mux 恢复后续跑。
	rec := &Recorder{}
	m1, out1 := newTestMux(testRules(), 0, rec)
	if err := m1.FeedChunk("app", 1, part1); err != nil {
		t.Fatal(err)
	}
	snap, err := m1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Count(EventCheckpointSaved) != 1 {
		t.Fatalf("want checkpoint_saved event, got %v", rec.Kinds())
	}

	rec2 := &Recorder{}
	out2 := &[]Record{}
	m2, err := Restore(snap, 0, rec2, func(r Record) { *out2 = append(*out2, r) })
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Count(EventCheckpointRestored) != 1 {
		t.Fatalf("want checkpoint_restored event, got %v", rec2.Kinds())
	}
	if err := m2.FeedChunk("app", 1, part2); err != nil {
		t.Fatal(err)
	}
	m2.Flush()

	if len(*out1) != 0 {
		t.Fatalf("no record should be emitted before restore, got %v", *out1)
	}
	if !reflect.DeepEqual(*out2, ref) {
		t.Fatalf("restored run diverges from reference\n got: %v\nwant: %v", *out2, ref)
	}
	// 半行必须拼回完整堆栈行。
	if got := (*out2)[0].Lines[1]; got != "    at com.Example.Foo(Foo.java:1)" {
		t.Fatalf("partial line not preserved across checkpoint: %q", got)
	}
}

// 行式输入的序号游标与重排缓冲同样进检查点。
func TestCheckpointRestoreSeqState(t *testing.T) {
	rec := &Recorder{}
	m1, _ := newTestMux(testRules(), 0, rec)
	m1.Feed(Line{Source: "app", Epoch: 1, Seq: 1, LineNo: 1, Text: "2026-10-04 A", CorrectsLineNo: -1})
	m1.Feed(Line{Source: "app", Epoch: 1, Seq: 3, LineNo: 3, Text: "2026-10-04 C", CorrectsLineNo: -1}) // 缺口
	snap, err := m1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	out2 := &[]Record{}
	m2, err := Restore(snap, 0, nil, func(r Record) { *out2 = append(*out2, r) })
	if err != nil {
		t.Fatal(err)
	}
	// 补齐 seq 2，重排缓冲中的 seq 3 应立即放行。
	m2.Feed(Line{Source: "app", Epoch: 1, Seq: 2, LineNo: 2, Text: "2026-10-04 B", CorrectsLineNo: -1})
	m2.Flush()

	recs := *out2
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d: %v", len(recs), recs)
	}
	for i, want := range []string{"2026-10-04 A", "2026-10-04 B", "2026-10-04 C"} {
		if recs[i].Text() != want {
			t.Fatalf("record %d: want %q, got %q", i, want, recs[i].Text())
		}
	}
}
