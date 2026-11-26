package hpa

import "math"

// toleranceEpsilon absorbs float64 representation error so that a ratio
// landing exactly on a dead-zone boundary (e.g. 1.1 with tolerance 0.1)
// is treated as inside the zone.
const toleranceEpsilon = 1e-9

type podMetric struct {
	value   float64
	request float64
}

func (c *Controller) inDeadZone(ratio float64) bool {
	return math.Abs(1-ratio) <= c.cfg.Tolerance+toleranceEpsilon
}

func (c *Controller) computeMetric(spec MetricSpec, snap Snapshot) MetricDecision {
	if spec.Type == ExternalMetric {
		return c.computeExternalMetric(spec, snap)
	}
	return c.computePerPodMetric(spec, snap)
}

// computePerPodMetric handles ResourceMetric and PodsMetric following the
// Kubernetes replica calculator semantics: pods inside the CPU
// initialization period or the initial readiness delay are ignored
// entirely; on a scale-up (ratio > 1) unready and missing pods are
// rebalanced to 100% of their request/target to avoid over-scaling, while
// on a scale-down unready pods count as 0 and missing pods as 100%; if
// rebalancing flips the scaling direction across 1.0 the current replica
// count is kept.
func (c *Controller) computePerPodMetric(spec MetricSpec, snap Snapshot) MetricDecision {
	md := MetricDecision{
		Name:            spec.Name,
		Type:            spec.Type,
		DesiredReplicas: snap.CurrentReplicas,
		Held:            true,
	}
	isResource := spec.Type == ResourceMetric
	target := spec.TargetAverageValue
	if isResource {
		target = spec.TargetUtilization / 100
	}
	if target <= 0 {
		md.Reason = "invalid metric target"
		return md
	}

	metrics := map[string]podMetric{}
	unready := map[string]podMetric{}
	missing := map[string]podMetric{}
	for _, p := range snap.Pods {
		age := snap.Time - p.StartedAt
		if age < c.cfg.CPUInitializationPeriod {
			continue
		}
		if !p.Ready && age < c.cfg.InitialReadinessDelay {
			continue
		}
		request := p.Requests[spec.Name]
		if request <= 0 {
			request = 1
		}
		value, ok := p.Samples[spec.Name]
		switch {
		case p.Ready && ok:
			metrics[p.Name] = podMetric{value: value, request: request}
		case p.Ready:
			missing[p.Name] = podMetric{request: request}
		case ok:
			metrics[p.Name] = podMetric{value: value, request: request}
			unready[p.Name] = podMetric{value: value, request: request}
		default:
			unready[p.Name] = podMetric{request: request}
		}
	}
	if len(metrics) == 0 {
		md.Reason = "no pod metrics available"
		return md
	}

	ratioOf := func(set map[string]podMetric) float64 {
		if isResource {
			var values, requests float64
			for _, m := range set {
				values += m.value
				requests += m.request
			}
			if requests <= 0 {
				return 0
			}
			return (values / requests) / target
		}
		var values float64
		for _, m := range set {
			values += m.value
		}
		return (values / float64(len(set))) / target
	}

	usageRatio := ratioOf(metrics)
	rebalance := len(unready) > 0 && usageRatio > 1
	for name, m := range unready {
		switch {
		case rebalance && isResource:
			m.value = m.request
		case rebalance:
			m.value = target
		default:
			m.value = 0
		}
		metrics[name] = m
	}
	for name, m := range missing {
		if isResource {
			m.value = m.request
		} else {
			m.value = target
		}
		metrics[name] = m
	}

	newRatio := ratioOf(metrics)
	md.UsageRatio = newRatio
	if c.inDeadZone(newRatio) {
		md.Reason = "within tolerance dead zone"
		return md
	}
	if (usageRatio < 1 && newRatio > 1) || (usageRatio > 1 && newRatio < 1) {
		md.Reason = "direction reversed after rebalancing unready/missing pods"
		return md
	}
	md.Held = false
	md.DesiredReplicas = int32(math.Ceil(newRatio * float64(len(metrics))))
	md.Reason = "metric drives scaling"
	return md
}

func (c *Controller) computeExternalMetric(spec MetricSpec, snap Snapshot) MetricDecision {
	md := MetricDecision{
		Name:            spec.Name,
		Type:            spec.Type,
		DesiredReplicas: snap.CurrentReplicas,
		Held:            true,
	}
	value, ok := snap.ExternalMetrics[spec.Name]
	if !ok {
		md.Reason = "external metric value missing"
		return md
	}
	if spec.TargetValue <= 0 {
		md.Reason = "invalid metric target"
		return md
	}
	ratio := value / spec.TargetValue
	md.UsageRatio = ratio
	if c.inDeadZone(ratio) {
		md.Reason = "within tolerance dead zone"
		return md
	}
	md.Held = false
	md.DesiredReplicas = int32(math.Ceil(ratio * float64(snap.CurrentReplicas)))
	md.Reason = "metric drives scaling"
	return md
}
