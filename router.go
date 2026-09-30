package sim

const (
	defaultLSALifetime = 200
	defaultSPFDelay    = 10
)

// Route is a single forwarding-table entry.
type Route struct {
	NextHop int
	Cost    int
}

type lsdbEntry struct {
	lsa     LSA
	install int
}

// Router originates, floods and stores LSAs, and recomputes routes
// with a debounced shortest-path-first run.
type Router struct {
	ID   int
	Down bool

	LSALifetime int // ticks an LSA stays valid; <=0 disables ageing
	SPFDelay    int // debounce window for SPF recomputation

	SPFRuns       int
	Originated    int
	DroppedOldLSA int

	net    *Network
	links  []*Link
	lsdb   map[int]lsdbEntry
	seq    int
	routes map[int]Route

	spfPending bool
}

func NewRouter(id int) *Router {
	return &Router{
		ID:          id,
		LSALifetime: defaultLSALifetime,
		SPFDelay:    defaultSPFDelay,
		lsdb:        make(map[int]lsdbEntry),
		routes:      make(map[int]Route),
	}
}

// Routes returns the current forwarding table (destination -> route).
func (r *Router) Routes() map[int]Route { return r.routes }

// LSDB returns a snapshot of the router's link-state database.
func (r *Router) LSDB() map[int]LSA {
	out := make(map[int]LSA, len(r.lsdb))
	for origin, e := range r.lsdb {
		out[origin] = e.lsa
	}
	return out
}

// Start originates the router's first LSA.
func (r *Router) Start() { r.originate() }

func (r *Router) neighbors() map[int]int {
	out := make(map[int]int)
	for _, l := range r.links {
		if !l.Up {
			continue
		}
		out[l.other(r).ID] = l.Cost
	}
	return out
}

func (r *Router) originate() {
	if r.Down {
		return
	}
	r.seq++
	r.Originated++
	lsa := LSA{Origin: r.ID, Seq: r.seq, Links: r.neighbors()}
	r.install(lsa)
	r.flood(Message{LSA: &lsa}, -1)
}

func (r *Router) onLinkChange() { r.originate() }

func (r *Router) install(lsa LSA) {
	now := r.net.Sim.Now()
	r.lsdb[lsa.Origin] = lsdbEntry{lsa: lsa, install: now}
	r.scheduleExpiry(lsa, now)
	r.scheduleSPF()
}

func (r *Router) scheduleExpiry(lsa LSA, installAt int) {
	if r.LSALifetime <= 0 {
		return
	}
	s := r.net.Sim
	s.After(r.LSALifetime, func() {
		e, ok := r.lsdb[lsa.Origin]
		if !ok || e.lsa.Seq != lsa.Seq || e.install != installAt {
			return // superseded by a newer LSA meanwhile
		}
		delete(r.lsdb, lsa.Origin)
		r.flood(Message{Purge: &Purge{Origin: lsa.Origin, Seq: lsa.Seq}}, -1)
		r.scheduleSPF()
	})
	if lsa.Origin == r.ID {
		// Refresh our own LSA at half its lifetime so neighbours
		// never age it out while we are alive.
		s.After(r.LSALifetime/2, func() {
			e, ok := r.lsdb[r.ID]
			if ok && e.lsa.Seq == lsa.Seq && e.install == installAt {
				r.originate()
			}
		})
	}
}

func (r *Router) flood(msg Message, except int) {
	if r.Down {
		return
	}
	for _, l := range r.links {
		if !l.Up {
			continue
		}
		if n := l.other(r); n.ID != except {
			l.Send(r, msg)
		}
	}
}

func (r *Router) receive(msg Message, from *Router) {
	if r.Down {
		return
	}
	switch {
	case msg.LSA != nil:
		r.receiveLSA(*msg.LSA, from.ID)
	case msg.Purge != nil:
		r.receivePurge(*msg.Purge, from.ID)
	}
}

func (r *Router) receiveLSA(lsa LSA, fromID int) {
	e, ok := r.lsdb[lsa.Origin]
	if ok && lsa.Seq <= e.lsa.Seq {
		// Duplicate or older advertisement: drop it, so a stale
		// copy can never resurrect a deleted link.
		r.DroppedOldLSA++
		return
	}
	r.install(lsa)
	r.flood(Message{LSA: &lsa}, fromID)
}

func (r *Router) receivePurge(p Purge, fromID int) {
	e, ok := r.lsdb[p.Origin]
	if !ok || e.lsa.Seq > p.Seq {
		return // nothing to purge, or we hold a newer LSA
	}
	delete(r.lsdb, p.Origin)
	r.flood(Message{Purge: &p}, fromID)
	r.scheduleSPF()
}

// scheduleSPF debounces SPF recomputation: any number of LSDB changes
// inside one SPFDelay window trigger exactly one run.
func (r *Router) scheduleSPF() {
	if r.spfPending {
		return
	}
	r.spfPending = true
	r.net.Sim.After(r.SPFDelay, func() {
		r.spfPending = false
		r.SPFRuns++
		r.routes = r.computeRoutes()
	})
}
