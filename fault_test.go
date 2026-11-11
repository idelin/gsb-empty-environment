package repartition

import (
	"fmt"
	"path/filepath"
	"testing"
)

// TestFaultInjectionSweep 在每一个逻辑追加位置注入一次崩溃，
// 覆盖 fsync 前半写与 fsync 后提交两类故障，验证恢复后不丢不重、
// 新拓扑记录不提前可见。
func TestFaultInjectionSweep(t *testing.T) {
	wl := buildWorkload()

	baseDir := filepath.Join(t.TempDir(), "base")
	baseSink := newMemSink()
	runToCompletion(t, baseDir, wl, baseSink, nil)
	totalAppends := countWALAppends(t, filepath.Join(baseDir, walName))
	// config + 记录 + 切换通知 + switched + 每条记录的投递标记帧与回执帧。
	wantFrames := 1 + len(wl.records) + len(wl.cutovers) + 1 + 2*len(wl.records)
	if totalAppends != wantFrames {
		t.Fatalf("WAL 帧数 = %d，期望 %d", totalAppends, wantFrames)
	}

	for at := 1; at <= totalAppends; at++ {
		for _, beforeSync := range []bool{true, false} {
			name := fmt.Sprintf("append=%d/beforeSync=%v", at, beforeSync)
			t.Run(name, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "b")
				sink := newMemSink()
				fault := FaultConfig{
					AtAppend:   at,
					BeforeSync: beforeSync,
					Err:        errInjectedCrash,
				}
				runToCompletion(t, dir, wl, sink, &fault)

				got, want := sink.ids(), expectedIDs(wl)
				if len(got) != len(want) {
					t.Fatalf("崩溃恢复后发布记录数 = %d，期望 %d", len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("发布集合不一致，得到 %v，期望 %v", got, want)
					}
				}
				if len(sink.published) != len(want) {
					t.Fatalf("有效发布数 = %d，期望 %d（存在重复生效）", len(sink.published), len(want))
				}
				// 发布与回执之间崩溃会产生重复下发尝试，但都必须被幂等吸收。
				if sink.attemptCount() < len(want) {
					t.Fatalf("下发尝试数 = %d，不应少于记录数 %d", sink.attemptCount(), len(want))
				}

				// 任何新拓扑记录的发布顺序都不得早于全部旧拓扑记录。
				for idx, id := range sink.order {
					if !isNewTopologyID(id) {
						continue
					}
					for _, oldID := range oldTopologyIDs(wl) {
						if positionOf(sink.order, oldID) > idx {
							t.Fatalf("新拓扑记录 %s 早于旧拓扑记录 %s 发布", id, oldID)
						}
					}
				}
			})
		}
	}
}

func isNewTopologyID(id string) bool {
	return len(id) > 0 && id[:4] == "true"
}

func oldTopologyIDs(wl workload) []string {
	var ids []string
	for _, r := range wl.records {
		if !r.Topology {
			ids = append(ids, r.id())
		}
	}
	return ids
}

func positionOf(order []string, id string) int {
	for i, x := range order {
		if x == id {
			return i
		}
	}
	return len(order) + 1
}
