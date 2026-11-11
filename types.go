package netsec

import "errors"

// ErrInfeasible reports that no complete trajectory can satisfy every
// constraint (allowed cells, reachability, speed limits and total length).
var ErrInfeasible = errors.New("netsec: no complete feasible trajectory satisfies the constraints")

// ErrInvalidProblem reports a malformed Problem.
var ErrInvalidProblem = errors.New("netsec: invalid problem")

// Cell identifies a grid cell by integer coordinates.
type Cell struct {
	Row int
	Col int
}

// Edge is a directed road link. Length is the non-negative integer distance
// travelled when the link is used in one time step; it is also the speed
// (distance per step) of that move.
type Edge struct {
	To     Cell
	Length int
}

// Graph reports the directed road links leaving a cell.
type Graph interface {
	Edges(from Cell) []Edge
}

// Interval is a closed integer interval [Min, Max].
type Interval struct {
	Min int
	Max int
}

// Range is a half-open interval of time indices [Start, End).
type Range struct {
	Start int
	End   int
}

// Problem describes the privacy corridor constraints.
//
// Time is indexed 0..len(Allowed)-1 with unit steps. At every time t the true
// position must be one of Allowed[t]. Consecutive positions must be connected
// by the road graph (an implicit zero-length "stay" move always exists).
//
// MaxSpeed caps the length (speed) of any single move; a value <= 0 means no
// cap beyond what the graph offers. MaxSpeedChange caps |speed_t-speed_{t-1}|
// for every step after the first, because no previous speed exists at time 0.
// TotalLength bounds the accumulated travelled length at the final time.
type Problem struct {
	Allowed        [][]Cell
	Graph          Graph
	MaxSpeed       int
	MaxSpeedChange int
	TotalLength    Interval
}

// State is an explicit solver state: the position together with the previous
// speed and the accumulated travelled length.
type State struct {
	Cell     Cell
	Speed    int
	Distance int
}
