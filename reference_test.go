package pktscale

import (
	"strings"
	"testing"
)

// 参考检查器必须能发现无法满足的包时距限制。
func TestCheckSpacingDetectsUnsatisfiable(t *testing.T) {
	// 手工构造：流 0 两个包间隔 1 tick，但包长 300 字节在 100 B/tick
	// 链路上需要 3 tick —— 时距不可满足。
	batches := [][]Fragment{
		{{Seq: 0, Stream: 0, Tick: 0, Size: 300}},
		{{Seq: 1, Stream: 0, Tick: 1, Size: 10}},
		{{Seq: 2, Stream: 1, Tick: 0, Size: 10}},
	}
	bad := CheckSpacing(batches, 2, 100)
	if len(bad) != 2 {
		t.Fatalf("want 2 violations (min gap + unsatisfiable), got %v", bad)
	}
	joined := strings.Join(bad, "\n")
	if !strings.Contains(joined, "unsatisfiable") {
		t.Fatalf("missing unsatisfiable-spacing report: %v", bad)
	}
	if !strings.Contains(joined, "min gap") {
		t.Fatalf("missing min-gap report: %v", bad)
	}
	// 满足约束时无报告。
	ok := CheckSpacing([][]Fragment{
		{{Seq: 0, Stream: 0, Tick: 0, Size: 100}},
		{{Seq: 1, Stream: 0, Tick: 5, Size: 100}},
	}, 2, 100)
	if len(ok) != 0 {
		t.Fatalf("false positives: %v", ok)
	}
}

// 参考检查器必须能发现漂移越界（用故意错误的输出验证）。
func TestCheckDriftDetectsViolation(t *testing.T) {
	recs := []Record{{Seq: 0, Stream: 0, Time: 10, Size: 1}}
	segs := []RateSegment{{At: 0, Rate: rat(1, 1)}}
	good := CheckDrift(segs, recs, [][]Fragment{{{Seq: 0, Stream: 0, Tick: 10, Size: 1}}}, 1)
	if len(good) != 0 {
		t.Fatalf("false positive: %v", good)
	}
	bad := CheckDrift(segs, recs, [][]Fragment{{{Seq: 0, Stream: 0, Tick: 15, Size: 1}}}, 1)
	if len(bad) != 1 {
		t.Fatalf("drift violation not detected: %v", bad)
	}
}
