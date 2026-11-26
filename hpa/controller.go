package hpa

import (
	"fmt"
	"math"
	"strings"
)

// Controller is a stateful HPA decision engine. Create one with
// NewController and feed it one Snapshot per sync period via Step.
type Controller struct {
	cfg             Config
	recommendations []recommendation
}

type recommendation struct {
	time     int64
	replicas int32
}

// NewController builds a Controller, applying defaults for zero-valued
// config fields.
func NewController(cfg Config) *Controller {
	if cfg.SyncPeriodSeconds <= 0 {
		cfg.SyncPeriodSeconds = 1
	}
	if cfg.MaxReplicas < cfg.MinReplicas {
		cfg.MaxReplicas = cfg.MinReplicas
	}
	return &Controller{cfg: cfg}
}

// Step advances the controller by one sync period and returns the scaling
// decision for the given snapshot.
func (c *Controller) Step(snap Snapshot) Decision {
	d := Decision{Time: snap.Time, CurrentReplicas: snap.CurrentReplicas}

	raw := snap.CurrentReplicas
	dominant := ""
	for i, spec := range c.cfg.Metrics {
		md := c.computeMetric(spec, snap)
		d.Metrics = append(d.Metrics, md)
		if i == 0 || md.DesiredReplicas > raw {
			raw = md.DesiredReplicas
			dominant = spec.Name
		}
	}
	d.RawDesiredReplicas = raw
	d.DominantMetric = dominant

	stabilized, stabNote := c.stabilize(snap.Time, raw, snap.CurrentReplicas)
	d.StabilizedReplicas = stabilized

	var reasons []string
	if stabNote != "" {
		reasons = append(reasons, stabNote)
	}

	target := stabilized
	current := snap.CurrentReplicas
	if stabilized > current && len(c.cfg.ScaleUpPolicies) > 0 {
		delta, pol := selectDelta(c.cfg.ScaleUpPolicies, c.cfg.ScaleUpSelectPolicy, current, c.cfg.SyncPeriodSeconds, true)
		if limit := current + delta; target > limit {
			target = limit
			reasons = append(reasons, "scale-up rate-limited by "+describePolicy(pol, delta))
		}
	}
	if stabilized < current && len(c.cfg.ScaleDownPolicies) > 0 {
		delta, pol := selectDelta(c.cfg.ScaleDownPolicies, c.cfg.ScaleDownSelectPolicy, current, c.cfg.SyncPeriodSeconds, false)
		if limit := current - delta; target < limit {
			target = limit
			reasons = append(reasons, "scale-down rate-limited by "+describePolicy(pol, delta))
		}
	}
	if target < c.cfg.MinReplicas {
		target = c.cfg.MinReplicas
		reasons = append(reasons, fmt.Sprintf("clamped to minReplicas %d", c.cfg.MinReplicas))
	}
	if target > c.cfg.MaxReplicas {
		target = c.cfg.MaxReplicas
		reasons = append(reasons, fmt.Sprintf("clamped to maxReplicas %d", c.cfg.MaxReplicas))
	}

	d.DesiredReplicas = target
	d.RateLimited = len(reasons) > 0
	if len(reasons) == 0 {
		d.Reason = "within all limits"
	} else {
		d.Reason = strings.Join(reasons, "; ")
	}
	return d
}

// stabilize records the raw recommendation and filters it through the
// sliding stabilization windows: scaling down takes the maximum of the
// recommendations inside the scale-down window, scaling up takes the
// minimum inside the scale-up window.
func (c *Controller) stabilize(now int64, raw, current int32) (int32, string) {
	c.recommendations = append(c.recommendations, recommendation{time: now, replicas: raw})
	maxWindow := c.cfg.ScaleUpStabilizationWindowSeconds
	if c.cfg.ScaleDownStabilizationWindowSeconds > maxWindow {
		maxWindow = c.cfg.ScaleDownStabilizationWindowSeconds
	}
	kept := c.recommendations[:0]
	for _, r := range c.recommendations {
		if r.time >= now-maxWindow {
			kept = append(kept, r)
		}
	}
	c.recommendations = kept

	up, down := raw, raw
	for _, r := range c.recommendations {
		if r.time > now-c.cfg.ScaleUpStabilizationWindowSeconds && r.replicas < up {
			up = r.replicas
		}
		if r.time > now-c.cfg.ScaleDownStabilizationWindowSeconds && r.replicas > down {
			down = r.replicas
		}
	}
	if raw <= current {
		if down != raw {
			return down, fmt.Sprintf("scale-down stabilization window held %d (raw %d)", down, raw)
		}
		return down, ""
	}
	if up != raw {
		return up, fmt.Sprintf("scale-up stabilization window held %d (raw %d)", up, raw)
	}
	return up, ""
}

// selectDelta computes the allowed absolute replica change from the given
// policies, combining them with the select policy. Percent policies are
// rounded up for scale-up and down for scale-down so a nonzero percentage
// always permits upward progress.
func selectDelta(policies []RatePolicy, sel SelectPolicy, current int32, syncPeriod int64, scaleUp bool) (int32, RatePolicy) {
	var best int32
	var chosen RatePolicy
	for i, p := range policies {
		period := p.PeriodSeconds
		if period <= 0 {
			period = syncPeriod
		}
		scale := float64(syncPeriod) / float64(period)
		var delta int32
		if p.Type == PercentPolicy {
			f := float64(current) * float64(p.Value) / 100 * scale
			if scaleUp {
				delta = int32(math.Ceil(f))
			} else {
				delta = int32(math.Floor(f))
			}
		} else {
			delta = int32(math.Round(float64(p.Value) * scale))
		}
		if i == 0 || (sel == MinChangePolicy && delta < best) || (sel == MaxChangePolicy && delta > best) {
			best = delta
			chosen = p
		}
	}
	return best, chosen
}

func describePolicy(p RatePolicy, delta int32) string {
	if p.Type == PercentPolicy {
		return fmt.Sprintf("percent=%d policy (max change %d)", p.Value, delta)
	}
	return fmt.Sprintf("pods=%d policy (max change %d)", p.Value, delta)
}
