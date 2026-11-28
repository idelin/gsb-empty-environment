package promqlengine

import (
	"errors"
	"fmt"
)

// ValueType mirrors the PromQL type classes used for argument validation.
type ValueType int

const (
	ValueTypeNone ValueType = iota
	ValueTypeVector
	ValueTypeMatrix
	ValueTypeScalar
)

// Function describes a registered matrix function.
type Function struct {
	Name      string
	ArgTypes  []ValueType
	ValueType ValueType
	isRate    bool
	isCounter bool
}

// Functions exposed by this package.
var (
	// Rate computes the per-second average growth over the range window.
	Rate = &Function{Name: "rate", ArgTypes: []ValueType{ValueTypeMatrix}, ValueType: ValueTypeVector, isRate: true, isCounter: true}
	// Increase computes the extrapolated total increase over the range window.
	Increase = &Function{Name: "increase", ArgTypes: []ValueType{ValueTypeMatrix}, ValueType: ValueTypeVector, isRate: false, isCounter: true}
)

var functionRegistry = map[string]*Function{
	"rate":     Rate,
	"increase": Increase,
}

// GetFunction looks up a registered function by name.
func GetFunction(name string) (*Function, bool) {
	f, ok := functionRegistry[name]
	return f, ok
}

func (e *Engine) evalCall(c *Call, tMs int64) (Value, error) {
	if c.Func == nil {
		return nil, errors.New("promqlengine: call without function")
	}
	if len(c.Args) != len(c.Func.ArgTypes) {
		return nil, fmt.Errorf("promqlengine: function %q expects %d argument(s), got %d",
			c.Func.Name, len(c.Func.ArgTypes), len(c.Args))
	}
	val, err := e.evalExpr(c.Args[0], tMs)
	if err != nil {
		return nil, err
	}
	mat, ok := val.(Matrix)
	if !ok {
		return nil, fmt.Errorf("promqlengine: %s requires a range-vector argument", c.Func.Name)
	}

	out := make([]Sample, 0, len(mat.Series))
	windowSeconds := float64(mat.RangeEndMs-mat.RangeStartMs) / 1000.0
	for _, s := range mat.Series {
		result, ok := extrapolatedRate(s.Points, mat.RangeStartMs, mat.RangeEndMs, c.Func.isCounter)
		if !ok {
			continue
		}
		if c.Func.isRate {
			result /= windowSeconds
		}
		out = append(out, Sample{Metric: s.Metric, Point: Point{T: tMs, V: result}})
	}
	sortVector(out)
	return out, nil
}

// Matrix is a range-vector result: series plus the exact window bounds
// [RangeStartMs, RangeEndMs] (Unix milliseconds) that produced them.
//
// The bounds are carried explicitly because extrapolation must be computed
// against the requested window, not against the first/last sample
// timestamps. Both range selectors and subqueries return this type.
type Matrix struct {
	Series       []Series
	RangeStartMs int64
	RangeEndMs   int64
}

// extrapolatedRate is a faithful port of Prometheus' extrapolatedRate helper
// (promql/functions.go).
//
// It returns (value, true) when the series contributes a result, or
// (0, false) when it must be skipped — the primary case being fewer than two
// non-stale samples in the window.
//
// Implemented semantics:
//   - counter reset: a sample below its predecessor adds the sample value
//     rather than the negative difference; a negative first sample is folded
//     in as the initial correction;
//   - average sample interval derived from first/last timestamps;
//   - end contributions: half the average of the endpoints when the edge gap
//     is at least half an average interval, otherwise the endpoint value;
//   - 1.1x threshold with 0.5x clamp independently at both edges;
//   - counter zero clamp: left extrapolation may never cross zero, so the
//     left duration is cut to the zero-crossing instant;
//   - regular NaNs flow through the arithmetic unchanged in kind.
func extrapolatedRate(samples []Point, rangeStart, rangeEnd int64, isCounter bool) (float64, bool) {
	if len(samples) < 2 {
		return 0, false
	}

	var resultFloat float64
	if isCounter && samples[0].V < 0 {
		resultFloat = samples[0].V
	}

	for i := 0; i < len(samples)-1; i++ {
		current := samples[i].V
		next := samples[i+1].V
		if isCounter && next < current {
			resultFloat += next
			continue
		}
		resultFloat += next - current
	}

	first := samples[0]
	last := samples[len(samples)-1]

	// Seconds from the window edges to the outermost samples.
	durationToStart := float64(first.T-rangeStart) / 1000.0
	durationToEnd := float64(rangeEnd-last.T) / 1000.0

	sampleInterval := float64(last.T-first.T) / float64(len(samples)-1) / 1000.0

	if isCounter {
		// A monotonic counter cannot be back-extrapolated past zero. When the
		// linear zero crossing happens before the targeted extrapolation time,
		// clamp the left duration to the zero crossing.
		if first.V > 0 {
			durationToZero := durationToStart - (first.V * sampleInterval / resultFloat)
			if durationToZero < durationToStart {
				durationToStart = durationToZero
			}
		}
	}

	if durationToStart >= sampleInterval/2.0 {
		resultFloat += averageValueBetween(first.V, last.V)
	} else {
		resultFloat += first.V
	}
	if durationToEnd >= sampleInterval/2.0 {
		resultFloat += averageValueBetween(first.V, last.V)
	} else {
		resultFloat += last.V
	}

	if durationToStart > 1.1*sampleInterval {
		durationToStart = 0.5 * sampleInterval
	}
	if durationToEnd > 1.1*sampleInterval {
		durationToEnd = 0.5 * sampleInterval
	}

	observedDuration := float64(last.T-first.T)/1000.0 + durationToStart + durationToEnd
	resultFloat = resultFloat * (float64(rangeEnd-rangeStart) / 1000.0) / observedDuration

	// Prometheus drops the result when a counter ends negative after a reset;
	// such series cannot produce a meaningful non-negative rate.
	if isCounter && resultFloat > 0 && (first.V < 0 || last.V < 0) {
		return 0, false
	}
	return resultFloat, true
}

// averageValueBetween is the value assumed at either extrapolated endpoint:
// the mean of the first and last sample values.
func averageValueBetween(first, last float64) float64 {
	return (first + last) / 2.0
}
