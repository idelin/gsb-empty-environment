// logasm-demo 演示：多源日志按起始行+缩进续行重组，覆盖跨块堆栈、
// 超长截断、序号缺口、迟到续行、迟到更正、规则热换、检查点恢复与背压。
package main

import (
	"fmt"
	"regexp"
	"strings"

	"logasm"
)

func main() {
	rules := logasm.Rules{
		Start:              regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`),
		ContinuationIndent: true,
		MaxRecordBytes:     128,
	}
	telemetry := &logasm.Recorder{}
	var records []logasm.Record
	mux := logasm.NewMux(rules, 0, telemetry, func(r logasm.Record) {
		records = append(records, r)
	})

	fmt.Println("== 1. 流切分：异常堆栈跨 17 字节块仍是一条 ==")
	log1 := "2026-10-04 10:00:00 INFO  service starting\n" +
		"2026-10-04 10:00:01 ERROR request failed\n" +
		"    java.lang.IllegalStateException: boom\n" +
		"        at com.example.Foo.bar(Foo.java:42)\n" +
		"2026-10-04 10:00:02 INFO  recovered\n"
	for i := 0; i < len(log1); i += 17 {
		end := i + 17
		if end > len(log1) {
			end = len(log1)
		}
		must(mux.FeedChunk("app", 1, log1[i:end]))
	}
	mux.Flush()
	show(records)
	records = nil

	fmt.Println("== 2. 多源多 epoch 独立重组 + 超长截断标记 ==")
	long := "2026-10-04 10:01:00 ERROR " + strings.Repeat("x", 120) + "\n    suppressed continuation\n"
	must(mux.FeedChunk("app", 2, long))
	must(mux.FeedChunk("svc", 1, "2026-10-04 10:01:00 INFO  svc hello\n"))
	mux.Flush()
	show(records)
	records = nil

	fmt.Println("== 3. 序号缺口 + 迟到续行（修订原记录，不拼进他人） ==")
	feed := func(seq uint64, no int, text string) {
		must(mux.Feed(logasm.Line{Source: "job", Epoch: 1, Seq: seq, LineNo: no, Text: text, CorrectsLineNo: -1}))
	}
	feed(1, 1, "2026-10-04 10:02:00 ERROR job failed")
	feed(3, 3, "2026-10-04 10:02:01 INFO  job retried")
	mux.Flush()                                          // 缺口永久化
	feed(2, 2, "    at com.example.Job.run(Job.java:7)") // 迟到续行
	show(records)
	records = nil

	fmt.Println("== 4. 迟到更正：稳定 ID，修订号递增 ==")
	must(mux.Feed(logasm.Line{Source: "job", Epoch: 1, Text: "2026-10-04 10:02:00 ERROR job failed (corrected)", CorrectsLineNo: 1}))
	show(records)
	records = nil

	fmt.Println("== 5. 规则热换：原行身份保留 ==")
	mux.SetRules("job", 1, logasm.Rules{Start: regexp.MustCompile(`^WARN`), ContinuationIndent: true})
	feed(4, 4, "WARN hot swap active")
	feed(5, 5, "2026-10-04 10:02:02 INFO  implicit start now")
	mux.Flush()
	show(records)
	records = nil

	fmt.Println("== 6. 检查点：半行与待定记录随快照恢复 ==")
	must(mux.FeedChunk("db", 1, "2026-10-04 10:03:00 ERROR tx abort\n    at com.examp"))
	snap, err := mux.Snapshot()
	must(err)
	fmt.Printf("checkpoint bytes=%d\n", len(snap))
	mux2, err := logasm.Restore(snap, 0, telemetry, func(r logasm.Record) { records = append(records, r) })
	must(err)
	must(mux2.FeedChunk("db", 1, "le.Db.Tx(Tx.java:11)\n"))
	mux2.Flush()
	show(records)
	records = nil

	fmt.Println("== 7. 背压：总缓冲额度耗尽 ==")
	telemetry2 := &logasm.Recorder{}
	small := logasm.NewMux(rules, 48, telemetry2, func(r logasm.Record) { records = append(records, r) })
	must(small.FeedChunk("tiny", 1, "2026-10-04 aaaaa\n"))
	err = small.FeedChunk("tiny", 1, "    a fairly long continuation line\n")
	fmt.Printf("second feed err: %v\n", err)
	small.Flush() // 释放额度后重试
	must(small.FeedChunk("tiny", 1, "    a fairly long continuation line\n"))
	small.Flush()
	show(records)
	fmt.Println("backpressure telemetry:")
	for _, e := range telemetry2.Events {
		fmt.Printf("  %-18s %s\n", e.Kind, e.Detail)
	}

	fmt.Println("== 遥测事件（本地合成，可夹具回放） ==")
	for _, e := range telemetry.Events {
		fmt.Printf("  %-18s src=%-4s epoch=%d rec=%s %s\n", e.Kind, e.Source, e.Epoch, e.RecordID, e.Detail)
	}
}

func show(recs []logasm.Record) {
	for _, r := range recs {
		fmt.Printf("  %s\n", r)
		for _, l := range r.Lines {
			fmt.Printf("      | %s\n", l)
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
