package netsec

import "time"

// AlertKind classifies the anomaly that raised an Alert.
type AlertKind string

const (
	// AlertSeriesQuota fires when a tenant hits its distinct-series cap.
	AlertSeriesQuota AlertKind = "series_quota_exceeded"
	// AlertRateLimit fires when a tenant exceeds its request rate.
	AlertRateLimit AlertKind = "rate_limited"
	// AlertCardinalityAnomaly fires when a tenant creates new series faster
	// than Config.AnomalyWindow / AnomalySeriesThreshold allow.
	AlertCardinalityAnomaly AlertKind = "cardinality_anomaly"
	// AlertTenantsEvicted fires when the global tenant table is full and an
	// idle tenant is evicted, indicating pressure on per-tenant state.
	AlertTenantsEvicted AlertKind = "tenant_table_eviction"
)

// Alert describes one anomalous event. Alerts are also retained in a bounded
// ring inside the Guard and can be drained with DrainAlerts.
type Alert struct {
	Time    time.Time
	Tenant  string
	Kind    AlertKind
	Detail  string
	Series  int
	Tenants int
}

// alertRing is a fixed-capacity circular buffer. Overwriting old entries means
// alert storage can never grow without bound.
type alertRing struct {
	entries []Alert
	head    int
	full    bool
}

func newAlertRing(capacity int) *alertRing {
	if capacity < 1 {
		capacity = 1
	}
	return &alertRing{entries: make([]Alert, 0, capacity)}
}

func (r *alertRing) push(a Alert) {
	if cap(r.entries) == 0 {
		return
	}
	if len(r.entries) < cap(r.entries) {
		r.entries = append(r.entries, a)
		return
	}
	r.entries[r.head] = a
	r.head = (r.head + 1) % cap(r.entries)
	r.full = true
}

// snapshot returns alerts in chronological order without clearing the ring.
func (r *alertRing) snapshot() []Alert {
	if !r.full {
		out := make([]Alert, len(r.entries))
		copy(out, r.entries)
		return out
	}
	n := cap(r.entries)
	out := make([]Alert, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, r.entries[(r.head+i)%n])
	}
	return out
}
