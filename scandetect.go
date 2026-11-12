// Package netsec provides a streaming detector for low-rate port scans.
//
// The detector consumes connection lifecycle events (start/failure/success),
// disambiguates retransmissions and out-of-order results on a per-connection
// basis, and raises (and withdraws) alerts over event-time windows defined by
// fixed connection start times.
package netsec

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// EventKind identifies the lifecycle phase carried by an Event.
type EventKind uint8

const (
	// EventStart marks the beginning of a connection attempt.
	EventStart EventKind = iota + 1
	// EventFailure marks a failed connection attempt.
	EventFailure
	// EventSuccess marks a successful connection attempt.
	EventSuccess
)

// Endpoint is an IP address and port pair.
type Endpoint struct {
	Addr string
	Port uint16
}

// Event is a single connection lifecycle observation.
//
// Every event kind carries the full identity and fixed metadata of the
// connection instance: the probe generation, the per-generation connection
// instance number, the fixed start time s, and both endpoints.
type Event struct {
	Kind       EventKind
	Generation uint64
	Instance   uint64
	Start      time.Time
	Source     Endpoint
	Target     Endpoint
}

// InstanceID uniquely identifies a connection instance within the detector:
// a connection instance number scoped to one probe generation.
type InstanceID struct {
	Generation uint64
	Instance   uint64
}

// IngestStatus reports how an event was handled by Ingest.
type IngestStatus uint8

const (
	// IngestAccepted stores a brand-new connection instance.
	IngestAccepted IngestStatus = iota + 1
	// IngestDuplicate addresses an already known instance without conflicting.
	// It covers no-op repeats and also a success overriding a prior failure;
	// no new instance is created and no counter is double counted.
	IngestDuplicate
	// IngestConflict carries metadata that contradicts a known instance.
	IngestConflict
	// IngestLate is rejected because its start time is below the watermark.
	IngestLate
	// IngestInvalid is malformed (unknown kind or missing identity fields).
	IngestInvalid
	// IngestMemoryLimit is rejected because the memory budget is exhausted.
	IngestMemoryLimit
)

// IngestResult is the outcome of feeding one event to the detector.
type IngestResult struct {
	// Status classifies the handling of the event.
	Status IngestStatus
	// Instance is the key the event refers to.
	Instance InstanceID
	// Late is true when the event was rejected as arriving behind the
	// watermark, including a rejected correction of an existing instance.
	Late bool
	// Conflicting is true when the event's metadata did not match the
	// metadata already registered for its (generation, instance) pair.
	Conflicting bool
}

// instanceState is the resolved lifecycle state of one connection instance.
type instanceState uint8

const (
	stateOpen instanceState = iota
	stateFailed
	stateSucceeded
)

// instance stores the resolved state of one connection attempt.
type instance struct {
	start    time.Time
	source   Endpoint
	target   Endpoint
	state    instanceState
	bytesEst int
}

// AlertKind distinguishes the two scan signals.
type AlertKind uint8

const (
	// AlertHostPortSweep fires when one source hits the distinct-failed-port
	// threshold against a single destination host within a window.
	AlertHostPortSweep AlertKind = iota + 1
	// AlertPortTargetSweep fires when one source hits the distinct-failed-host
	// threshold against one destination port within a window.
	AlertPortTargetSweep
)

// Alert is one threshold breach in a query window.
type Alert struct {
	Kind   AlertKind
	Source string
	// Host is the destination host for AlertHostPortSweep.
	Host string
	// Port is the destination port for AlertPortTargetSweep.
	Port uint16
	// Count is the number of distinct failed ports (host sweep) or distinct
	// failed target hosts (port sweep) backing the alert.
	Count int
}

// QueryID identifies a registered window query.
type QueryID uint64

// Query is a windowed scan query. Window W, both thresholds and the query
// time t must be positive. The window is [t-W, t) measured on start time.
type Query struct {
	Window        time.Duration
	PortThreshold int
	HostThreshold int
	At            time.Time
}

// QuerySnapshot is the current answer to a registered query.
type QuerySnapshot struct {
	ID         QueryID
	Query      Query
	Frozen     bool
	Incomplete bool
	Alerts     []Alert
}

// ChangeKind classifies an alert lifecycle change.
type ChangeKind uint8

const (
	// ChangeRaised marks an alert that became active.
	ChangeRaised ChangeKind = iota + 1
	// ChangeWithdrawn marks an alert that a later correction removed.
	ChangeWithdrawn
)

// Change is one alert lifecycle transition observed by the detector.
type Change struct {
	Kind    ChangeKind
	QueryID QueryID
	Alert   Alert
	Seq     uint64
}

// ChangeFunc is invoked outside the detector lock after a state-changing
// ingest. A callback must not call back into the same detector.
type ChangeFunc func(Change)

// Detector is a streaming scan detector safe for concurrent use.
type Detector struct {
	mu          sync.RWMutex
	watermark   time.Time
	instances   map[InstanceID]*instance
	queries     map[QueryID]*queryState
	nextQueryID QueryID
	nextSeq     uint64
	memUsed     int
	memLimit    int
	incomplete  bool
	changes     []Change
	onChange    ChangeFunc
}

type queryState struct {
	query  Query
	frozen bool
	alerts map[alertKey]Alert
}

// alertKey is the identity of an alert within one query window.
type alertKey struct {
	kind   AlertKind
	source string
	host   string
	port   uint16
}

// Option configures a Detector at construction.
type Option func(*Detector)

// WithMemoryLimit caps the approximate resident state of the detector at
// limit bytes. A zero or negative limit means unlimited.
func WithMemoryLimit(limit int) Option {
	return func(d *Detector) { d.memLimit = limit }
}

// WithChangeCallback installs a callback fired for every alert raise and
// withdrawal, outside the detector lock.
func WithChangeCallback(fn ChangeFunc) Option {
	return func(d *Detector) { d.onChange = fn }
}

// NewDetector creates an empty detector with the watermark at the zero time.
func NewDetector(opts ...Option) *Detector {
	d := &Detector{
		instances: make(map[InstanceID]*instance),
		queries:   make(map[QueryID]*queryState),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// instanceOverhead is the conservative fixed footprint of a tracked instance,
// covering the map slot, struct headers and endpoint/string headers.
const instanceOverhead = 256

func estimateBytes(e Event) int {
	return instanceOverhead + len(e.Source.Addr) + len(e.Target.Addr)
}

func (e Event) valid() bool {
	if e.Kind != EventStart && e.Kind != EventFailure && e.Kind != EventSuccess {
		return false
	}
	if e.Start.IsZero() || e.Source.Addr == "" || e.Target.Addr == "" {
		return false
	}
	return true
}

func metadataEqual(e Event, in *instance) bool {
	return e.Start.Equal(in.start) && e.Source == in.source && e.Target == in.target
}

// Ingest applies one lifecycle event.
//
// Duplicate events that repeat already-resolved state are acknowledged without
// changing any count. Metadata conflicts on a reused (generation, instance)
// pair are rejected. Success definitively overrides a prior failure of the
// same instance; a failure arriving after success is a duplicate and cannot
// resurrect a counted failure. Any insertion or correction whose start time is
// below the current watermark is rejected as late.
func (d *Detector) Ingest(e Event) IngestResult {
	id := InstanceID{Generation: e.Generation, Instance: e.Instance}
	if !e.valid() {
		return IngestResult{Status: IngestInvalid, Instance: id}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	existing, known := d.instances[id]
	if known {
		if !metadataEqual(e, existing) {
			return IngestResult{
				Status:      IngestConflict,
				Instance:    id,
				Conflicting: true,
			}
		}
		late := e.Start.Before(d.watermark)
		if late {
			if stateChanging(e.Kind, existing.state) {
				return IngestResult{Status: IngestLate, Instance: id, Late: true}
			}
			return IngestResult{Status: IngestDuplicate, Instance: id, Late: true}
		}
		if !stateChanging(e.Kind, existing.state) {
			return IngestResult{Status: IngestDuplicate, Instance: id}
		}

		existing.state = nextState(e.Kind, existing.state)
		changes := d.recomputeOpenLocked()
		d.dispatchLocked(changes)
		return IngestResult{Status: IngestDuplicate, Instance: id}
	}

	if e.Start.Before(d.watermark) {
		return IngestResult{Status: IngestLate, Instance: id, Late: true}
	}
	cost := estimateBytes(e)
	if d.memLimit > 0 && d.memUsed+cost > d.memLimit {
		d.incomplete = true
		return IngestResult{Status: IngestMemoryLimit, Instance: id}
	}

	d.instances[id] = &instance{
		start:    e.Start,
		source:   e.Source,
		target:   e.Target,
		state:    nextState(e.Kind, stateOpen),
		bytesEst: cost,
	}
	d.memUsed += cost
	changes := d.recomputeOpenLocked()
	d.dispatchLocked(changes)
	return IngestResult{Status: IngestAccepted, Instance: id}
}

// stateChanging reports whether an event moves an instance to a different
// resolved state. Repeated starts, repeated failures, failures after success
// and repeated successes are non-changing duplicates.
func stateChanging(kind EventKind, current instanceState) bool {
	return nextState(kind, current) != current
}

// nextState applies the success-overrides-failure ordering. Success is
// terminal; a start alone leaves an attempt open and uncounted.
func nextState(kind EventKind, current instanceState) instanceState {
	switch kind {
	case EventStart:
		return current
	case EventFailure:
		if current == stateOpen {
			return stateFailed
		}
		return current
	case EventSuccess:
		if current != stateSucceeded {
			return stateSucceeded
		}
		return current
	default:
		return current
	}
}

// recomputeOpenLocked fully recalculates every non-frozen query from the
// instance table, returning alert raises and withdrawals relative to each
// query's previously reported alerts. Frozen windows are untouched.
func (d *Detector) recomputeOpenLocked() []Change {
	var changes []Change
	for id, qs := range d.queries {
		if qs.frozen {
			continue
		}
		fresh := evaluate(d.instances, qs.query)
		changes = append(changes, diffAlerts(id, qs.alerts, fresh)...)
		qs.alerts = fresh
	}
	return changes
}

func diffAlerts(id QueryID, old, fresh map[alertKey]Alert) []Change {
	var changes []Change
	for key, alert := range fresh {
		if prev, ok := old[key]; !ok || prev.Count != alert.Count {
			changes = append(changes, Change{Kind: ChangeRaised, QueryID: id, Alert: alert})
		}
	}
	for key, alert := range old {
		if _, ok := fresh[key]; !ok {
			changes = append(changes, Change{Kind: ChangeWithdrawn, QueryID: id, Alert: alert})
		}
	}
	sortChanges(changes)
	return changes
}

func (d *Detector) dispatchLocked(changes []Change) {
	if len(changes) == 0 {
		return
	}
	for i := range changes {
		d.nextSeq++
		changes[i].Seq = d.nextSeq
	}
	d.changes = append(d.changes, changes...)
	if d.onChange != nil {
		batch := append([]Change(nil), changes...)
		// Invoked synchronously but after all detector state has been
		// committed; callbacks must not re-enter the same Detector.
		for _, c := range batch {
			d.onChange(c)
		}
	}
}

func sortChanges(changes []Change) {
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.QueryID != b.QueryID {
			return a.QueryID < b.QueryID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return compareAlert(a.Alert, b.Alert) < 0
	})
}

func compareAlert(a, b Alert) int {
	if a.Kind != b.Kind {
		if a.Kind < b.Kind {
			return -1
		}
		return 1
	}
	if a.Source != b.Source {
		if a.Source < b.Source {
			return -1
		}
		return 1
	}
	if a.Host != b.Host {
		if a.Host < b.Host {
			return -1
		}
		return 1
	}
	if a.Port != b.Port {
		if a.Port < b.Port {
			return -1
		}
		return 1
	}
	return 0
}

type hostPortKey struct {
	source string
	host   string
}

type portTargetKey struct {
	source string
	port   uint16
}

// evaluate performs a full recomputation of one query window by scanning
// every resolved instance and counting distinct failed endpoints.
//
// The window is [At-Window, At) on the fixed start time. Open and succeeded
// instances never count: only distinct failed (source-IP, destination-host,
// destination-port) tuples are counted, so retransmissions of one attempt add
// nothing.
func evaluate(instances map[InstanceID]*instance, q Query) map[alertKey]Alert {
	low := q.At.Add(-q.Window)

	hostPorts := make(map[hostPortKey]map[uint16]struct{})
	portHosts := make(map[portTargetKey]map[string]struct{})

	for _, in := range instances {
		if in.state != stateFailed {
			continue
		}
		if !in.start.Before(q.At) || in.start.Before(low) {
			continue
		}

		hpk := hostPortKey{source: in.source.Addr, host: in.target.Addr}
		ports, ok := hostPorts[hpk]
		if !ok {
			ports = make(map[uint16]struct{})
			hostPorts[hpk] = ports
		}
		ports[in.target.Port] = struct{}{}

		ptk := portTargetKey{source: in.source.Addr, port: in.target.Port}
		hosts, ok := portHosts[ptk]
		if !ok {
			hosts = make(map[string]struct{})
			portHosts[ptk] = hosts
		}
		hosts[in.target.Addr] = struct{}{}
	}

	alerts := make(map[alertKey]Alert)
	for key, ports := range hostPorts {
		if len(ports) < q.PortThreshold {
			continue
		}
		alert := Alert{
			Kind:   AlertHostPortSweep,
			Source: key.source,
			Host:   key.host,
			Count:  len(ports),
		}
		alerts[alertKey{kind: alert.Kind, source: key.source, host: key.host}] = alert
	}
	for key, hosts := range portHosts {
		if len(hosts) < q.HostThreshold {
			continue
		}
		alert := Alert{
			Kind:   AlertPortTargetSweep,
			Source: key.source,
			Port:   key.port,
			Count:  len(hosts),
		}
		alerts[alertKey{kind: alert.Kind, source: key.source, port: key.port}] = alert
	}
	return alerts
}

// AdvanceWatermark moves the event-time watermark forward. The watermark is
// monotonic: equal or earlier values are ignored. Once it reaches a query's
// time t that query's [t-W, t) window freezes and later corrections cannot
// change or withdraw its alerts.
func (d *Detector) AdvanceWatermark(w time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !w.After(d.watermark) {
		return
	}
	d.watermark = w
	for id, qs := range d.queries {
		if !qs.frozen && !w.Before(qs.query.At) {
			qs.frozen = true
			_ = id
		}
	}
}

// Watermark returns the current watermark.
func (d *Detector) Watermark() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.watermark
}

// RegisterQuery registers a windowed query and returns its handle. Queries
// whose time t is not after the current watermark are frozen immediately with
// their current answer. Invalid queries (non-positive window, thresholds or
// time) return an error.
func (d *Detector) RegisterQuery(q Query) (QueryID, error) {
	if q.Window <= 0 || q.PortThreshold <= 0 || q.HostThreshold <= 0 || q.At.IsZero() {
		return 0, ErrInvalidQuery
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	d.nextQueryID++
	id := d.nextQueryID
	qs := &queryState{query: q, alerts: make(map[alertKey]Alert)}
	if !d.watermark.Before(q.At) {
		qs.frozen = true
	}
	qs.alerts = evaluate(d.instances, q)
	d.queries[id] = qs
	return id, nil
}

// Snapshot returns a point-in-time copy of a query's answer. The second return
// value is false for an unknown query.
func (d *Detector) Snapshot(id QueryID) (QuerySnapshot, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	qs, ok := d.queries[id]
	if !ok {
		return QuerySnapshot{}, false
	}
	return QuerySnapshot{
		ID:         id,
		Query:      qs.query,
		Frozen:     qs.frozen,
		Incomplete: d.incomplete,
		Alerts:     sortedAlerts(qs.alerts),
	}, true
}

// RemoveQuery drops a registered query and returns whether it existed.
func (d *Detector) RemoveQuery(id QueryID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.queries[id]; !ok {
		return false
	}
	delete(d.queries, id)
	return true
}

// Changes returns a copy of every alert raise/withdrawal observed so far,
// ordered by assignment sequence.
func (d *Detector) Changes() []Change {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]Change(nil), d.changes...)
}

// Incomplete reports whether the memory budget was ever exceeded. The flag is
// sticky: results may miss rejected events even after pressure subsides.
func (d *Detector) Incomplete() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.incomplete
}

// MemoryUsed reports the approximate resident state in bytes.
func (d *Detector) MemoryUsed() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.memUsed
}

func sortedAlerts(m map[alertKey]Alert) []Alert {
	if len(m) == 0 {
		return []Alert{}
	}
	out := make([]Alert, 0, len(m))
	for _, alert := range m {
		out = append(out, alert)
	}
	sort.Slice(out, func(i, j int) bool {
		return compareAlert(out[i], out[j]) < 0
	})
	return out
}

// ErrInvalidQuery is returned for queries with non-positive parameters.
var ErrInvalidQuery = errors.New("netsec: query requires positive window, thresholds and time")
