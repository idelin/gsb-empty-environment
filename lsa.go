package sim

// LSA is a link-state advertisement: the originating router, a
// monotonically increasing sequence number, and the origin's current
// adjacencies (neighbour router ID -> link cost).
type LSA struct {
	Origin int
	Seq    int
	Links  map[int]int
}

// Purge asks routers to drop a previously installed LSA (sent when the
// LSA's lifetime expires).
type Purge struct {
	Origin int
	Seq    int
}

// Message is the unit of traffic carried by links.
type Message struct {
	LSA   *LSA
	Purge *Purge
}
