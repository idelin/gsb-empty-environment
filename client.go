package causalrouter

// Client maintains a local version vector. Sending increments the own
// entry; receiving merges the observed clock.
type Client struct {
	ID    string
	clock VectorClock
}

func NewClient(id string) *Client {
	return &Client{ID: id, clock: VectorClock{}}
}

// Send increments the client's own clock entry and returns a stamped
// message ready to be submitted to a Router.
func (c *Client) Send(payload string) *Message {
	c.clock.Increment(c.ID)
	return NewMessage(c.ID, c.clock, payload)
}

// Receive merges the clock of an observed message into the local clock.
func (c *Client) Receive(m *Message) {
	c.clock.Merge(m.Clock)
}

// Clock returns a copy of the client's current version vector.
func (c *Client) Clock() VectorClock {
	return c.clock.Copy()
}
