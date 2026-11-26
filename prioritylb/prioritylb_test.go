package prioritylb

import (
	"math"
	"testing"
	"time"
)

const tolerance = 1e-9

var t0 = time.Unix(1_700_000_000, 0)

func baseConfig() Config {
	return Config{
		Now:                    t0,
		OverprovisioningFactor: 1.4,
		PanicThreshold:         0.5,
		SlowStartWindow:        0,
		AggressionExponent:     1,
		MinWeightFloor:         0,
	}
}

// group builds equal-weight endpoints with the given counts per state, in
// healthy -> degraded -> unhealthy order. All endpoints are aged beyond any
// slow-start window.
func group(healthy, degraded, unhealthy int) PriorityGroup {
	endpoints := make([]Endpoint, 0, healthy+degraded+unhealthy)
	add := func(n int, state Health) {
		for i := 0; i < n; i++ {
			endpoints = append(endpoints, Endpoint{
				Weight:   1,
				State:    state,
				OnlineAt: t0.Add(-time.Hour),
			})
		}
	}
	add(healthy, Healthy)
	add(degraded, Degraded)
	add(unhealthy, Unhealthy)
	return PriorityGroup{Endpoints: endpoints}
}

func approx(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %.12f, want %.12f", label, got, want)
	}
}

func sharesOf(r *Result) []float64 {
	out := make([]float64, len(r.PriorityShares))
	for i, s := range r.PriorityShares {
		out[i] = s.Share
	}
	return out
}

func endpointProbs(r *Result) []float64 {
	out := make([]float64, len(r.Endpoints))
	for i, ep := range r.Endpoints {
		out[i] = ep.Probability
	}
	return out
}

func assertSumsToOne(t *testing.T, values []float64, label string) {
	t.Helper()
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	if math.Abs(sum-1) > tolerance {
		t.Fatalf("%s sum = %.12f, want 1.0", label, sum)
	}
}

// TestEnvoyOverflowToP1 reproduces the first Envoy priority load example:
// P0 at 70% health with overprovisioning 1.4 keeps 98% and spills 2% to P1.
func TestEnvoyOverflowToP1(t *testing.T) {
	groups := []PriorityGroup{
		group(7, 0, 3),
		group(10, 0, 0),
	}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	if r.Panic {
		t.Fatal("panic mode must not trigger above the threshold")
	}

	shares := sharesOf(r)
	approx(t, r.PriorityShares[0].HealthyAvailability, 0.98, "P0 healthy availability")
	approx(t, shares[0], 0.98, "P0 share")
	approx(t, shares[1], 0.02, "P1 share")
	assertSumsToOne(t, shares, "priority shares")

	probs := endpointProbs(r)
	for i := 0; i < 7; i++ {
		approx(t, probs[i], 0.98/7, "P0 healthy endpoint probability")
	}
	for i := 7; i < 10; i++ {
		approx(t, probs[i], 0, "P0 unhealthy endpoint probability")
	}
	for i := 10; i < 20; i++ {
		approx(t, probs[i], 0.02/10, "P1 endpoint probability")
	}
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestEnvoyDegradedSecondLevel reproduces the Envoy example where P0 has
// 60% healthy and 20% degraded: healthy capacity alone covers 84%, degraded
// capacity covers the remaining 16%, so P1 receives nothing.
func TestEnvoyDegradedSecondLevel(t *testing.T) {
	groups := []PriorityGroup{
		group(6, 2, 2),
		group(10, 0, 0),
	}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	shares := sharesOf(r)
	approx(t, r.PriorityShares[0].HealthyAvailability, 0.84, "P0 healthy availability")
	approx(t, r.PriorityShares[0].DegradedAvailability, 0.28, "P0 degraded availability")
	approx(t, shares[0], 1.0, "P0 share")
	approx(t, shares[1], 0.0, "P1 share")
	assertSumsToOne(t, shares, "priority shares")

	probs := endpointProbs(r)
	for i := 0; i < 8; i++ {
		approx(t, probs[i], 1.0/8, "eligible P0 endpoint probability")
	}
	for i := 8; i < 10; i++ {
		approx(t, probs[i], 0, "unhealthy P0 endpoint probability")
	}
	for i := 10; i < 20; i++ {
		approx(t, probs[i], 0, "P1 endpoint probability")
	}
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestEnvoyDegradedOverflowAcrossPriorities reproduces the Envoy example in
// which two priorities each expose 20% healthy + 20% degraded capacity.
// Healthy capacity absorbs 56% of the load, degraded capacity the rest, and
// the equal priorities finish at a 50/50 split.
func TestEnvoyDegradedOverflowAcrossPriorities(t *testing.T) {
	groups := []PriorityGroup{
		group(2, 2, 6), // 10 endpoints: 20% healthy, 20% degraded
		group(2, 2, 6),
	}
	cfg := baseConfig()
	// Keep panic mode out of this test so the degraded two-pass semantics
	// can be observed in isolation.
	cfg.PanicThreshold = 0

	r, err := Calculate(groups, cfg)
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	shares := sharesOf(r)
	approx(t, shares[0], 0.56, "P0 share")
	approx(t, shares[1], 0.44, "P1 share")
	assertSumsToOne(t, shares, "priority shares")

	probs := endpointProbs(r)
	for g := 0; g < 2; g++ {
		base := g * 10
		for i := 0; i < 4; i++ {
			want := map[int]float64{0: 0.56 / 4, 1: 0.44 / 4}[g]
			approx(t, probs[base+i], want, "eligible endpoint probability")
		}
		for i := 4; i < 10; i++ {
			approx(t, probs[base+i], 0, "unhealthy endpoint probability")
		}
	}
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestEnvoyProportionalScaleUp covers a fleet whose total effective capacity
// stays below 100% without crossing the panic threshold: each priority has
// 60% healthy endpoints (raw fraction above 50%), so panic does not trigger,
// while a reduced overprovisioning factor leaves combined availability below
// 100% and forces proportional scale-up.
func TestEnvoyProportionalScaleUp(t *testing.T) {
	groups := []PriorityGroup{
		group(6, 0, 4), // 60% raw healthy
		group(6, 0, 4),
	}
	cfg := baseConfig()
	cfg.OverprovisioningFactor = 0.7 // per-group availability 0.6*0.7 = 0.42

	r, err := Calculate(groups, cfg)
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	if r.Panic {
		t.Fatal("60% raw health with 50% threshold must not panic")
	}

	shares := sharesOf(r)
	approx(t, r.PriorityShares[0].HealthyAvailability, 0.42, "P0 availability")
	approx(t, shares[0], 0.5, "P0 share")
	approx(t, shares[1], 0.5, "P1 share")
	assertSumsToOne(t, shares, "priority shares")

	probs := endpointProbs(r)
	for g := 0; g < 2; g++ {
		base := g * 10
		for i := 0; i < 6; i++ {
			approx(t, probs[base+i], 0.5/6, "healthy endpoint probability")
		}
		for i := 6; i < 10; i++ {
			approx(t, probs[base+i], 0, "unhealthy endpoint probability")
		}
	}
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestGlobalPanicMode covers a fleet-wide sub-threshold deployment: health
// filtering is dropped and traffic follows raw endpoint counts, including
// unhealthy endpoints.
func TestGlobalPanicMode(t *testing.T) {
	groups := []PriorityGroup{
		group(1, 1, 2), // 4 endpoints, 25% healthy
		group(2, 0, 4), // 6 endpoints, 33% healthy
	}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	if !r.Panic {
		t.Fatal("expected global panic mode")
	}

	shares := sharesOf(r)
	approx(t, shares[0], 0.4, "P0 panic share")
	approx(t, shares[1], 0.6, "P1 panic share")
	assertSumsToOne(t, shares, "priority shares")

	probs := endpointProbs(r)
	for i := 0; i < 4; i++ {
		approx(t, probs[i], 0.4/4, "P0 endpoint probability")
	}
	for i := 4; i < 10; i++ {
		approx(t, probs[i], 0.6/6, "P1 endpoint probability")
	}
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestPanicThresholdBoundary pins the strict-less-than semantics: a priority
// exactly at the threshold keeps the fleet out of panic mode.
func TestPanicThresholdBoundary(t *testing.T) {
	groups := []PriorityGroup{
		group(2, 0, 2), // 50% healthy, equal to threshold
		group(1, 0, 3), // 25% healthy
	}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	if r.Panic {
		t.Fatal("a priority exactly at the threshold must prevent panic")
	}

	shares := sharesOf(r)
	// P0 healthy availability caps at min(0.5*1.4, 1) = 0.7, leaving 0.3.
	approx(t, shares[0], 0.7, "P0 share")
	approx(t, shares[1], 0.3, "P1 share")
	assertSumsToOne(t, shares, "priority shares")
}

// TestSlowStartFloorProtect checks that a cold endpoint never drops below the
// floor m, while a ramped endpoint above the floor follows the power curve.
func TestSlowStartFloorProtect(t *testing.T) {
	cfg := baseConfig()
	cfg.SlowStartWindow = 100 * time.Second
	cfg.AggressionExponent = 2
	cfg.MinWeightFloor = 0.1

	window := cfg.SlowStartWindow
	groups := []PriorityGroup{{Endpoints: []Endpoint{
		{ID: "cold", Weight: 100, State: Healthy, OnlineAt: t0},
		{ID: "ramped", Weight: 100, State: Healthy, OnlineAt: t0.Add(-25 * time.Second)},
		{ID: "warmed", Weight: 100, State: Healthy, OnlineAt: t0.Add(-window)},
	}}}

	r, err := Calculate(groups, cfg)
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	// age=0 -> ramp 0 -> floor 0.1.
	approx(t, r.Endpoints[0].EffectiveWeight, 100*0.1, "cold effective weight")
	// age/W=0.25, exponent 1/2 -> 0.5 which is above the floor.
	approx(t, r.Endpoints[1].EffectiveWeight, 100*0.5, "ramped effective weight")
	// age == W is outside the window -> full weight.
	approx(t, r.Endpoints[2].EffectiveWeight, 100, "warmed effective weight")

	probs := endpointProbs(r)
	approx(t, probs[0], 0.1/1.6, "cold probability")
	approx(t, probs[1], 0.5/1.6, "ramped probability")
	approx(t, probs[2], 1.0/1.6, "warmed probability")
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestSlowStartFloorExceedsCurve verifies the floor wins when the curve dips
// below m for a very young endpoint.
func TestSlowStartFloorExceedsCurve(t *testing.T) {
	cfg := baseConfig()
	cfg.SlowStartWindow = 100 * time.Second
	cfg.AggressionExponent = 1
	cfg.MinWeightFloor = 0.2

	groups := []PriorityGroup{{Endpoints: []Endpoint{
		{ID: "cold", Weight: 10, State: Healthy, OnlineAt: t0.Add(-time.Second)},
		{ID: "old", Weight: 10, State: Healthy, OnlineAt: t0.Add(-10 * time.Second)},
	}}}

	r, err := Calculate(groups, cfg)
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	// First endpoint curve is 0.01 but floor 0.2 applies. The second
	// endpoint curve is 0.1, also below the floor, so it uses 0.2 as well.
	probs := endpointProbs(r)
	approx(t, probs[0], 0.5, "floored endpoint probability")
	approx(t, probs[1], 0.5, "floored ramped endpoint probability")
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestRawWeightsSplitTraffic ensures original weights drive intra-group
// splitting once slow start is disabled, and zero-weight endpoints get nothing.
func TestRawWeightsSplitTraffic(t *testing.T) {
	groups := []PriorityGroup{{Endpoints: []Endpoint{
		{ID: "a", Weight: 1, State: Healthy, OnlineAt: t0.Add(-time.Hour)},
		{ID: "b", Weight: 3, State: Healthy, OnlineAt: t0.Add(-time.Hour)},
		{ID: "zero", Weight: 0, State: Healthy, OnlineAt: t0.Add(-time.Hour)},
	}}}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	probs := endpointProbs(r)
	approx(t, probs[0], 0.25, "weight-1 endpoint probability")
	approx(t, probs[1], 0.75, "weight-3 endpoint probability")
	approx(t, probs[2], 0, "zero-weight endpoint probability")
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestPanicModeAppliesSlowStart confirms slow start still protects cold
// endpoints when the fleet enters panic mode, while unhealthy endpoints are
// now eligible for traffic.
func TestPanicModeAppliesSlowStart(t *testing.T) {
	cfg := baseConfig()
	cfg.SlowStartWindow = 100 * time.Second
	cfg.AggressionExponent = 1
	cfg.MinWeightFloor = 0.25

	groups := []PriorityGroup{{Endpoints: []Endpoint{
		{ID: "cold", Weight: 10, State: Unhealthy, OnlineAt: t0},
		{ID: "warm", Weight: 10, State: Unhealthy, OnlineAt: t0.Add(-50 * time.Second)},
	}}}

	r, err := Calculate(groups, cfg)
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}
	if !r.Panic {
		t.Fatal("expected panic mode for a fully unhealthy group")
	}

	probs := endpointProbs(r)
	// Floor 0.25 for the cold endpoint, 0.5 curve value for the warm one.
	approx(t, probs[0], 0.25/0.75, "cold endpoint probability under panic")
	approx(t, probs[1], 0.5/0.75, "warm endpoint probability under panic")
	assertSumsToOne(t, probs, "endpoint probabilities")
}

// TestEmptyGroupsAreSkipped ensures empty priorities do not break assignment
// and receive a zero share.
func TestEmptyGroupsAreSkipped(t *testing.T) {
	groups := []PriorityGroup{
		group(0, 0, 0),
		group(10, 0, 0),
		group(0, 0, 0),
	}

	r, err := Calculate(groups, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	shares := sharesOf(r)
	approx(t, shares[0], 0, "empty P0 share")
	approx(t, shares[1], 1, "P1 share")
	approx(t, shares[2], 0, "empty P2 share")
	assertSumsToOne(t, shares, "priority shares")
}

// TestExactNormalization verifies the residual correction makes the outputs
// sum to exactly 1.0 rather than merely approximately 1.0.
func TestExactNormalization(t *testing.T) {
	endpoints := make([]Endpoint, 7)
	for i := range endpoints {
		endpoints[i] = Endpoint{
			ID:       "ep",
			Weight:   3,
			State:    Healthy,
			OnlineAt: t0.Add(-time.Hour),
		}
	}

	r, err := Calculate([]PriorityGroup{{Endpoints: endpoints}}, baseConfig())
	if err != nil {
		t.Fatalf("Calculate returned error: %v", err)
	}

	shareSum := 0.0
	for _, s := range r.PriorityShares {
		shareSum += s.Share
	}
	if shareSum != 1.0 {
		t.Fatalf("priority share sum = %.17g, want exactly 1.0", shareSum)
	}

	probSum := 0.0
	for _, ep := range r.Endpoints {
		probSum += ep.Probability
	}
	if probSum != 1.0 {
		t.Fatalf("endpoint probability sum = %.17g, want exactly 1.0", probSum)
	}
}

func TestErrors(t *testing.T) {
	goodGroup := group(1, 0, 0)

	if _, err := Calculate(nil, baseConfig()); err != ErrNoEndpoints {
		t.Fatalf("empty input: got %v, want ErrNoEndpoints", err)
	}
	// A non-panicking group whose only eligible endpoint has zero weight
	// leaves nothing routable.
	zeroWeightGroup := []PriorityGroup{{Endpoints: []Endpoint{
		{Weight: 0, State: Healthy, OnlineAt: t0},
		{Weight: 1, State: Unhealthy, OnlineAt: t0},
	}}}
	if _, err := Calculate(zeroWeightGroup, baseConfig()); err != ErrNoEndpoints {
		t.Fatalf("zero eligible weight: got %v, want ErrNoEndpoints", err)
	}

	cfg := baseConfig()
	cfg.PanicThreshold = 1.5
	if _, err := Calculate([]PriorityGroup{goodGroup}, cfg); err == nil {
		t.Fatal("expected error for panic threshold outside [0,1]")
	}

	cfg = baseConfig()
	cfg.AggressionExponent = 0
	if _, err := Calculate([]PriorityGroup{goodGroup}, cfg); err == nil {
		t.Fatal("expected error for non-positive aggression exponent")
	}

	cfg = baseConfig()
	cfg.MinWeightFloor = -0.1
	if _, err := Calculate([]PriorityGroup{goodGroup}, cfg); err == nil {
		t.Fatal("expected error for negative weight floor")
	}

	badWeight := []PriorityGroup{{Endpoints: []Endpoint{
		{Weight: -1, State: Healthy, OnlineAt: t0},
	}}}
	if _, err := Calculate(badWeight, baseConfig()); err == nil {
		t.Fatal("expected error for negative endpoint weight")
	}
}
