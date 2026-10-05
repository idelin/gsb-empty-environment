package main

// Interval is an uncertain clock reading: the true timestamp is known to lie
// in [Lo, Hi] (inclusive).
type Interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// Task describes one unit of work through three observed events:
//
//	ready: dependencies satisfied / queued for a resource
//	start: resource acquired, execution begins
//	end:   execution finished
//
// wait  = start - ready  (queueing: resource wait + dependency handoff)
// run   = end   - start  (compute)
type Task struct {
	ID    string   `json:"id"`
	Ready Interval `json:"ready"`
	Start Interval `json:"start"`
	End   Interval `json:"end"`
	Deps  []string `json:"deps"`
}

// Input is the top-level JSON document consumed by the analyzer.
type Input struct {
	Origin Interval `json:"origin"` // project clock zero event
	Tasks  []Task   `json:"tasks"`
}
