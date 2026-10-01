package cardinalguard

import "fmt"

// CheckInvariants verifies the safety properties of the limiter. It returns
// one human-readable violation per problem found; an empty slice means all
// invariants hold. The checked properties are:
//
//  1. Receive conservation: every received sample/byte is accounted as
//     live, overflow, late or expired — nothing is silently dropped.
//  2. Ledger/index consistency: the byte ledger mirrors the live series
//     index key-for-key and byte-for-byte.
//  3. Quota safety: non-whitelisted live series never exceed the tenant or
//     metric quota, and internal counters match the actual index.
//  4. Fair allocation: the overflow bucket is non-empty only while quota is
//     exhausted (promotion is eager and FIFO).
//  5. Bucket integrity: contribution records sum to the bucket totals and
//     the FIFO order matches the contribution set.
func (l *Limiter) CheckInvariants() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var violations []string
	fail := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	var liveSamples, liveBytes int64
	tenantActual := make(map[string]int)
	metricActual := make(map[string]int)
	for key, s := range l.series {
		liveSamples += s.Samples
		liveBytes += s.Bytes
		if !s.Whitelisted {
			tenantActual[s.Tenant]++
			metricActual[mkey(s.Tenant, s.Metric)]++
		}
		if got := l.ledger.Get(key); got != s.Bytes {
			fail("ledger/index mismatch for series %q: ledger=%d series=%d", key, got, s.Bytes)
		}
	}
	if len(l.ledger.live) != len(l.series) {
		fail("ledger/index size mismatch: ledger=%d series=%d", len(l.ledger.live), len(l.series))
	}
	if l.ledger.LiveTotal() != liveBytes {
		fail("ledger live bytes %d != series live bytes %d", l.ledger.LiveTotal(), liveBytes)
	}

	var overflowSamples, overflowBytes int64
	for mk, b := range l.buckets {
		overflowSamples += b.samples
		overflowBytes += b.bytes
		var contribSamples, contribBytes int64
		if len(b.order) != len(b.contrib) {
			fail("bucket %q: order len %d != contrib len %d", mk, len(b.order), len(b.contrib))
		}
		seen := make(map[string]bool, len(b.order))
		for _, k := range b.order {
			seen[k] = true
		}
		for k, c := range b.contrib {
			contribSamples += c.samples
			contribBytes += c.bytes
			if !seen[k] {
				fail("bucket %q: contribution %q missing from promotion order", mk, k)
			}
		}
		if contribSamples != b.samples || contribBytes != b.bytes {
			fail("bucket %q: contributions (%d samples, %d bytes) != bucket totals (%d, %d)",
				mk, contribSamples, contribBytes, b.samples, b.bytes)
		}
		if b.samples > 0 {
			tenant, _ := splitMKey(mk)
			if l.tenantCount[tenant] < l.cfg.TenantQuota &&
				l.metricCount[mk] < l.cfg.MetricQuota {
				fail("fairness: bucket %q holds %d folded samples while quota is available", mk, b.samples)
			}
		}
	}

	if total := liveSamples + overflowSamples + l.stats.Late + l.stats.Expired; total != l.stats.Received {
		fail("sample conservation: live %d + overflow %d + late %d + expired %d = %d, received %d",
			liveSamples, overflowSamples, l.stats.Late, l.stats.Expired, total, l.stats.Received)
	}
	if total := l.ledger.LiveTotal() + l.ledger.Overflow + l.ledger.Late + l.ledger.Expired; total != l.stats.ReceivedBytes {
		fail("byte conservation: ledger total %d != received bytes %d", total, l.stats.ReceivedBytes)
	}
	if l.ledger.Overflow != overflowBytes {
		fail("ledger overflow bytes %d != bucket bytes %d", l.ledger.Overflow, overflowBytes)
	}
	if l.ledger.Late != l.stats.LateBytes {
		fail("ledger late bytes %d != stats late bytes %d", l.ledger.Late, l.stats.LateBytes)
	}
	if l.ledger.Expired != l.stats.ExpiredBytes {
		fail("ledger expired bytes %d != stats expired bytes %d", l.ledger.Expired, l.stats.ExpiredBytes)
	}

	for tenant, n := range tenantActual {
		if l.tenantCount[tenant] != n {
			fail("tenant counter %q = %d, actual %d", tenant, l.tenantCount[tenant], n)
		}
		if n > l.cfg.TenantQuota {
			fail("tenant %q exceeds quota: %d > %d", tenant, n, l.cfg.TenantQuota)
		}
	}
	for mk, n := range metricActual {
		if l.metricCount[mk] != n {
			fail("metric counter %q = %d, actual %d", mk, l.metricCount[mk], n)
		}
		if n > l.cfg.MetricQuota {
			fail("metric %q exceeds quota: %d > %d", mk, n, l.cfg.MetricQuota)
		}
	}
	return violations
}
