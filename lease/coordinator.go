package lease

import (
	"time"

	"leasefencing/sim"
)

type Coordinator struct {
	id        string
	sim       *sim.Simulator
	ttl       time.Duration
	term      uint64
	fence     uint64
	holder    string
	expiresAt time.Time
}

func NewCoordinator(id string, s *sim.Simulator, ttl time.Duration) *Coordinator {
	return &Coordinator{id: id, sim: s, ttl: ttl}
}

func (c *Coordinator) Receive(msg interface{}) {
	switch m := msg.(type) {
	case CampaignRequest:
		c.handleCampaign(m)
	case RenewRequest:
		c.handleRenew(m)
	case TokenRequest:
		c.handleToken(m)
	}
}

func (c *Coordinator) handleCampaign(m CampaignRequest) {
	now := c.sim.Now()
	if c.holder != "" && c.holder != m.Candidate && now.Before(c.expiresAt) {
		c.sim.Send(c.id, m.Candidate, CampaignResponse{
			Granted: false,
			Term:    c.term,
			Reason:  "lease held by " + c.holder,
		})
		return
	}
	c.term++
	c.holder = m.Candidate
	c.expiresAt = now.Add(c.ttl)
	c.sim.Send(c.id, m.Candidate, CampaignResponse{
		Granted:   true,
		Term:      c.term,
		GrantedAt: now,
		ExpiresAt: c.expiresAt,
	})
}

func (c *Coordinator) handleRenew(m RenewRequest) {
	now := c.sim.Now()
	if m.Node != c.holder || m.Term != c.term || !now.Before(c.expiresAt) {
		c.sim.Send(c.id, m.Node, RenewResponse{
			Granted: false,
			Term:    c.term,
			Reason:  "lease not held",
		})
		return
	}
	c.expiresAt = now.Add(c.ttl)
	c.sim.Send(c.id, m.Node, RenewResponse{
		Granted:   true,
		Term:      c.term,
		GrantedAt: now,
		ExpiresAt: c.expiresAt,
	})
}

func (c *Coordinator) handleToken(m TokenRequest) {
	now := c.sim.Now()
	if m.Node != c.holder || m.Term != c.term || !now.Before(c.expiresAt) {
		c.sim.Send(c.id, m.Node, TokenResponse{
			Granted:   false,
			Term:      c.term,
			RequestID: m.RequestID,
			Reason:    "lease not held",
		})
		return
	}
	c.fence++
	c.sim.Send(c.id, m.Node, TokenResponse{
		Granted:   true,
		Term:      m.Term,
		Fence:     c.fence,
		RequestID: m.RequestID,
	})
}

func (c *Coordinator) Term() uint64 {
	return c.term
}

func (c *Coordinator) Fence() uint64 {
	return c.fence
}

func (c *Coordinator) Holder() string {
	return c.holder
}

func (c *Coordinator) ExpiresAt() time.Time {
	return c.expiresAt
}
