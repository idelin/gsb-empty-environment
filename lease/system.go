package lease

import (
	"time"

	"leasefencing/sim"
)

const (
	CoordinatorID = "coordinator"
	ResourceID    = "resource"
)

type System struct {
	Sim         *sim.Simulator
	Coordinator *Coordinator
	Resource    *Resource
	Nodes       map[string]*Node
}

func NewSystem(nodeIDs []string, ttl, maxClockError time.Duration, offsets map[string]time.Duration) *System {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := sim.NewSimulator(start)
	s.SetDefaultDelay(10 * time.Millisecond)
	coord := NewCoordinator(CoordinatorID, s, ttl)
	res := NewResource(ResourceID, s)
	s.Register(CoordinatorID, coord)
	s.Register(ResourceID, res)
	nodes := make(map[string]*Node)
	for _, id := range nodeIDs {
		clock := sim.NewSkewedClock(s.Clock(), offsets[id], 1, 1)
		node := NewNode(id, s, CoordinatorID, ResourceID, clock, ttl, maxClockError)
		nodes[id] = node
		s.Register(id, node)
	}
	return &System{
		Sim:         s,
		Coordinator: coord,
		Resource:    res,
		Nodes:       nodes,
	}
}
