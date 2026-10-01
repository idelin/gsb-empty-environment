package telemetry

import (
	"testing"
	"time"
)

func testConfig(tenantQuota, metricQuota int, ttl time.Duration) Config {
	return Config{TenantQuota: tenantQuota, MetricQuota: metricQuota, TTL: ttl}
}

func lbl(pairs ...string) []Label {
	out := make([]Label, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Label{Key: pairs[i], Value: pairs[i+1]})
	}
	return out
}

func mustVerify(t *testing.T, l *Limiter) Snapshot {
	t.Helper()
	snap := l.Snapshot()
	if problems := Verify(snap); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("invariant: %s", p)
		}
	}
	return snap
}

func TestLabelOrderInsensitive(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 10, time.Minute), clock.Now)

	r1 := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("a", "1", "b", "2"), Value: 1, Bytes: 10})
	r2 := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("b", "2", "a", "1"), Value: 2, Bytes: 10})
	if r1.Outcome != AdmittedNew || r2.Outcome != AdmittedExisting {
		t.Fatalf("outcomes: %v %v", r1.Outcome, r2.Outcome)
	}
	if r1.LabelKey != r2.LabelKey || r1.Generation != r2.Generation {
		t.Fatalf("label order produced different series: %+v %+v", r1, r2)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if len(m.Series) != 1 {
		t.Fatalf("want 1 series, got %d", len(m.Series))
	}
	if got := m.Series[r1.LabelKey].Samples; got != 2 {
		t.Fatalf("want 2 samples, got %d", got)
	}
}

func TestMetricQuotaFoldsExcess(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 2, time.Minute), clock.Now)
	for _, inst := range []string{"0", "1", "2"} {
		l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", inst), Value: 1, Bytes: 5})
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if m.LiveCounted != 2 || m.OverflowSamples != 1 || m.FoldedTotal != 1 {
		t.Fatalf("live=%d overflow=%d folded=%d", m.LiveCounted, m.OverflowSamples, m.FoldedTotal)
	}
	if m.Received != 3 {
		t.Fatalf("received=%d", m.Received)
	}
}

func TestTenantQuotaFoldsExcess(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(2, 10, time.Minute), clock.Now)
	outcomes := []IngestOutcome{}
	for _, metric := range []string{"m1", "m2", "m3"} {
		r := l.Ingest(Sample{Tenant: "t", Metric: metric, Labels: lbl("a", "1"), Value: 1, Bytes: 5})
		outcomes = append(outcomes, r.Outcome)
	}
	if outcomes[2] != Folded {
		t.Fatalf("third series should fold under tenant quota: %v", outcomes)
	}
	snap := mustVerify(t, l)
	if got := snap.Tenants["t"].LiveCounted; got != 2 {
		t.Fatalf("tenant live=%d", got)
	}
}

func TestWhitelistBypassesQuota(t *testing.T) {
	cfg := testConfig(1, 1, time.Minute)
	if err := cfg.Allow("m", lbl("instance", "wl")...); err != nil {
		t.Fatal(err)
	}
	clock := &ManualClock{}
	l := NewLimiter(cfg, clock.Now)

	r := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "wl"), Value: 1, Bytes: 5})
	if r.Outcome != AdmittedWhitelisted {
		t.Fatalf("whitelisted series: %v", r.Outcome)
	}
	// 白名单不占额度，普通系列仍可录取。
	r = l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "a"), Value: 1, Bytes: 5})
	if r.Outcome != AdmittedNew {
		t.Fatalf("regular series after whitelist: %v", r.Outcome)
	}
	// 额度已满，下一个普通系列折叠。
	r = l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "b"), Value: 1, Bytes: 5})
	if r.Outcome != Folded {
		t.Fatalf("over-quota series: %v", r.Outcome)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if m.LiveCounted != 1 || len(m.Series) != 2 {
		t.Fatalf("liveCounted=%d series=%d", m.LiveCounted, len(m.Series))
	}
}

func TestReservedLabelRejected(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 10, time.Minute), clock.Now)
	r := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl(OverflowLabelKey, "1"), Value: 1, Bytes: 5})
	if r.Outcome != Rejected {
		t.Fatalf("overflow label must be rejected: %v", r.Outcome)
	}
	r = l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("__anything", "x"), Value: 1, Bytes: 5})
	if r.Outcome != Rejected {
		t.Fatalf("reserved prefix must be rejected: %v", r.Outcome)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if m.Rejected != 2 || m.Received != 0 {
		t.Fatalf("rejected=%d received=%d", m.Rejected, m.Received)
	}
}

func TestDuplicateLabelKeyRejected(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 10, time.Minute), clock.Now)
	r := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("a", "1", "a", "2"), Value: 1, Bytes: 5})
	if r.Outcome != Rejected {
		t.Fatalf("duplicate key: %v", r.Outcome)
	}
}

func TestExpiryReclaimAllowsNewSeries(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(2, 2, time.Second), clock.Now)
	base := time.Unix(1000, 0)
	clock.Advance(base)
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "0"), Value: 1, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "1"), Value: 1, Bytes: 5})

	clock.Advance(base.Add(2 * time.Second))
	l.Reclaim()
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if len(m.Series) != 0 || len(m.Archived) != 2 {
		t.Fatalf("after reclaim: live=%d archived=%d", len(m.Series), len(m.Archived))
	}

	// 回收释放额度后允许新系列。
	r := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "2"), Value: 1, Bytes: 5})
	if r.Outcome != AdmittedNew {
		t.Fatalf("new series after reclaim: %v", r.Outcome)
	}
	mustVerify(t, l)
}

func TestLateSampleStartsNewGeneration(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(4, 4, time.Second), clock.Now)
	base := time.Unix(1000, 0)
	clock.Advance(base)
	r1 := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "0"), Value: 7, Bytes: 5})

	// 迟到样本：旧代际已过期，重新录取为新代际，统计互不混淆。
	clock.Advance(base.Add(3 * time.Second))
	r2 := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "0"), Value: 9, Bytes: 5})
	if r2.Outcome != AdmittedNew || r2.Generation == r1.Generation {
		t.Fatalf("late sample: %+v vs %+v", r2, r1)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	live := m.Series[r2.LabelKey]
	if live.Samples != 1 || live.Value != 9 {
		t.Fatalf("new generation: %+v", live)
	}
	if len(m.Archived) != 1 || m.Archived[0].Generation != r1.Generation || m.Archived[0].Value != 7 {
		t.Fatalf("archived generation: %+v", m.Archived)
	}
}

func TestOverflowContributionRetracted(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 1, time.Second), clock.Now)
	base := time.Unix(1000, 0)
	clock.Advance(base)
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "a"), Value: 1, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "b"), Value: 2, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "b"), Value: 3, Bytes: 5})

	// a 过期后额度释放，b 的样本触发录取并撤回其折叠贡献。
	clock.Advance(base.Add(2 * time.Second))
	r := l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "b"), Value: 4, Bytes: 5})
	if r.Outcome != RetractedAdmit {
		t.Fatalf("want retracted-admit, got %v", r.Outcome)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	b := m.Series[r.LabelKey]
	if b.Samples != 3 || b.Value != 9 {
		t.Fatalf("retracted series: %+v", b)
	}
	if m.OverflowSamples != 0 || m.FoldedTotal != 2 || m.RetractedSum != 2 {
		t.Fatalf("overflow=%d folded=%d retracted=%d", m.OverflowSamples, m.FoldedTotal, m.RetractedSum)
	}
}

func TestCorrectMovesStatsAndQuota(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 2, time.Minute), clock.Now)
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "1"), Value: 1, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "1"), Value: 2, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "2"), Value: 4, Bytes: 5})

	// 额度已满时更正：源释放的槽位让目标被录取。
	oc := l.Correct("t", "m", lbl("instance", "1"), lbl("instance", "3"))
	if oc != CorrectedAdmit {
		t.Fatalf("correct: %v", oc)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	if m.LiveCounted != 2 {
		t.Fatalf("liveCounted=%d", m.LiveCounted)
	}
	key3 := LabelKey(mustCanonical(t, lbl("instance", "3")))
	if got := m.Series[key3].Value; got != 3 {
		t.Fatalf("corrected series value=%d", got)
	}

	// 更正到已存活系列：统计合并。
	oc = l.Correct("t", "m", lbl("instance", "3"), lbl("instance", "2"))
	if oc != CorrectedMerge {
		t.Fatalf("merge: %v", oc)
	}
	snap = mustVerify(t, l)
	m = snap.Tenants["t"].Metrics["m"]
	key2 := LabelKey(mustCanonical(t, lbl("instance", "2")))
	if got := m.Series[key2].Value; got != 7 {
		t.Fatalf("merged value=%d", got)
	}
	if m.LiveCounted != 1 {
		t.Fatalf("liveCounted after merge=%d", m.LiveCounted)
	}
}

func TestCorrectFoldedContribution(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(10, 1, time.Minute), clock.Now)
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "a"), Value: 1, Bytes: 5})
	l.Ingest(Sample{Tenant: "t", Metric: "m", Labels: lbl("instance", "b"), Value: 2, Bytes: 5}) // folded

	// 折叠中的贡献被更正：额度仍满，统计以新组合名义留在溢出桶。
	oc := l.Correct("t", "m", lbl("instance", "b"), lbl("instance", "c"))
	if oc != CorrectedFold {
		t.Fatalf("correct folded: %v", oc)
	}
	snap := mustVerify(t, l)
	m := snap.Tenants["t"].Metrics["m"]
	keyC := LabelKey(mustCanonical(t, lbl("instance", "c")))
	if m.OverflowContribs[keyC] != 1 {
		t.Fatalf("contribs=%v", m.OverflowContribs)
	}

	// 再把 c 更正到已存活的 a：贡献撤回并合并进存活系列。
	oc = l.Correct("t", "m", lbl("instance", "c"), lbl("instance", "a"))
	if oc != CorrectedMerge {
		t.Fatalf("correct to live: %v", oc)
	}
	snap = mustVerify(t, l)
	m = snap.Tenants["t"].Metrics["m"]
	keyA := LabelKey(mustCanonical(t, lbl("instance", "a")))
	if got := m.Series[keyA].Value; got != 3 {
		t.Fatalf("merged value=%d", got)
	}
	if m.OverflowSamples != 0 {
		t.Fatalf("overflow=%d", m.OverflowSamples)
	}
}

func TestFairAllocationAcrossMetrics(t *testing.T) {
	clock := &ManualClock{}
	l := NewLimiter(testConfig(4, 2, time.Minute), clock.Now)
	// 指标 noisy 灌入 5 个系列：2 个录取、3 个折叠。
	for _, inst := range []string{"0", "1", "2", "3", "4"} {
		l.Ingest(Sample{Tenant: "t", Metric: "noisy", Labels: lbl("instance", inst), Value: 1, Bytes: 5})
	}
	// 指标 quiet 的系列不受 noisy 影响：指标级额度保证公平分配。
	for _, inst := range []string{"0", "1"} {
		r := l.Ingest(Sample{Tenant: "t", Metric: "quiet", Labels: lbl("instance", inst), Value: 1, Bytes: 5})
		if r.Outcome != AdmittedNew {
			t.Fatalf("quiet metric starved: %v", r.Outcome)
		}
	}
	snap := mustVerify(t, l)
	noisy := snap.Tenants["t"].Metrics["noisy"]
	quiet := snap.Tenants["t"].Metrics["quiet"]
	if noisy.LiveCounted != 2 || noisy.FoldedTotal != 3 {
		t.Fatalf("noisy: live=%d folded=%d", noisy.LiveCounted, noisy.FoldedTotal)
	}
	if quiet.LiveCounted != 2 {
		t.Fatalf("quiet: live=%d", quiet.LiveCounted)
	}
}

func mustCanonical(t *testing.T, labels []Label) []Label {
	t.Helper()
	c, err := CanonicalLabels(labels)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
