// Package sim implements a discrete-event simulation of a hierarchical
// weighted-fair-queueing (WFQ) packet scheduler. All time is in integer
// ticks; no real network traffic is generated.
package sim

// Packet is a unit of work. Virtual times are recorded for the service
// trace. All fields are exported so the whole simulation state can be
// snapshotted with encoding/gob.
type Packet struct {
	ID         int
	FlowID     int
	Bytes      int
	ArriveTick int

	// Flow-level virtual times, assigned at enqueue.
	VStart  float64
	VFinish float64

	ServeStart int
	ServeEnd   int

	Served    bool
	Cancelled bool
	Dropped   bool // backpressured or withdrawn while queued
}

// Flow is a leaf queue belonging to a tenant.
type Flow struct {
	ID       int
	TenantID int
	Weight   float64
	Capacity int // max queued packets; 0 means unlimited

	Queue     []*Packet
	VFinish   float64 // finish time of the last enqueued packet
	Active    bool
	Withdrawn bool

	ServedBytes int
}

// Tenant is an intermediate scheduling level; flows are grouped under it.
type Tenant struct {
	ID     int
	Weight float64

	Active  bool
	VStart  float64 // global-domain virtual times of the current candidate
	VFinish float64
}
