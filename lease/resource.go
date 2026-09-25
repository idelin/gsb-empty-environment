package lease

import "leasefencing/sim"

type Resource struct {
	id        string
	sim       *sim.Simulator
	lastFence uint64
	value     string
	hasValue  bool

	Rejected      uint64
	LastRejection string
}

func NewResource(id string, s *sim.Simulator) *Resource {
	return &Resource{id: id, sim: s}
}

func (r *Resource) Receive(msg interface{}) {
	req, ok := msg.(WriteRequest)
	if !ok {
		return
	}
	resp := WriteResponse{Fence: req.Fence, Value: req.Value}
	switch {
	case req.Fence == 0:
		resp.Accepted = false
		resp.Reason = "fence token must be positive"
	case r.hasValue && req.Fence == r.lastFence:
		resp.Accepted = false
		resp.Reason = "duplicate fence token"
	case r.hasValue && req.Fence < r.lastFence:
		resp.Accepted = false
		resp.Reason = "stale fence token"
	default:
		r.lastFence = req.Fence
		r.value = req.Value
		r.hasValue = true
		resp.Accepted = true
	}
	if !resp.Accepted {
		r.Rejected++
		r.LastRejection = resp.Reason
	}
	if req.From != "" {
		r.sim.Send(r.id, req.From, resp)
	}
}

func (r *Resource) Value() string {
	return r.value
}

func (r *Resource) LastFence() uint64 {
	return r.lastFence
}
