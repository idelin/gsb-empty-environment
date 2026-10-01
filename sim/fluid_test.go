package sim

import (
	"testing"
)

// The hierarchical packet scheduler is compared against an independent
// hierarchical fluid (GPS) reference on a small scenario with two tenants,
// unequal weights, different packet sizes and a mid-run weight change.
// Deviation is reported in BYTES of service, not packet counts.
func TestFluidDeviationSmall(t *testing.T) {
	const until = 300
	e := NewEngine(1)
	e.AddTenant(1, 2)
	e.AddTenant(2, 1)
	e.AddFlow(1, 1, 1, 0) // 7-byte packets
	e.AddFlow(2, 1, 3, 0) // 3-byte packets
	e.AddFlow(3, 2, 1, 0) // 5-byte packets
	burst(e, 0, 1, 1, 20, 7)
	burst(e, 0, 2, 101, 20, 3)
	burst(e, 0, 3, 201, 20, 5)
	e.Submit(Event{Tick: 100, Type: EvFlowWeight, FlowID: 2, Weight: 1})
	e.Run(until)

	mkArrivals := func(n, size int) []FluidArrival {
		return []FluidArrival{{Tick: 0, Bytes: n * size}}
	}
	fluid := FluidServe([]FluidFlow{
		{FlowID: 1, TenantID: 1, Weight: 1, TenantWeight: 2, Arrivals: mkArrivals(20, 7)},
		{FlowID: 2, TenantID: 1, Weight: 3, TenantWeight: 2, Arrivals: mkArrivals(20, 3),
			WeightEvents: []FluidWeightEvent{{Tick: 100, Weight: 1}}},
		{FlowID: 3, TenantID: 2, Weight: 1, TenantWeight: 1, Arrivals: mkArrivals(20, 5)},
	}, 1, until)

	rep := ByteDeviation(e.Trace, fluid, until)
	for id, d := range rep.PerFlowMax {
		t.Logf("flow %d: max byte deviation %.3f", id, d)
	}
	t.Logf("overall max byte deviation %.3f bytes (flow %d @ tick %d)",
		rep.MaxBytes, rep.WorstFlow, rep.WorstTick)

	// Two-level WFQ vs GPS: per-flow byte lag is bounded by a small
	// multiple of the largest packet (7 bytes here).
	const maxPkt = 7.0
	if rep.MaxBytes > 3*maxPkt {
		t.Fatalf("byte deviation %.3f exceeds bound %.1f", rep.MaxBytes, 3*maxPkt)
	}
}

// Fluid reference sanity: two equal-weight flows split capacity evenly in
// bytes; a 2:1 weight split gives a 2:1 byte ratio.
func TestFluidReferenceSanity(t *testing.T) {
	svc := FluidServe([]FluidFlow{
		{FlowID: 1, TenantID: 1, Weight: 1, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 100}}},
		{FlowID: 2, TenantID: 1, Weight: 1, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 100}}},
	}, 1, 200)
	close := func(a, b float64) bool { return a > b-1e-9 && a < b+1e-9 }
	if !close(svc[1][200], 100) || !close(svc[2][200], 100) {
		t.Fatalf("equal-weight fluid split wrong: %v %v", svc[1][200], svc[2][200])
	}
	if !close(svc[1][50], 25) {
		t.Fatalf("fluid rate wrong: got %v want 25", svc[1][50])
	}

	svc = FluidServe([]FluidFlow{
		{FlowID: 1, TenantID: 1, Weight: 2, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 120}}},
		{FlowID: 2, TenantID: 1, Weight: 1, TenantWeight: 1, Arrivals: []FluidArrival{{Tick: 0, Bytes: 120}}},
	}, 1, 180)
	if !close(svc[1][90], 60) || !close(svc[2][90], 30) {
		t.Fatalf("weighted fluid split wrong: %v %v", svc[1][90], svc[2][90])
	}
}
