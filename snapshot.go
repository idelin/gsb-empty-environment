package telemetry

import "fmt"

// SeriesSnapshot 是一个存活系列的只读视图。
type SeriesSnapshot struct {
	LabelKey   string
	Generation uint64
	Counted    bool
	Samples    int64
	Bytes      int64
	Value      int64
}

// MetricSnapshot 是一个指标的只读视图。
type MetricSnapshot struct {
	LiveCounted      int
	Series           map[string]SeriesSnapshot
	OverflowSamples  int64
	OverflowBytes    int64
	OverflowValue    int64
	OverflowContribs map[string]int64 // labelKey -> 折叠样本数（可撤回贡献）
	Archived         []ArchivedGeneration
	Received         int64
	ReceivedBytes    int64
	ReceivedValue    int64
	Rejected         int64
	FoldedTotal      int64
	RetractedSum     int64
}

// TenantSnapshot 是一个租户的只读视图。
type TenantSnapshot struct {
	LiveCounted int
	LedgerBytes int64
	Metrics     map[string]MetricSnapshot
}

// Snapshot 是整个限制器的一致性只读视图。
type Snapshot struct {
	TenantQuota int
	MetricQuota int
	Tenants     map[string]TenantSnapshot
}

// Snapshot 拷贝当前状态，供核验与展示。
func (l *Limiter) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := Snapshot{
		TenantQuota: l.cfg.TenantQuota,
		MetricQuota: l.cfg.MetricQuota,
		Tenants:     map[string]TenantSnapshot{},
	}
	for tname, t := range l.tenants {
		ts := TenantSnapshot{
			LiveCounted: t.liveCounted,
			LedgerBytes: t.ledgerBytes,
			Metrics:     map[string]MetricSnapshot{},
		}
		for mname, m := range t.metrics {
			ms := MetricSnapshot{
				LiveCounted:      m.liveCounted,
				Series:           map[string]SeriesSnapshot{},
				OverflowSamples:  m.overflow.samples,
				OverflowBytes:    m.overflow.bytes,
				OverflowValue:    m.overflow.value,
				OverflowContribs: map[string]int64{},
				Archived:         append([]ArchivedGeneration(nil), m.archived...),
				Received:         m.received,
				ReceivedBytes:    m.receivedB,
				ReceivedValue:    m.receivedV,
				Rejected:         m.rejected,
				FoldedTotal:      m.foldedTotal,
				RetractedSum:     m.retractedSum,
			}
			for key, s := range m.series {
				ms.Series[key] = SeriesSnapshot{
					LabelKey:   key,
					Generation: s.generation,
					Counted:    s.counted,
					Samples:    s.samples,
					Bytes:      s.bytes,
					Value:      s.value,
				}
			}
			for key, c := range m.overflow.contributions {
				ms.OverflowContribs[key] = c.samples
			}
			ts.Metrics[mname] = ms
		}
		snap.Tenants[tname] = ts
	}
	return snap
}

// Verify 核验快照的不变量，返回所有违规描述（空切片表示全部通过）：
//  1. 额度：租户与指标两层的占用都不超限；
//  2. 接收量守恒：接收样本 = 存活 + 溢出 + 已封存代际（字节与值同理）；
//  3. 账本一致：租户字节账本 = 存活系列字节 + 溢出桶字节；
//  4. 代际隔离：同一指标内代际号全局唯一，过期代际只存在于封存区；
//  5. 折叠守恒：历史折叠数 = 溢出桶中可撤回贡献 + 已撤回数；
//  6. 溢出桶无保留前缀标签，不与真实同名标签冲突。
func Verify(s Snapshot) []string {
	var problems []string
	fail := func(format string, args ...interface{}) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	for tname, t := range s.Tenants {
		if t.LiveCounted > s.TenantQuota {
			fail("tenant %q: live counted %d exceeds tenant quota %d", tname, t.LiveCounted, s.TenantQuota)
		}
		var countedSum int
		var liveBytes int64
		for mname, m := range t.Metrics {
			if m.LiveCounted > s.MetricQuota {
				fail("tenant %q metric %q: live counted %d exceeds metric quota %d", tname, mname, m.LiveCounted, s.MetricQuota)
			}
			countedSum += m.LiveCounted

			var liveSamples, liveBytesM, liveVal int64
			seenGen := map[uint64]string{}
			for key, srs := range m.Series {
				liveSamples += srs.Samples
				liveBytes += srs.Bytes
				liveBytesM += srs.Bytes
				liveVal += srs.Value
				if prev, dup := seenGen[srs.Generation]; dup {
					fail("tenant %q metric %q: generation %d shared by live series %q and %q", tname, mname, srs.Generation, prev, key)
				}
				seenGen[srs.Generation] = "live:" + key
			}
			var archSamples, archBytes, archVal int64
			for _, a := range m.Archived {
				archSamples += a.Samples
				archBytes += a.Bytes
				archVal += a.Value
				if prev, dup := seenGen[a.Generation]; dup {
					fail("tenant %q metric %q: generation %d of archived %q also used by %q", tname, mname, a.Generation, a.LabelKey, prev)
				}
				seenGen[a.Generation] = "archived:" + a.LabelKey
			}
			if got, want := liveSamples+m.OverflowSamples+archSamples, m.Received; got != want {
				fail("tenant %q metric %q: sample conservation broken: live+overflow+archived=%d received=%d", tname, mname, got, want)
			}
			if got, want := liveBytesM+m.OverflowBytes+archBytes, m.ReceivedBytes; got != want {
				fail("tenant %q metric %q: byte conservation broken: live+overflow+archived=%d received=%d", tname, mname, got, want)
			}
			if got, want := liveVal+m.OverflowValue+archVal, m.ReceivedValue; got != want {
				fail("tenant %q metric %q: value conservation broken: live+overflow+archived=%d received=%d", tname, mname, got, want)
			}
			var contribSum int64
			for key, n := range m.OverflowContribs {
				contribSum += n
				if len(key) >= 2 && key[:2] == ReservedPrefix {
					fail("tenant %q metric %q: overflow contribution %q uses reserved prefix", tname, mname, key)
				}
			}
			if contribSum != m.OverflowSamples {
				fail("tenant %q metric %q: overflow contributions %d != overflow samples %d", tname, mname, contribSum, m.OverflowSamples)
			}
			if got, want := contribSum+m.RetractedSum, m.FoldedTotal; got != want {
				fail("tenant %q metric %q: fold accounting broken: contributions+retracted=%d folded=%d", tname, mname, got, want)
			}
		}
		if countedSum != t.LiveCounted {
			fail("tenant %q: metric live counted sum %d != tenant live counted %d", tname, countedSum, t.LiveCounted)
		}
		var overflowBytes int64
		for _, m := range t.Metrics {
			overflowBytes += m.OverflowBytes
		}
		if got, want := liveBytes+overflowBytes, t.LedgerBytes; got != want {
			fail("tenant %q: ledger %d != live+overflow bytes %d", tname, got, want)
		}
	}
	return problems
}
