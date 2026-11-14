package netsec

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func baseConfig() Config {
	return Config{
		Keys: map[string]KeyPolicy{
			"region": {
				MaxLength: 8,
				Whitelist: map[string]struct{}{"us": {}, "eu": {}, "ap": {}},
				Policy:    Reject,
			},
			"host": {
				MaxLength:   16,
				Whitelist:   map[string]struct{}{"a": {}, "b": {}},
				Policy:      Bucket,
				BucketCount: 4,
			},
			"path": {
				MaxLength:   32,
				Policy:      Bucket,
				BucketCount: 16,
			},
			"request_id": {
				MaxLength:   64,
				Policy:      Bucket,
				BucketCount: 4,
			},
		},
		MaxLabels:              8,
		MaxSeriesPerTenant:     64,
		RatePerSecond:          100,
		Burst:                  100,
		MaxTenants:             128,
		AnomalySeriesThreshold: 5,
		AnomalyWindow:          time.Second,
		AlertRingSize:          64,
		AlertCooldown:          time.Hour,
	}
}

func mustGuard(t *testing.T, cfg Config) *Guard {
	t.Helper()
	g, err := NewGuard(cfg)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g
}

func TestLegalLabelsAcceptedAndStable(t *testing.T) {
	g := mustGuard(t, baseConfig())

	out, fp1, err := g.Admit("tenant-1", map[string]string{
		"region": "us",
		"host":   "a",
		"path":   "/v1/users",
	})
	if err != nil {
		t.Fatalf("admit legal labels: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d labels, want 3", len(out))
	}
	if out[0].Key != "host" || out[1].Key != "path" || out[2].Key != "region" {
		t.Fatalf("labels not canonicalized by key: %+v", out)
	}
	if got := out[2].Value; got != "us" {
		t.Fatalf("whitelisted value altered: got %q", got)
	}

	_, fp2, err := g.Admit("tenant-1", map[string]string{
		"path":   "/v1/users",
		"region": "us",
		"host":   "a",
	})
	if err != nil || fp1 != fp2 {
		t.Fatalf("fingerprint unstable: fp1=%d fp2=%d err=%v", fp1, fp2, err)
	}
	if g.SeriesCount("tenant-1") != 1 {
		t.Fatalf("repeated series created new entry: count=%d", g.SeriesCount("tenant-1"))
	}
}

func TestUnknownKeyAndTooManyLabels(t *testing.T) {
	g := mustGuard(t, baseConfig())

	if _, _, err := g.Admit("t", map[string]string{"evil": "x"}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
	if _, _, err := g.Admit("", map[string]string{"region": "us"}); !errors.Is(err, ErrTenantInvalid) {
		t.Fatalf("want ErrTenantInvalid, got %v", err)
	}

	cfg := baseConfig()
	cfg.MaxLabels = 2
	g = mustGuard(t, cfg)
	labels := map[string]string{
		"region": "us", "host": "a", "path": "/", "request_id": "r",
	}
	if _, _, err := g.Admit("t", labels); !errors.Is(err, ErrTooManyLabels) {
		t.Fatalf("want ErrTooManyLabels, got %v", err)
	}
}

func TestOverlongValueRejectAndBucket(t *testing.T) {
	g := mustGuard(t, baseConfig())

	if _, _, err := g.Admit("t", map[string]string{"region": "very-long-region"}); !errors.Is(err, ErrValueTooLong) {
		t.Fatalf("want ErrValueTooLong, got %v", err)
	}
	if _, _, err := g.Admit("t", map[string]string{"region": "moon"}); !errors.Is(err, ErrValueNotAllowed) {
		t.Fatalf("want ErrValueNotAllowed, got %v", err)
	}

	out, _, err := g.Admit("t", map[string]string{"host": "attacker-controlled-very-long-host"})
	if err != nil {
		t.Fatalf("bucketed host should be accepted: %v", err)
	}
	if out[0].Value != BucketOther {
		t.Fatalf("got %q, want %q", out[0].Value, BucketOther)
	}
	out2, _, err := g.Admit("t", map[string]string{"host": "another-unlisted-host"})
	if err != nil || out2[0].Value != BucketOther {
		t.Fatalf("unlisted hosts must share one bucket, got %v / %q", err, out2[0].Value)
	}
}

func TestUniqueLabelAttackIsBounded(t *testing.T) {
	cfg := baseConfig()
	cfg.RatePerSecond = 1_000_000
	cfg.Burst = 1_000_000
	g := mustGuard(t, cfg)

	for i := 0; i < 5000; i++ {
		if _, _, err := g.Admit("attacker", map[string]string{
			"request_id": fmt.Sprintf("req-%d-unique", i),
		}); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	if got := g.SeriesCount("attacker"); got > 4 {
		t.Fatalf("unique labels grew series to %d, want <= 4 buckets", got)
	}

	for i := 0; i < 1000; i++ {
		if _, _, err := g.Admit("attacker", map[string]string{
			"path": fmt.Sprintf("/users/%d/resources/%d", i, i*7),
		}); err != nil {
			t.Fatalf("path admit %d: %v", i, err)
		}
	}
	if got := g.SeriesCount("attacker"); got > 4+16 {
		t.Fatalf("free-form dimensions exceeded buckets: series=%d", got)
	}
}

func TestSeriesQuotaEnforcedAndAlerted(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxSeriesPerTenant = 3
	cfg.Keys["path"] = KeyPolicy{MaxLength: 32, Policy: Bucket, BucketCount: 32}
	g := mustGuard(t, cfg)

	quotaHit := false
	for i := 0; i < 40; i++ {
		_, _, err := g.Admit("t", map[string]string{"path": fmt.Sprintf("/unique/%d", i)})
		if errors.Is(err, ErrSeriesQuota) {
			quotaHit = true
			break
		}
		if err != nil {
			t.Fatalf("unexpected error on %d: %v", i, err)
		}
	}
	if !quotaHit {
		t.Fatal("never hit series quota")
	}
	if got := g.SeriesCount("t"); got != 3 {
		t.Fatalf("series=%d, want cap 3", got)
	}

	var found bool
	for _, a := range g.DrainAlerts() {
		if a.Kind == AlertSeriesQuota && a.Tenant == "t" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected AlertSeriesQuota")
	}
	if drained := g.DrainAlerts(); len(drained) != 0 {
		t.Fatalf("DrainAlerts did not clear buffer: %d left", len(drained))
	}
}

func TestRateLimitWithFakeClock(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := baseConfig()
	cfg.RatePerSecond = 2
	cfg.Burst = 3
	cfg.Clock = clk
	g := mustGuard(t, cfg)

	for i := 0; i < 3; i++ {
		if _, _, err := g.Admit("t", map[string]string{"region": "us"}); err != nil {
			t.Fatalf("burst request %d rejected: %v", i, err)
		}
	}
	if _, _, err := g.Admit("t", map[string]string{"region": "us"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}

	clk.advance(time.Second)
	for i := 0; i < 2; i++ {
		if _, _, err := g.Admit("t", map[string]string{"region": "us"}); err != nil {
			t.Fatalf("refilled request %d rejected: %v", i, err)
		}
	}
	if _, _, err := g.Admit("t", map[string]string{"region": "us"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited after refill, got %v", err)
	}

	var found bool
	for _, a := range g.DrainAlerts() {
		if a.Kind == AlertRateLimit {
			found = true
		}
	}
	if !found {
		t.Fatal("expected AlertRateLimit")
	}
}

func TestCrossTenantIsolationAndLRUEviction(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxTenants = 2
	g := mustGuard(t, cfg)

	labels := map[string]string{"path": "/shared"}
	for _, tenant := range []string{"alpha", "beta"} {
		if _, _, err := g.Admit(tenant, labels); err != nil {
			t.Fatalf("admit %s: %v", tenant, err)
		}
	}
	if g.SeriesCount("alpha") != 1 || g.SeriesCount("beta") != 1 {
		t.Fatal("per-tenant series are not isolated")
	}

	if _, _, err := g.Admit("gamma", labels); err != nil {
		t.Fatalf("admit gamma: %v", err)
	}
	if g.TenantCount() != 2 {
		t.Fatalf("tenant table grew to %d, want 2", g.TenantCount())
	}
	if _, ok := g.tenants["alpha"]; ok {
		t.Fatal("LRU tenant alpha was not evicted")
	}
	if _, ok := g.tenants["beta"]; !ok {
		t.Fatal("recently used tenant beta wrongly evicted")
	}

	var evicted bool
	for _, a := range g.DrainAlerts() {
		if a.Kind == AlertTenantsEvicted && a.Tenant == "alpha" {
			evicted = true
		}
	}
	if !evicted {
		t.Fatal("expected AlertTenantsEvicted for alpha")
	}

	if _, _, err := g.Admit("alpha", labels); err != nil {
		t.Fatalf("re-admit alpha: %v", err)
	}
}

func TestCardinalityAnomalyAlert(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := baseConfig()
	cfg.Clock = clk
	cfg.AnomalySeriesThreshold = 3
	cfg.AnomalyWindow = 10 * time.Second
	cfg.Burst = 100
	g := mustGuard(t, cfg)

	for i := 0; i < 4; i++ {
		if _, _, err := g.Admit("t", map[string]string{
			"path": fmt.Sprintf("/burst/%d", i),
		}); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}

	var found bool
	for _, a := range g.DrainAlerts() {
		if a.Kind == AlertCardinalityAnomaly && a.Series == 4 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected cardinality anomaly alert after 4 rapid new series")
	}

	clk.advance(11 * time.Second)
	if _, _, err := g.Admit("t", map[string]string{"path": "/late/new"}); err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	state := g.tenants["t"]
	if len(state.createdAt) != 1 {
		t.Fatalf("anomaly window not pruned to the new entry, entries=%d", len(state.createdAt))
	}
}

func TestAlertCooldownSuppressesDuplicates(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxSeriesPerTenant = 1
	cfg.Keys["path"] = KeyPolicy{MaxLength: 32, Policy: Bucket, BucketCount: 32}
	cfg.AlertCooldown = time.Minute
	g := mustGuard(t, cfg)

	for i := 0; i < 10; i++ {
		_, _, _ = g.Admit("t", map[string]string{"path": fmt.Sprintf("/x/%d", i)})
	}
	quotaAlerts := 0
	for _, a := range g.DrainAlerts() {
		if a.Kind == AlertSeriesQuota {
			quotaAlerts++
		}
	}
	if quotaAlerts != 1 {
		t.Fatalf("cooldown violated: %d quota alerts, want 1", quotaAlerts)
	}
}

func TestMemoryStaysBoundedUnderFlood(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxSeriesPerTenant = 32
	cfg.MaxTenants = 16
	cfg.AlertRingSize = 32
	cfg.AnomalySeriesThreshold = 0
	cfg.AnomalyWindow = 0
	cfg.RatePerSecond = 1_000_000
	cfg.Burst = 1_000_000
	g := mustGuard(t, cfg)

	warmup := func() {
		for i := 0; i < 2000; i++ {
			tenant := fmt.Sprintf("tenant-%d", i%64)
			if _, _, err := g.Admit(tenant, map[string]string{
				"request_id": fmt.Sprintf("req-%d", i),
				"path":       fmt.Sprintf("/p/%d", i),
			}); err != nil {
				t.Fatalf("admit %d: %v", i, err)
			}
		}
	}

	warmup()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	warmup()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Structural bounds are the real guarantee; the heap check confirms that
	// repeated flooding does not leak retained state.
	if g.TenantCount() > cfg.MaxTenants {
		t.Fatalf("tenant count %d exceeds cap %d", g.TenantCount(), cfg.MaxTenants)
	}
	for tenant, state := range g.tenants {
		if len(state.series) > cfg.MaxSeriesPerTenant {
			t.Fatalf("tenant %q holds %d series, cap %d", tenant, len(state.series), cfg.MaxSeriesPerTenant)
		}
	}
	if len(g.alerts.snapshot()) > cfg.AlertRingSize {
		t.Fatal("alert ring exceeded its capacity")
	}
	if growth := int64(after.HeapAlloc) - int64(before.HeapAlloc); growth > 2<<20 {
		t.Fatalf("heap grew by %d bytes across a second flood (possible unbounded retention)", growth)
	}
}

func TestConcurrentAdmits(t *testing.T) {
	cfg := baseConfig()
	cfg.RatePerSecond = 1_000_000
	cfg.Burst = 1_000_000
	g := mustGuard(t, cfg)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, _, err := g.Admit(fmt.Sprintf("tenant-%d", worker%4), map[string]string{
					"request_id": fmt.Sprintf("w%d-r%d", worker, i),
				})
				if err != nil {
					t.Errorf("admit: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	for i := 0; i < 4; i++ {
		if got := g.SeriesCount(fmt.Sprintf("tenant-%d", i)); got > 4 {
			t.Fatalf("tenant-%d series=%d exceeds request_id buckets", i, got)
		}
	}
}

func TestInvalidConfigsRejected(t *testing.T) {
	cases := map[string]func(Config) Config{
		"no keys": func(c Config) Config {
			c.Keys = nil
			return c
		},
		"zero series quota": func(c Config) Config {
			c.MaxSeriesPerTenant = 0
			return c
		},
		"zero rate": func(c Config) Config {
			c.RatePerSecond = 0
			return c
		},
		"free-form bucket without count": func(c Config) Config {
			c.Keys["path"] = KeyPolicy{Policy: Bucket}
			return c
		},
		"negative length": func(c Config) Config {
			p := c.Keys["region"]
			p.MaxLength = -1
			c.Keys["region"] = p
			return c
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewGuard(mutate(baseConfig())); !errors.Is(err, ErrConfig) {
				t.Fatalf("want ErrConfig, got %v", err)
			}
		})
	}
}
