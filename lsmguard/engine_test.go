package lsmguard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// randKey 生成确定性的"随机散列" key。
func randKey(i int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("key-%d", i)))
	return hex.EncodeToString(sum[:])
}

// buildEngine 构造含 numTables 个 SSTable、每表 tableSize 个 key 的引擎。
func buildEngine(numTables, tableSize int) (*Engine, []string) {
	e := NewEngine(tableSize)
	var keys []string
	for i := 0; i < numTables*tableSize; i++ {
		k := randKey(i)
		keys = append(keys, k)
		e.Put(k, fmt.Sprintf("val-%d", i))
	}
	e.Flush()
	return e, keys
}

func TestGetCorrectness(t *testing.T) {
	e, keys := buildEngine(4, 100)
	if e.NumSSTables() != 4 {
		t.Fatalf("期望 4 个 SSTable，实际 %d", e.NumSSTables())
	}
	for _, k := range keys {
		if _, ok := e.Get(k); !ok {
			t.Fatalf("已写入的 key 未命中: %s", k)
		}
	}
	if _, ok := e.Get(randKey(999999)); ok {
		t.Fatal("未写入的 key 不应命中")
	}
}

func TestReadAmplificationLowWithBloom(t *testing.T) {
	e, keys := buildEngine(4, 100)
	m := NewMonitor(200)
	e.SetOnGet(m.Observe)

	for i := 0; i < 200; i++ {
		e.Get(keys[i%len(keys)])
	}
	met := m.Metrics()
	t.Logf("Bloom 全开时读放大 = %.2f", met.ReadAmplification)
	if met.ReadAmplification > 1.5 {
		t.Fatalf("Bloom 全开时读放大应接近 1，实际 %.2f", met.ReadAmplification)
	}
}

// TestReadAmplificationJumpAndAlert 复现验收场景：
// 关闭部分 Bloom Filter 后，读放大从 ~1 跃升至 ~4，监控器输出 Compaction 告警。
func TestReadAmplificationJumpAndAlert(t *testing.T) {
	e, keys := buildEngine(4, 100)
	m := NewMonitor(100)
	var alerts []Alert
	m.SetOnAlert(func(a Alert) { alerts = append(alerts, a) })
	e.SetOnGet(m.Observe)

	// 阶段一：Bloom 全部开启
	for i := 0; i < 100; i++ {
		e.Get(keys[i%len(keys)])
	}
	before := m.Metrics().ReadAmplification
	t.Logf("阶段一读放大 = %.2f", before)
	if before > 1.5 {
		t.Fatalf("阶段一读放大应接近 1，实际 %.2f", before)
	}

	// 阶段二：故意关闭全部 4 个 SSTable 的 Bloom Filter
	for i := 0; i < 4; i++ {
		e.DisableBloom(i)
	}
	m.ResetAlertCooldown()
	for i := 0; i < 100; i++ {
		e.Get(keys[i%len(keys)])
	}
	after := m.Metrics().ReadAmplification
	t.Logf("阶段二读放大 = %.2f", after)
	if after < 3.0 {
		t.Fatalf("关闭 Bloom 后读放大应显著跃升，实际 %.2f", after)
	}

	// 校验告警
	if len(alerts) == 0 {
		t.Fatal("监控器应产生读放大告警")
	}
	found := false
	for _, a := range alerts {
		t.Logf("告警: %s", a.String())
		if a.Metric == "read_amplification" {
			found = true
			if a.Value <= ReadAmpWarnThreshold {
				t.Fatalf("告警值 %.2f 应超过阈值 %.1f", a.Value, ReadAmpWarnThreshold)
			}
		}
	}
	if !found {
		t.Fatal("缺少 read_amplification 告警")
	}
}

func TestFalsePositiveRateAlert(t *testing.T) {
	m := NewMonitor(50)
	var alerts []Alert
	m.SetOnAlert(func(a Alert) { alerts = append(alerts, a) })

	// 合成高误判样本：Bloom 阳性 10 次中 3 次误判（30%）
	for i := 0; i < 50; i++ {
		m.Observe(GetStats{
			Key:            randKey(i),
			SSTablesRead:   2,
			BloomPositives: 10,
			FalsePositives: 3,
		})
	}
	met := m.Metrics()
	t.Logf("误判率 = %.2f%%", met.FalsePositiveRate*100)
	if met.FalsePositiveRate <= FPRateWarnThreshold {
		t.Fatalf("误判率 %.2f 应超过阈值 %.2f", met.FalsePositiveRate, FPRateWarnThreshold)
	}
	found := false
	for _, a := range alerts {
		t.Logf("告警: %s", a.String())
		if a.Metric == "bloom_fp_rate" {
			found = true
		}
	}
	if !found {
		t.Fatal("缺少 bloom_fp_rate 告警（应建议重建 Filter）")
	}
}

func TestBloomFilterNoFalseNegative(t *testing.T) {
	b := NewBloomFilter(1000, 0.01)
	for i := 0; i < 1000; i++ {
		b.Add([]byte(randKey(i)))
	}
	for i := 0; i < 1000; i++ {
		if !b.MaybePresent([]byte(randKey(i))) {
			t.Fatalf("Bloom Filter 出现假阴性: key %d", i)
		}
	}
	// 误判率应远低于 5%
	fp := 0
	for i := 1000; i < 2000; i++ {
		if b.MaybePresent([]byte(randKey(i))) {
			fp++
		}
	}
	rate := float64(fp) / 1000.0
	t.Logf("Bloom 实测误判率 = %.2f%%", rate*100)
	if rate > 0.05 {
		t.Fatalf("误判率 %.2f%% 超出预期", rate*100)
	}
}
