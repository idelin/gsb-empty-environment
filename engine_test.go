package lsmwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func hashKey(prefix string, i int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, i)))
	return hex.EncodeToString(sum[:])
}

// buildEngine 写入 5 个 SSTable，每表 400 个随机散列 Key。
func buildEngine(m *Monitor) *Engine {
	eng := NewEngine(m)
	for b := 0; b < 5; b++ {
		for j := 0; j < 400; j++ {
			eng.Put(hashKey("key", b*400+j), "v")
		}
		eng.Flush()
	}
	return eng
}

func TestBloomFilterNoFalseNegatives(t *testing.T) {
	f := NewBloomFilter(1000, 10)
	for i := 0; i < 1000; i++ {
		f.Add(hashKey("key", i))
	}
	for i := 0; i < 1000; i++ {
		if !f.Test(hashKey("key", i)) {
			t.Fatalf("已插入的 key-%d 被误判为不存在（假阴性）", i)
		}
	}
}

func TestEnginePutGetAndMiss(t *testing.T) {
	eng := NewEngine(nil)
	eng.Put("a", "1")
	if v, ok := eng.Get("a"); !ok || v != "1" {
		t.Fatal("MemTable 命中失败")
	}
	eng.Flush()
	if v, ok := eng.Get("a"); !ok || v != "1" {
		t.Fatal("Flush 后 SSTable 命中失败")
	}
	if _, ok := eng.Get("not-exist"); ok {
		t.Fatal("不存在的 Key 不应命中")
	}
}

func TestBloomDisabledForcesFullScan(t *testing.T) {
	m := NewMonitor(10)
	eng := buildEngine(m)
	eng.SetBloomEnabled(false)

	if _, ok := eng.Get(hashKey("ghost", 0)); ok {
		t.Fatal("ghost key 不应命中")
	}
	snap := m.Snapshot()
	if snap.ReadAmplification != 5.0 {
		t.Fatalf("关闭 Bloom 后未命中应读全部 5 表，实际读放大 %.2f", snap.ReadAmplification)
	}
}

// TestReadAmplificationJump 是验收场景：健康基线 ~1.05，关闭 Bloom 后跃升至 4.2，
// 监控器即时告警并建议增加 Compaction 权重。
func TestReadAmplificationJump(t *testing.T) {
	m := NewMonitor(300)
	eng := buildEngine(m)

	// 阶段1：健康基线（Bloom 正常，全部命中）
	for i := 0; i < 2000; i++ {
		eng.Get(hashKey("key", (i%5)*400+(i/5)%400))
	}
	m.Calibrate()
	base := m.Snapshot().BaselineReadAmplification
	if base < 1.0 || base > 1.3 {
		t.Fatalf("健康基线读放大应在 [1.0, 1.3]，实际 %.3f", base)
	}

	// 阶段2：故意关闭 Bloom Filter，60% 未命中 + 40% 命中
	eng.SetBloomEnabled(false)
	for i := 0; i < 1000; i++ {
		if i%10 < 6 {
			eng.Get(hashKey("ghost", i))
		} else {
			pos := (i / 10) % 5
			eng.Get(hashKey("key", (4-pos)*400+(i/10/5)%400))
		}
	}

	snap := m.Snapshot()
	if snap.WindowReadAmplification < 3.5 {
		t.Fatalf("关闭 Bloom 后窗口读放大应跃升至 ~4.2，实际 %.2f", snap.WindowReadAmplification)
	}
	var spike *Alert
	for i := range snap.Alerts {
		if snap.Alerts[i].Kind == AlertReadAmplificationSpike {
			spike = &snap.Alerts[i]
		}
	}
	if spike == nil {
		t.Fatalf("应触发读放大突增告警，实际告警 %+v", snap.Alerts)
	}
	if !strings.Contains(spike.Suggestion, "Compaction") {
		t.Fatalf("告警应建议增加 Compaction 权重压制读延迟，实际: %s", spike.Suggestion)
	}
	t.Logf("读放大从基线 %.2f 跃升至 %.2f，告警: %s", base, snap.WindowReadAmplification, spike.Message)
}

// TestCompactionSuppressesReadAmplification 验证采纳建议后 Compaction 能压制读放大。
func TestCompactionSuppressesReadAmplification(t *testing.T) {
	m := NewMonitor(500)
	eng := buildEngine(m)
	eng.SetBloomEnabled(false)
	eng.Compact()

	for i := 0; i < 500; i++ {
		eng.Get(hashKey("key", i))
	}
	if ra := m.Snapshot().WindowReadAmplification; ra != 1.0 {
		t.Fatalf("Compaction 后命中查询读放大应为 1.0，实际 %.2f", ra)
	}
}

// TestTinyBloomTriggersRebuildAlert 验证过滤器位数过小时触发重建建议。
func TestTinyBloomTriggersRebuildAlert(t *testing.T) {
	m := NewMonitor(300)
	eng := buildEngine(m)
	eng.RebuildFilters(2)

	for i := 0; i < 500; i++ {
		eng.Get(hashKey("key", (i%5)*400+(i/5)%400))
	}
	snap := m.Snapshot()
	if snap.WindowFalsePositiveRate < 0.10 {
		t.Fatalf("2 bits/key 时误判率应超过 10%%，实际 %.2f%%", snap.WindowFalsePositiveRate*100)
	}
	found := false
	for _, a := range snap.Alerts {
		if a.Kind == AlertBloomFilterRebuild {
			found = true
		}
	}
	if !found {
		t.Fatalf("应触发 BLOOM_FILTER_REBUILD 告警，实际 %+v", snap.Alerts)
	}
}
