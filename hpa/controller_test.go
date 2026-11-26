package hpa_test

import (
	"math"
	"strings"
	"testing"

	"prescreen.local/ps-6397eb1ab0/hpa"
)

func cpuSpec() hpa.MetricSpec {
	return hpa.MetricSpec{Name: "cpu", Type: hpa.ResourceMetric, TargetUtilization: 50}
}

func extSpec() hpa.MetricSpec {
	return hpa.MetricSpec{Name: "ext", Type: hpa.ExternalMetric, TargetValue: 100}
}

func readyPod(name string, startedAt int64, cpu float64) hpa.PodSnapshot {
	return hpa.PodSnapshot{
		Name:      name,
		Ready:     true,
		StartedAt: startedAt,
		Samples:   map[string]float64{"cpu": cpu},
		Requests:  map[string]float64{"cpu": 100},
	}
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// An unready pod past its readiness delay reports a huge startup CPU burst.
// The naive ratio wants to scale up, but rebalancing the unready pod to
// 100% of request flips the ratio below 1, so the controller must hold.
func TestUnreadyPodDirectionReversalHold(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas: 1,
		MaxReplicas: 20,
		Tolerance:   0.1,
		Metrics:     []hpa.MetricSpec{cpuSpec()},
	})
	pods := []hpa.PodSnapshot{}
	for _, name := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"} {
		pods = append(pods, readyPod(name, -100, 0))
	}
	pods = append(pods, hpa.PodSnapshot{
		Name:      "p11",
		Ready:     false,
		StartedAt: -100,
		Samples:   map[string]float64{"cpu": 5000},
		Requests:  map[string]float64{"cpu": 100},
	})
	d := ctl.Step(hpa.Snapshot{Time: 0, CurrentReplicas: 11, Pods: pods})
	if d.DesiredReplicas != 11 {
		t.Fatalf("expected hold at 11, got %d (reason %q)", d.DesiredReplicas, d.Reason)
	}
	md := d.Metrics[0]
	if !md.Held || !strings.Contains(md.Reason, "direction reversed") {
		t.Fatalf("expected direction-reversal hold, got %+v", md)
	}
	if !almostEqual(md.UsageRatio, 100.0/1100.0/0.5) {
		t.Fatalf("unexpected rebalanced ratio %v", md.UsageRatio)
	}
}

// Ready pods with missing samples are treated as 100% of request on a
// scale-down, which flips the ratio above 1 and prevents a false
// scale-in.
func TestMissingSampleDirectionReversalHold(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas: 1,
		MaxReplicas: 20,
		Tolerance:   0.1,
		Metrics:     []hpa.MetricSpec{cpuSpec()},
	})
	pods := []hpa.PodSnapshot{
		readyPod("p1", -100, 10),
		{Name: "p2", Ready: true, StartedAt: -100, Requests: map[string]float64{"cpu": 100}},
		{Name: "p3", Ready: true, StartedAt: -100, Requests: map[string]float64{"cpu": 100}},
		{Name: "p4", Ready: true, StartedAt: -100, Requests: map[string]float64{"cpu": 100}},
	}
	d := ctl.Step(hpa.Snapshot{Time: 0, CurrentReplicas: 4, Pods: pods})
	if d.DesiredReplicas != 4 {
		t.Fatalf("expected hold at 4, got %d (reason %q)", d.DesiredReplicas, d.Reason)
	}
	md := d.Metrics[0]
	if !md.Held || !strings.Contains(md.Reason, "direction reversed") {
		t.Fatalf("expected direction-reversal hold, got %+v", md)
	}
	if !almostEqual(md.UsageRatio, 1.55) {
		t.Fatalf("unexpected rebalanced ratio %v", md.UsageRatio)
	}
}

// Unready pods inside the initial readiness delay are ignored entirely, so
// their zero startup load never enters the mean.
func TestInitialReadinessDelayIgnoresStartingPods(t *testing.T) {
	pods := []hpa.PodSnapshot{
		readyPod("p1", -100, 60),
		readyPod("p2", -100, 60),
		{Name: "p3", Ready: false, StartedAt: -5, Requests: map[string]float64{"cpu": 100}},
		{Name: "p4", Ready: false, StartedAt: -5, Requests: map[string]float64{"cpu": 100}},
	}
	withDelay := hpa.NewController(hpa.Config{
		MinReplicas: 1, MaxReplicas: 20, Tolerance: 0.1,
		InitialReadinessDelay: 30,
		Metrics:               []hpa.MetricSpec{cpuSpec()},
	})
	d := withDelay.Step(hpa.Snapshot{Time: 0, CurrentReplicas: 4, Pods: pods})
	if !almostEqual(d.Metrics[0].UsageRatio, 1.2) {
		t.Fatalf("expected ratio 1.2 over ready pods only, got %v", d.Metrics[0].UsageRatio)
	}

	withoutDelay := hpa.NewController(hpa.Config{
		MinReplicas: 1, MaxReplicas: 20, Tolerance: 0.1,
		Metrics: []hpa.MetricSpec{cpuSpec()},
	})
	d2 := withoutDelay.Step(hpa.Snapshot{Time: 0, CurrentReplicas: 4, Pods: pods})
	if !almostEqual(d2.Metrics[0].UsageRatio, 1.6) {
		t.Fatalf("expected ratio 1.6 with unready pods rebalanced to 100%%, got %v", d2.Metrics[0].UsageRatio)
	}
}

// Ratios exactly at 1-tol and 1+tol stay in the dead zone; anything
// strictly outside scales.
func TestToleranceDeadZoneBoundary(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas: 1,
		MaxReplicas: 100,
		Tolerance:   0.1,
		Metrics:     []hpa.MetricSpec{extSpec()},
	})
	cases := []struct {
		value float64
		want  int32
	}{
		{110, 10}, // ratio exactly 1+tol: hold
		{90, 10},  // ratio exactly 1-tol: hold
		{111, 12}, // ceil(1.11*10)
		{89, 9},   // ceil(0.89*10)
	}
	for i, tc := range cases {
		d := ctl.Step(hpa.Snapshot{
			Time:            int64(i),
			CurrentReplicas: 10,
			ExternalMetrics: map[string]float64{"ext": tc.value},
		})
		if d.DesiredReplicas != tc.want {
			t.Fatalf("value %v: want %d, got %d (reason %q)", tc.value, tc.want, d.DesiredReplicas, d.Reason)
		}
	}
	if !ctl.Step(hpa.Snapshot{Time: 4, CurrentReplicas: 10, ExternalMetrics: map[string]float64{"ext": 100}}).Metrics[0].Held {
		t.Fatal("ratio 1.0 must be held by the dead zone")
	}
}

// A scale-down recommendation is latched by the maximum of the
// recommendations inside the stabilization window and only released after
// the high recommendation ages out.
func TestScaleDownStabilizationLatch(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas:                         1,
		MaxReplicas:                         20,
		Tolerance:                           0.1,
		Metrics:                             []hpa.MetricSpec{extSpec()},
		ScaleDownStabilizationWindowSeconds: 300,
	})
	step := func(t int64, value float64) hpa.Decision {
		return ctl.Step(hpa.Snapshot{
			Time:            t,
			CurrentReplicas: 10,
			ExternalMetrics: map[string]float64{"ext": value},
		})
	}
	if d := step(0, 100); d.DesiredReplicas != 10 {
		t.Fatalf("t=0: want 10, got %d", d.DesiredReplicas)
	}
	d := step(10, 40)
	if d.RawDesiredReplicas != 4 || d.StabilizedReplicas != 10 || d.DesiredReplicas != 10 {
		t.Fatalf("t=10: want raw 4 latched to 10, got %+v", d)
	}
	if d = step(200, 40); d.DesiredReplicas != 10 {
		t.Fatalf("t=200: want latched 10, got %d", d.DesiredReplicas)
	}
	if d = step(301, 40); d.DesiredReplicas != 4 {
		t.Fatalf("t=301: want released to 4, got %d (reason %q)", d.DesiredReplicas, d.Reason)
	}
}

// Scale-up stabilization takes the minimum of the window, damping a scale
// up while a lower recommendation is still inside the window.
func TestScaleUpStabilizationWindow(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas:                       1,
		MaxReplicas:                       20,
		Tolerance:                         0.1,
		Metrics:                           []hpa.MetricSpec{extSpec()},
		ScaleUpStabilizationWindowSeconds: 100,
	})
	step := func(t int64, value float64) hpa.Decision {
		return ctl.Step(hpa.Snapshot{
			Time:            t,
			CurrentReplicas: 5,
			ExternalMetrics: map[string]float64{"ext": value},
		})
	}
	if d := step(0, 50); d.RawDesiredReplicas != 3 {
		t.Fatalf("t=0: want raw 3, got %d", d.RawDesiredReplicas)
	}
	d := step(10, 200)
	if d.RawDesiredReplicas != 10 || d.StabilizedReplicas != 3 {
		t.Fatalf("t=10: want raw 10 damped to 3, got %+v", d)
	}
	if d = step(200, 200); d.DesiredReplicas != 10 {
		t.Fatalf("t=200: want 10 after window expiry, got %d", d.DesiredReplicas)
	}
}

// Percent and absolute policies are combined by the select policy, for
// both directions, and the [min,max] clamp applies after rate limiting.
func TestRatePolicyPercentAndPodsInteraction(t *testing.T) {
	base := hpa.Config{
		MinReplicas: 1,
		MaxReplicas: 100,
		Tolerance:   0.1,
		Metrics:     []hpa.MetricSpec{extSpec()},
		ScaleUpPolicies: []hpa.RatePolicy{
			{Type: hpa.PercentPolicy, Value: 50},
			{Type: hpa.PodsPolicy, Value: 2},
		},
		ScaleDownPolicies: []hpa.RatePolicy{
			{Type: hpa.PercentPolicy, Value: 50},
			{Type: hpa.PodsPolicy, Value: 1},
		},
	}
	run := func(cfg hpa.Config, value float64) hpa.Decision {
		return hpa.NewController(cfg).Step(hpa.Snapshot{
			Time:            0,
			CurrentReplicas: 10,
			ExternalMetrics: map[string]float64{"ext": value},
		})
	}

	cfg := base
	cfg.ScaleUpSelectPolicy = hpa.MaxChangePolicy
	if d := run(cfg, 1000); d.DesiredReplicas != 15 || !strings.Contains(d.Reason, "percent=50") {
		t.Fatalf("scale-up MaxChange: want 15 via percent policy, got %d (%q)", d.DesiredReplicas, d.Reason)
	}
	cfg.ScaleUpSelectPolicy = hpa.MinChangePolicy
	if d := run(cfg, 1000); d.DesiredReplicas != 12 || !strings.Contains(d.Reason, "pods=2") {
		t.Fatalf("scale-up MinChange: want 12 via pods policy, got %d (%q)", d.DesiredReplicas, d.Reason)
	}

	cfg = base
	cfg.ScaleDownSelectPolicy = hpa.MaxChangePolicy
	if d := run(cfg, 10); d.DesiredReplicas != 5 || !strings.Contains(d.Reason, "percent=50") {
		t.Fatalf("scale-down MaxChange: want 5 via percent policy, got %d (%q)", d.DesiredReplicas, d.Reason)
	}
	cfg.ScaleDownSelectPolicy = hpa.MinChangePolicy
	if d := run(cfg, 10); d.DesiredReplicas != 9 || !strings.Contains(d.Reason, "pods=1") {
		t.Fatalf("scale-down MinChange: want 9 via pods policy, got %d (%q)", d.DesiredReplicas, d.Reason)
	}

	cfg = base
	cfg.MaxReplicas = 12
	if d := run(cfg, 1000); d.DesiredReplicas != 12 || !strings.Contains(d.Reason, "clamped to maxReplicas 12") {
		t.Fatalf("clamp: want 12, got %d (%q)", d.DesiredReplicas, d.Reason)
	}
	cfg = base
	cfg.MinReplicas = 8
	cfg.ScaleDownSelectPolicy = hpa.MaxChangePolicy
	if d := run(cfg, 1); d.DesiredReplicas != 8 || !strings.Contains(d.Reason, "clamped to minReplicas 8") {
		t.Fatalf("clamp: want 8, got %d (%q)", d.DesiredReplicas, d.Reason)
	}
}

// With several metrics the largest desired replica count wins and the
// dominant metric is reported.
func TestMultiMetricMaxAndDominant(t *testing.T) {
	ctl := hpa.NewController(hpa.Config{
		MinReplicas: 1,
		MaxReplicas: 50,
		Tolerance:   0.1,
		Metrics:     []hpa.MetricSpec{cpuSpec(), extSpec()},
	})
	pods := []hpa.PodSnapshot{
		readyPod("p1", -100, 30),
		readyPod("p2", -100, 30),
		readyPod("p3", -100, 30),
		readyPod("p4", -100, 30),
	}
	d := ctl.Step(hpa.Snapshot{
		Time:            0,
		CurrentReplicas: 4,
		Pods:            pods,
		ExternalMetrics: map[string]float64{"ext": 700},
	})
	if d.Metrics[0].DesiredReplicas != 3 {
		t.Fatalf("cpu metric: want 3, got %d", d.Metrics[0].DesiredReplicas)
	}
	if d.DesiredReplicas != 28 || d.DominantMetric != "ext" {
		t.Fatalf("want 28 via ext, got %d via %q", d.DesiredReplicas, d.DominantMetric)
	}

	d = ctl.Step(hpa.Snapshot{
		Time:            1,
		CurrentReplicas: 4,
		Pods:            pods,
		ExternalMetrics: map[string]float64{"ext": 100},
	})
	if d.DesiredReplicas != 4 {
		t.Fatalf("want hold at 4, got %d", d.DesiredReplicas)
	}
}
