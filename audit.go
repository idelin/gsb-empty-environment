package netsec

import (
	"errors"
	"math/big"
)

// Status classifies a group in an audit report.
type Status int

const (
	// StatusZeroSample means the group currently has no records, so no
	// distribution or distance can be defined for it.
	StatusZeroSample Status = iota
	// StatusWithin means the group distance is at or below the threshold.
	StatusWithin
	// StatusExceeding means the group distance is strictly greater than the
	// threshold.
	StatusExceeding
)

// String renders the status for host-side logs.
func (s Status) String() string {
	switch s {
	case StatusZeroSample:
		return "zero-sample"
	case StatusWithin:
		return "within-threshold"
	case StatusExceeding:
		return "exceeding-threshold"
	default:
		return "unknown"
	}
}

// GroupReport is the exact audit result for one group.
type GroupReport struct {
	Group      string
	Status     Status
	GroupTotal int64
	Distance   *big.Rat // nil for zero-sample groups
	Threshold  *big.Rat
	GroupDist  []*big.Rat // exact rational group distribution
	Population []*big.Rat // exact rational population distribution at audit time
	Transport  []EdgeFlow // canonical transport evidence, nil for zero-sample
}

// Audit evaluates every known group against the population distribution.
// threshold is an exact rational bound; a group violates it only when its
// distance is strictly greater than threshold. Reports are returned in
// lexicographic group order. When no records exist at all every group (if any)
// is reported as zero-sample.
func (v *Verifier) Audit(threshold *big.Rat) ([]*GroupReport, error) {
	if threshold == nil {
		return nil, errors.New("netsec: audit threshold must not be nil")
	}
	if threshold.Sign() < 0 {
		return nil, errors.New("netsec: audit threshold must be non-negative")
	}

	snap := v.snapshot()
	population := countsToRatDist(snap.grand, snap.total)

	reports := make([]*GroupReport, 0, len(snap.names))
	for _, name := range snap.names {
		reports = append(reports, v.auditOne(snap, name, threshold, population))
	}
	return reports, nil
}

// AuditGroup audits a single group. The second result is false when the group
// is unknown to the verifier.
func (v *Verifier) AuditGroup(group string, threshold *big.Rat) (*GroupReport, bool, error) {
	if threshold == nil {
		return nil, false, errors.New("netsec: audit threshold must not be nil")
	}
	if threshold.Sign() < 0 {
		return nil, false, errors.New("netsec: audit threshold must be non-negative")
	}

	snap := v.snapshot()
	if _, ok := snap.groups[group]; !ok {
		return nil, false, nil
	}
	population := countsToRatDist(snap.grand, snap.total)
	return v.auditOne(snap, group, threshold, population), true, nil
}

func (v *Verifier) auditOne(snap *verifierSnapshot, group string, threshold *big.Rat, population []*big.Rat) *GroupReport {
	g := snap.groups[group]
	report := &GroupReport{
		Group:      group,
		GroupTotal: g.total,
		Threshold:  new(big.Rat).Set(threshold),
		Population: population,
	}
	if g.total == 0 {
		report.Status = StatusZeroSample
		report.GroupDist = countsToRatDist(g.counts, g.total)
		return report
	}

	report.GroupDist = countsToRatDist(g.counts, g.total)
	distance, err := Wasserstein1(report.GroupDist, population)
	if err != nil {
		// Unreachable: both distributions are fully materialized by the verifier.
		panic(err)
	}
	flows, err := TransportFlows(report.GroupDist, population)
	if err != nil {
		panic(err)
	}
	report.Distance = distance
	report.Transport = flows
	if distance.Cmp(threshold) > 0 {
		report.Status = StatusExceeding
	} else {
		report.Status = StatusWithin
	}
	return report
}

// Exceeding returns the reports whose status is StatusExceeding.
func Exceeding(reports []*GroupReport) []*GroupReport {
	var out []*GroupReport
	for _, report := range reports {
		if report.Status == StatusExceeding {
			out = append(out, report)
		}
	}
	return out
}
