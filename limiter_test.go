package cardinalguard

import (
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		TenantQuota: 3,
		MetricQuota: 2,
		TTL:         10,
		Whitelist:   []WhitelistEntry{{Tenant: "vip"}},
	}
}

func mustSample(t *testing.T, l *Limiter, tenant, metric string, labels LabelSet, tick, bytes int64) {
	t.Helper()
	if err := l.Sample(tenant, metric, labels, tick, bytes); err != nil {
		t.Fatalf("sample: %v", err)
	}
}

func mustInvariants(t *testing.T, l *Limiter) {
	t.Helper()
	for _, v := range l.CheckInvariants() {
		t.Errorf("invariant: %s", v)
	}
}

func TestCanonicalKeyOrderInsensitive(t *testing.T) {
	a := canonicalKey("t", "m", LabelSet{"host": "h1", "zone": "z1"})
	b := canonicalKey("t", "m", LabelSet{"zone": "z1", "host": "h1"})
	if a != b {
		t.Fatalf("label key order changed identity: %q vs %q", a, b)
	}

	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1", "zone": "z1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"zone": "z1", "host": "h1"}, 2, 10)
	if got := l.Snapshot().LiveSeries; got != 1 {
		t.Fatalf("reordered keys created a second series: live_series=%d", got)
	}
	mustInvariants(t, l)
}

func TestReservedLabelRejected(t *testing.T) {
	l := NewLimiter(testConfig())
	err := l.Sample("t", "m", LabelSet{OverflowLabel: "true"}, 1, 10)
	if err == nil {
		t.Fatal("real series with reserved overflow label was accepted")
	}
	if got := l.Snapshot().Received; got != 0 {
		t.Fatalf("rejected sample was accounted: received=%d", got)
	}
	// The synthetic bucket identity must differ from every admissible real key.
	real := canonicalKey("t", "m", LabelSet{"overflow": "true", "host": "h1"})
	if real == OverflowKey("t", "m") {
		t.Fatal("overflow bucket identity collides with a real series key")
	}
	mustInvariants(t, l)
}

func TestQuotaFoldAndCount(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h2"}, 2, 10)
	// Metric quota (2) exhausted: h3 folds into the overflow bucket.
	mustSample(t, l, "t", "m", LabelSet{"host": "h3"}, 3, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h3"}, 4, 10)

	snap := l.Snapshot()
	if snap.LiveSeries != 2 || snap.Overflow != 2 || snap.Folded != 2 {
		t.Fatalf("want live_series=2 overflow=2 folded=2, got %+v", snap)
	}
	// Folded samples are counted, not dropped: conservation must hold.
	if snap.Received != snap.Live+snap.Overflow+snap.Late+snap.Expired {
		t.Fatalf("conservation broken: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestTenantQuotaAppliesAcrossMetrics(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m1", LabelSet{"host": "a"}, 1, 10)
	mustSample(t, l, "t", "m1", LabelSet{"host": "b"}, 2, 10) // metric m1 full
	mustSample(t, l, "t", "m2", LabelSet{"host": "c"}, 3, 10) // tenant now full (3)
	mustSample(t, l, "t", "m2", LabelSet{"host": "d"}, 4, 10) // tenant quota blocks

	snap := l.Snapshot()
	if snap.LiveSeries != 3 || snap.Overflow != 1 {
		t.Fatalf("tenant quota not enforced: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestWhitelistBypassesQuota(t *testing.T) {
	l := NewLimiter(testConfig())
	for i := 0; i < 5; i++ {
		mustSample(t, l, "vip", "m", LabelSet{"host": fmt.Sprintf("h%d", i)}, int64(i+1), 10)
	}
	snap := l.Snapshot()
	if snap.LiveSeries != 5 || snap.Overflow != 0 {
		t.Fatalf("whitelisted series were limited: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestExpiryReclaimsAndAllowsNewSeries(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h2"}, 2, 10)
	l.Reap(20) // both expired (leases 11 and 12)
	if got := l.Snapshot().LiveSeries; got != 0 {
		t.Fatalf("expired series still live: %d", got)
	}
	// Quota was reclaimed: brand-new combinations are admitted again.
	mustSample(t, l, "t", "m", LabelSet{"host": "h3"}, 21, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h4"}, 22, 10)
	snap := l.Snapshot()
	if snap.LiveSeries != 2 || snap.Overflow != 0 || snap.Expired != 2 {
		t.Fatalf("reclaim did not free quota: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestLateSampleGenerationIsolation(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	l.Reap(20) // generation 1 reclaimed
	// Late arrival for the reclaimed generation: counted as late, and it
	// must not resurrect the series.
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 3, 10)
	snap := l.Snapshot()
	if snap.Late != 1 || snap.LiveSeries != 0 {
		t.Fatalf("late sample leaked into live state: %+v", snap)
	}
	// A fresh generation of the same combination is admitted...
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 25, 10)
	// ...and an even-later arrival for generation 1 is still isolated.
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 5, 10)
	snap = l.Snapshot()
	if snap.Late != 2 || snap.Live != 1 || snap.LiveSeries != 1 {
		t.Fatalf("generation isolation broken: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestOverflowPromotionIsFairFIFO(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h2"}, 2, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h3"}, 3, 10) // folded first
	mustSample(t, l, "t", "m", LabelSet{"host": "h4"}, 4, 10) // folded second
	l.Reap(11)                                                // h1 expires (lease 11); h2 survives (lease 12)

	// Exactly one slot freed: the oldest folded series wins it.
	keys := l.LiveSeriesKeys()
	promoted := canonicalKey("t", "m", LabelSet{"host": "h3"})
	found := false
	for _, k := range keys {
		if k == promoted {
			found = true
		}
	}
	if !found {
		t.Fatalf("oldest folded series was not promoted first: %q", keys)
	}
	snap := l.Snapshot()
	if snap.Overflow != 1 || snap.Promoted != 1 {
		t.Fatalf("unfair promotion: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestCorrectionTransfersStatsWithoutLoss(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "typo"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "typo"}, 2, 10)
	if err := l.Correct("t", "m", LabelSet{"host": "typo"}, LabelSet{"host": "fixed"}, 3); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	if snap.Live != 2 || snap.LiveSeries != 1 || snap.Corrected != 1 {
		t.Fatalf("correction lost statistics: %+v", snap)
	}
	// The corrected series keeps receiving samples under the new key.
	mustSample(t, l, "t", "m", LabelSet{"host": "fixed"}, 4, 10)
	if got := l.Snapshot().Live; got != 3 {
		t.Fatalf("corrected series did not stay live: live=%d", got)
	}
	mustInvariants(t, l)
}

func TestCorrectionOfOverflowContributionIsRevocable(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h2"}, 2, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "typo"}, 3, 10) // folded
	mustSample(t, l, "t", "m", LabelSet{"host": "typo"}, 4, 10) // folded
	// Correct the folded series to the h1 combination: its contribution is
	// withdrawn from the bucket and merged into the live series.
	if err := l.Correct("t", "m", LabelSet{"host": "typo"}, LabelSet{"host": "h1"}, 5); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	if snap.Overflow != 0 || snap.Live != 4 || snap.LiveSeries != 2 {
		t.Fatalf("contribution not revoked cleanly: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestCorrectionToFullQuotaFoldsWithoutLoss(t *testing.T) {
	l := NewLimiter(testConfig())
	mustSample(t, l, "t", "m", LabelSet{"host": "h1"}, 1, 10)
	mustSample(t, l, "t", "m", LabelSet{"host": "h2"}, 2, 10)
	mustSample(t, l, "t", "m2", LabelSet{"host": "x"}, 3, 10)   // tenant quota now full
	mustSample(t, l, "t", "m", LabelSet{"host": "typo"}, 4, 10) // folded: metric m full
	// Correcting the folded series cannot admit live (tenant quota stays
	// full); the statistics must land back in overflow, counted, not dropped.
	if err := l.Correct("t", "m", LabelSet{"host": "typo"}, LabelSet{"host": "h3"}, 5); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	if snap.Overflow != 1 || snap.LiveSeries != 3 ||
		snap.Received != snap.Live+snap.Overflow+snap.Late+snap.Expired {
		t.Fatalf("correction under full quota lost statistics: %+v", snap)
	}
	mustInvariants(t, l)
}

func TestConcurrentAdjustmentsAreAtomic(t *testing.T) {
	l := NewLimiter(Config{TenantQuota: 8, MetricQuota: 4, TTL: 10})
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tick := int64(i%50) + 1
				tenant := fmt.Sprintf("t%d", (worker+i)%3)
				metric := fmt.Sprintf("m%d", (worker+i)%4)
				host := fmt.Sprintf("h%d", (worker*7+i)%12)
				switch i % 10 {
				case 0:
					l.Reap(tick)
				case 1:
					_ = l.Correct(tenant, metric,
						LabelSet{"host": host},
						LabelSet{"host": host, "fixed": "yes"}, tick)
				default:
					_ = l.Sample(tenant, metric, LabelSet{"host": host}, tick, 10)
				}
			}
		}(worker)
	}
	wg.Wait()
	mustInvariants(t, l)
	snap := l.Snapshot()
	if snap.Received != snap.Live+snap.Overflow+snap.Late+snap.Expired {
		t.Fatalf("conservation broken under concurrency: %+v", snap)
	}
}
