package matrixclock

// Hub is a synchronous in-order message transport used to simulate the
// network between nodes.
type Hub struct {
	nodes map[string]*Node
	queue []Message
}

// NewHub builds a Hub connecting the given nodes.
func NewHub(nodes ...*Node) *Hub {
	h := &Hub{nodes: make(map[string]*Node)}
	for _, n := range nodes {
		h.nodes[n.ID] = n
	}
	return h
}

// Send enqueues a message produced by Node.Send.
func (h *Hub) Send(m Message) { h.queue = append(h.queue, m) }

// Pending reports how many messages are still in flight.
func (h *Hub) Pending() int { return len(h.queue) }

// DeliverAll delivers every queued message to its recipient and returns
// the number of deliveries.
func (h *Hub) DeliverAll() int {
	delivered := 0
	for len(h.queue) > 0 {
		m := h.queue[0]
		h.queue = h.queue[1:]
		h.nodes[m.To].Receive(m)
		delivered++
	}
	return delivered
}
