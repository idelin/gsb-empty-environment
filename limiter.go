package cardinalguard

import (
	"fmt"
	"sort"
	"sync"
)

// Config configures a Limiter. Quotas apply at two levels: per tenant and
// per (tenant, metric). TTL is expressed in logical ticks; a series expires
// when a reap tick reaches its ExpiresAt.
type Config struct {
	TenantQuota int
	MetricQuota int
	TTL         int64
	Whitelist   []WhitelistEntry
}

// WhitelistEntry matches series that are always admitted live and never
// consume quota. Empty Tenant/Metric act as wildcards; Labels is a subset
// match against the series labels.
type WhitelistEntry struct {
	Tenant string
	Metric string
	Labels LabelSet
}

func (e WhitelistEntry) matches(tenant, metric string, labels LabelSet) bool {
	if e.Tenant != "" && e.Tenant != tenant {
		return false
	}
	if e.Metric != "" && e.Metric != metric {
		return false
	}
	for k, v := range e.Labels {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// Series is one live, admitted label combination.
type Series struct {
	Key         string
	Tenant      string
	Metric      string
	Labels      LabelSet
	Samples     int64
	Bytes       int64
	CreatedTick int64
	ExpiresAt   int64
	Whitelisted bool
}

// contribution is a revocable record of samples folded into the overflow
// bucket on behalf of one original series key. Contributions can be
// withdrawn (label correction) or promoted (quota freed) without losing
// their statistics.
type contribution struct {
	samples   int64
	bytes     int64
	labels    LabelSet
	firstTick int64
}

// overflowBucket is the deterministic aggregation target for one
// (tenant, metric) when its quota is exhausted.
type overflowBucket struct {
	samples int64
	bytes   int64
	contrib map[string]*contribution
	order   []string // FIFO of first fold per key, drives fair promotion
}

func newOverflowBucket() *overflowBucket {
	return &overflowBucket{contrib: make(map[string]*contribution)}
}

// Stats are cumulative counters. Folded counts every sample ever routed to
// overflow (including later-promoted ones); conservation is expressed with
// the live counters instead:
//
//	Received == Live + Overflow + Late + Expired   (samples and bytes)
type Stats struct {
	Received      int64
	ReceivedBytes int64
	Late          int64
	LateBytes     int64
	Folded        int64
	Expired       int64
	ExpiredBytes  int64
	Corrected     int64
	Promoted      int64
}

// Limiter admits series under two-level cardinality quotas, folds overflow
// into a deterministic bucket, reclaims expired series and keeps a byte
// ledger consistent with the live index. All state transitions take one
// mutex, so concurrent samples, reaps and corrections adjust quota
// atomically.
type Limiter struct {
	mu          sync.Mutex
	cfg         Config
	now         int64
	series      map[string]*Series
	tenantCount map[string]int
	metricCount map[string]int
	buckets     map[string]*overflowBucket
	ledger      *Ledger
	stats       Stats
}

func NewLimiter(cfg Config) *Limiter {
	return &Limiter{
		cfg:         cfg,
		series:      make(map[string]*Series),
		tenantCount: make(map[string]int),
		metricCount: make(map[string]int),
		buckets:     make(map[string]*overflowBucket),
		ledger:      NewLedger(),
	}
}

func (l *Limiter) advanceClock(tick int64) {
	if tick > l.now {
		l.now = tick
	}
}

func (l *Limiter) whitelisted(tenant, metric string, labels LabelSet) bool {
	for _, e := range l.cfg.Whitelist {
		if e.matches(tenant, metric, labels) {
			return true
		}
	}
	return false
}

func (l *Limiter) hasCapacity(tenant, metric string) bool {
	return l.tenantCount[tenant] < l.cfg.TenantQuota &&
		l.metricCount[mkey(tenant, metric)] < l.cfg.MetricQuota
}

// Sample ingests one sample. Every sample is accounted for exactly once: it
// lands in a live series, in the overflow bucket, or in the late counter.
func (l *Limiter) Sample(tenant, metric string, labels LabelSet, tick, bytes int64) error {
	if err := validateScope(tenant, metric); err != nil {
		return err
	}
	if err := ValidateLabels(labels); err != nil {
		return err
	}
	if bytes < 0 {
		return fmt.Errorf("negative sample bytes")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advanceClock(tick)
	l.stats.Received++
	l.stats.ReceivedBytes += bytes

	key := canonicalKey(tenant, metric, labels)
	if s, ok := l.series[key]; ok {
		if tick < s.CreatedTick {
			// Late arrival for a previous, already-reclaimed generation
			// of this combination: count it, never merge it into the
			// current generation.
			l.countLate(bytes)
			return nil
		}
		s.Samples++
		s.Bytes += bytes
		s.ExpiresAt = tick + l.cfg.TTL
		l.ledger.Add(key, bytes)
		return nil
	}
	if tick+l.cfg.TTL <= l.now {
		// Late on arrival: would already be expired under the clock the
		// limiter has observed, so admitting it would be immediately
		// reaped. Count it as late instead of silently dropping it.
		l.countLate(bytes)
		return nil
	}
	if l.whitelisted(tenant, metric, labels) {
		l.admit(key, tenant, metric, labels, 1, bytes, tick, true)
		return nil
	}
	if l.hasCapacity(tenant, metric) {
		l.admit(key, tenant, metric, labels, 1, bytes, tick, false)
		return nil
	}
	l.fold(tenant, metric, key, labels, 1, bytes, tick, true)
	return nil
}

func (l *Limiter) countLate(bytes int64) {
	l.stats.Late++
	l.stats.LateBytes += bytes
	l.ledger.Late += bytes
}

// admit installs a live series. The caller must hold the lock and must have
// verified capacity (or whitelist) first, so a series is never counted into
// quota and then dropped.
func (l *Limiter) admit(key, tenant, metric string, labels LabelSet, samples, bytes, createdTick int64, whitelisted bool) {
	l.series[key] = &Series{
		Key:         key,
		Tenant:      tenant,
		Metric:      metric,
		Labels:      copyLabels(labels),
		Samples:     samples,
		Bytes:       bytes,
		CreatedTick: createdTick,
		ExpiresAt:   l.now + l.cfg.TTL,
		Whitelisted: whitelisted,
	}
	if !whitelisted {
		l.tenantCount[tenant]++
		l.metricCount[mkey(tenant, metric)]++
	}
	l.ledger.Add(key, bytes)
}

// fold routes samples into the deterministic overflow bucket, recording a
// revocable contribution keyed by the original series key. countAsNew is
// false when re-folding statistics that were already accounted as folded
// (e.g. a correction moving an existing contribution).
func (l *Limiter) fold(tenant, metric, key string, labels LabelSet, samples, bytes, tick int64, countAsNew bool) {
	mk := mkey(tenant, metric)
	b := l.buckets[mk]
	if b == nil {
		b = newOverflowBucket()
		l.buckets[mk] = b
	}
	b.samples += samples
	b.bytes += bytes
	c, ok := b.contrib[key]
	if !ok {
		c = &contribution{labels: copyLabels(labels), firstTick: tick}
		b.contrib[key] = c
		b.order = append(b.order, key)
	}
	c.samples += samples
	c.bytes += bytes
	if countAsNew {
		l.stats.Folded += samples
	}
	l.ledger.Overflow += bytes
}

// Reap reclaims every series whose lease expired at or before tick, frees
// their quota, then promotes waiting overflow contributions FIFO into the
// freed capacity.
func (l *Limiter) Reap(tick int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advanceClock(tick)
	for key, s := range l.series {
		if s.ExpiresAt <= tick {
			delete(l.series, key)
			if !s.Whitelisted {
				l.tenantCount[s.Tenant]--
				l.metricCount[mkey(s.Tenant, s.Metric)]--
			}
			l.stats.Expired += s.Samples
			l.stats.ExpiredBytes += s.Bytes
			l.ledger.Expired += l.ledger.Remove(key)
		}
	}
	// Deterministic promotion order across buckets.
	mks := make([]string, 0, len(l.buckets))
	for mk, b := range l.buckets {
		if len(b.order) > 0 {
			mks = append(mks, mk)
		}
	}
	sort.Strings(mks)
	for _, mk := range mks {
		tenant, metric := splitMKey(mk)
		l.promoteLocked(tenant, metric)
	}
}

// promoteLocked moves overflow contributions into live series, oldest
// first, while capacity remains. Statistics move with the contribution:
// nothing is counted into quota and then dropped.
func (l *Limiter) promoteLocked(tenant, metric string) {
	mk := mkey(tenant, metric)
	b := l.buckets[mk]
	if b == nil {
		return
	}
	for len(b.order) > 0 && l.hasCapacity(tenant, metric) {
		key := b.order[0]
		c := b.contrib[key]
		l.series[key] = &Series{
			Key:         key,
			Tenant:      tenant,
			Metric:      metric,
			Labels:      copyLabels(c.labels),
			Samples:     c.samples,
			Bytes:       c.bytes,
			CreatedTick: c.firstTick,
			ExpiresAt:   l.now + l.cfg.TTL,
		}
		l.tenantCount[tenant]++
		l.metricCount[mk]++
		l.ledger.Overflow -= c.bytes
		l.ledger.Add(key, c.bytes)
		b.samples -= c.samples
		b.bytes -= c.bytes
		delete(b.contrib, key)
		b.order = b.order[1:]
		l.stats.Promoted++
	}
}

// Correct atomically rewrites the labels of a series: the old combination's
// live series and/or revocable overflow contribution is withdrawn and its
// full statistics are re-admitted under the corrected combination. If the
// corrected combination cannot be admitted live, the statistics are folded
// into overflow under the new key — never dropped.
func (l *Limiter) Correct(tenant, metric string, oldLabels, newLabels LabelSet, tick int64) error {
	if err := validateScope(tenant, metric); err != nil {
		return err
	}
	if err := ValidateLabels(oldLabels); err != nil {
		return err
	}
	if err := ValidateLabels(newLabels); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advanceClock(tick)

	oldKey := canonicalKey(tenant, metric, oldLabels)
	newKey := canonicalKey(tenant, metric, newLabels)
	if oldKey == newKey {
		// Same combination (e.g. only key order differed): nothing to do.
		return nil
	}
	mk := mkey(tenant, metric)

	var liveSamples, liveBytes, overflowSamples, overflowBytes int64
	firstTick := tick
	found := false

	// Withdraw the revocable overflow contribution of the old key.
	if b := l.buckets[mk]; b != nil {
		if c, ok := b.contrib[oldKey]; ok {
			overflowSamples = c.samples
			overflowBytes = c.bytes
			firstTick = c.firstTick
			b.samples -= c.samples
			b.bytes -= c.bytes
			l.ledger.Overflow -= c.bytes
			delete(b.contrib, oldKey)
			for i, k := range b.order {
				if k == oldKey {
					b.order = append(b.order[:i], b.order[i+1:]...)
					break
				}
			}
			found = true
		}
	}
	// Detach the live series under the old key.
	if s, ok := l.series[oldKey]; ok {
		liveSamples = s.Samples
		liveBytes = s.Bytes
		if s.CreatedTick < firstTick {
			firstTick = s.CreatedTick
		}
		delete(l.series, oldKey)
		if !s.Whitelisted {
			l.tenantCount[tenant]--
			l.metricCount[mk]--
		}
		l.ledger.Remove(oldKey)
		found = true
	}
	if !found {
		return nil
	}
	l.stats.Corrected++
	samples := liveSamples + overflowSamples
	bytes := liveBytes + overflowBytes

	// Re-admit under the corrected key, preserving every accounted sample.
	if s, ok := l.series[newKey]; ok {
		s.Samples += samples
		s.Bytes += bytes
		l.ledger.Add(newKey, bytes)
	} else if l.whitelisted(tenant, metric, newLabels) {
		l.admit(newKey, tenant, metric, newLabels, samples, bytes, firstTick, true)
	} else if l.hasCapacity(tenant, metric) {
		l.admit(newKey, tenant, metric, newLabels, samples, bytes, firstTick, false)
	} else {
		if liveSamples > 0 {
			l.fold(tenant, metric, newKey, newLabels, liveSamples, liveBytes, firstTick, true)
		}
		if overflowSamples > 0 {
			l.fold(tenant, metric, newKey, newLabels, overflowSamples, overflowBytes, firstTick, false)
		}
	}
	// Capacity may have been freed (merge or whitelist path): promote
	// waiting contributions to preserve the fairness invariant.
	l.promoteLocked(tenant, metric)
	return nil
}

// Snapshot is a consistent read-only view of the limiter state.
type Snapshot struct {
	Received      int64
	ReceivedBytes int64
	Live          int64
	LiveBytes     int64
	LiveSeries    int
	Overflow      int64
	OverflowBytes int64
	Late          int64
	LateBytes     int64
	Expired       int64
	ExpiredBytes  int64
	Folded        int64
	Promoted      int64
	Corrected     int64
}

func (l *Limiter) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := Snapshot{
		Received:      l.stats.Received,
		ReceivedBytes: l.stats.ReceivedBytes,
		LiveSeries:    len(l.series),
		OverflowBytes: l.ledger.Overflow,
		Late:          l.stats.Late,
		LateBytes:     l.stats.LateBytes,
		Expired:       l.stats.Expired,
		ExpiredBytes:  l.stats.ExpiredBytes,
		Folded:        l.stats.Folded,
		Promoted:      l.stats.Promoted,
		Corrected:     l.stats.Corrected,
	}
	for _, s := range l.series {
		snap.Live += s.Samples
		snap.LiveBytes += s.Bytes
	}
	for _, b := range l.buckets {
		snap.Overflow += b.samples
	}
	return snap
}

// LiveSeriesKeys returns the sorted canonical keys of all live series.
func (l *Limiter) LiveSeriesKeys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	keys := make([]string, 0, len(l.series))
	for k := range l.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// OverflowKey exposes the deterministic bucket identity for a (tenant,
// metric), for tests and tooling.
func OverflowKey(tenant, metric string) string { return overflowKey(tenant, metric) }
