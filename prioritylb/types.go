package prioritylb

import (
	"errors"
	"time"
)

// Health is the health state of a single endpoint.
type Health int

const (
	// Healthy endpoints fill traffic first within each priority.
	Healthy Health = iota
	// Degraded endpoints only absorb traffic after healthy capacity is
	// exhausted (both inside their own priority and across priorities).
	Degraded
	// Unhealthy endpoints never receive traffic outside panic mode.
	Unhealthy
)

// Endpoint is a single upstream member of a priority group.
type Endpoint struct {
	// ID is the caller-owned endpoint identifier, echoed back in Result.
	ID string
	// Weight is the raw endpoint weight used for intra-group traffic
	// splitting. Must be non-negative and finite; a weight of 0 receives
	// no traffic.
	Weight float64
	// State is the current health state.
	State Health
	// OnlineAt is the moment the endpoint started serving.
	OnlineAt time.Time
}

// PriorityGroup holds every endpoint belonging to one priority level.
// Groups must be supplied in priority order P0, P1, ..., Pn.
type PriorityGroup struct {
	Endpoints []Endpoint
}

// Config carries the global load-balancing parameters.
type Config struct {
	// Now is the reference time used for slow-start age computation.
	Now time.Time
	// OverprovisioningFactor scales raw availability, e.g. 1.4.
	// A value <= 0 defaults to 1.0.
	OverprovisioningFactor float64
	// PanicThreshold is the global panic threshold in [0, 1], e.g. 0.5.
	// Panic mode triggers when the healthy fraction of every non-empty
	// priority is strictly below this value.
	PanicThreshold float64
	// SlowStartWindow is the slow-start duration W. A zero or negative
	// value disables slow start.
	SlowStartWindow time.Duration
	// AggressionExponent is the exponent A in (age/W)^(1/A). Must be > 0.
	AggressionExponent float64
	// MinWeightFloor is the minimum slow-start multiplier m in [0, 1].
	MinWeightFloor float64
}

// EndpointProbability is the final normalized probability of one endpoint.
type EndpointProbability struct {
	// Priority is the index of the group the endpoint belongs to.
	Priority int
	// EndpointIndex is the endpoint position within PriorityGroup.Endpoints.
	EndpointIndex int
	// EndpointID echoes Endpoint.ID.
	EndpointID string
	// EffectiveWeight is the weight after slow-start scaling.
	EffectiveWeight float64
	// Probability is the final global probability; the sum over every
	// endpoint equals 1.0.
	Probability float64
}

// PriorityShare is the traffic share assigned to one priority.
type PriorityShare struct {
	// Priority is the group index.
	Priority int
	// Share is the fraction of total traffic, in [0, 1].
	Share float64
	// HealthyAvailability and DegradedAvailability are the capped,
	// overprovisioned availability values in [0, 1].
	HealthyAvailability  float64
	DegradedAvailability float64
}

// Result is the full output of Calculate.
type Result struct {
	// Panic reports whether global panic mode was triggered.
	Panic bool
	// PriorityShares contains one entry per input group, in order. The
	// shares sum to exactly 1.0.
	PriorityShares []PriorityShare
	// Endpoints contains one entry per input endpoint (group-major order).
	// The probabilities sum to exactly 1.0.
	Endpoints []EndpointProbability
}

// ErrNoEndpoints is returned when no traffic can be assigned because the
// input contains no routable endpoint (empty input, all unhealthy, or every
// eligible endpoint has zero effective weight).
var ErrNoEndpoints = errors.New("prioritylb: no routable endpoints")
