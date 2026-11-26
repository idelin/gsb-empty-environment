package mxadmit

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrInconsistentOrder is returned when the supplied partial order, or
	// the observed activation ticks, admits no total order.
	ErrInconsistentOrder = errors.New("mxadmit: inconsistent activation order")

	// ErrTooManyOrderings is returned when enumeration exceeds the requested
	// bound.  The caller may raise the bound or inspect a smaller batch.
	ErrTooManyOrderings = errors.New("mxadmit: too many compatible orderings")
)

// Ordering is one total order compatible with the observed partial orders.
type Ordering struct {
	EventIDs []string
}

type internalEvent struct {
	Activation
}

type world struct {
	// events are indices into model.events in total order.
	events []int
}

type model struct {
	batch    Batch
	configs  map[string]*SignedConfig
	receipts map[string]*Receipt

	events    []internalEvent
	eventByID map[string]int
	worlds    []world
	endpoints []string
}

func (m *model) config(id string) *SignedConfig {
	return m.configs[id]
}

func canonicalEventID(configID, endpoint string, at Tick) string {
	return fmt.Sprintf("%s|%s|%d", configID, endpoint, int64(at))
}

func buildModel(b Batch, orderingLimit int) (*model, error) {
	if orderingLimit <= 0 {
		orderingLimit = defaultOrderingLimit
	}
	m := &model{
		batch:     b,
		configs:   make(map[string]*SignedConfig, len(b.Configs)),
		receipts:  make(map[string]*Receipt, len(b.Receipts)),
		eventByID: make(map[string]int),
	}

	for i := range b.Configs {
		c := &b.Configs[i]
		if c.ID == "" {
			return nil, fmt.Errorf("mxadmit: config at index %d has an empty ID", i)
		}
		if _, exists := m.configs[c.ID]; exists {
			return nil, fmt.Errorf("mxadmit: duplicate config ID %q", c.ID)
		}
		m.configs[c.ID] = c
	}

	for i := range b.Receipts {
		r := &b.Receipts[i]
		id := r.ID
		if id == "" {
			id = fmt.Sprintf("receipt@%d", i)
			b.Receipts[i].ID = id
			r = &b.Receipts[i]
		}
		if _, exists := m.receipts[id]; exists {
			return nil, fmt.Errorf("mxadmit: duplicate receipt ID %q", id)
		}
		m.receipts[id] = r
	}
	deliveryIDs := make(map[string]struct{}, len(b.Deliveries))
	for i := range b.Deliveries {
		if b.Deliveries[i].ID == "" {
			b.Deliveries[i].ID = fmt.Sprintf("delivery@%d", i)
		}
		id := b.Deliveries[i].ID
		if _, exists := deliveryIDs[id]; exists {
			return nil, fmt.Errorf("mxadmit: duplicate delivery ID %q", id)
		}
		deliveryIDs[id] = struct{}{}
	}
	m.batch = b

	if err := m.buildEvents(); err != nil {
		return nil, err
	}
	if err := m.enumerate(b, orderingLimit); err != nil {
		return nil, err
	}
	m.collectEndpoints()
	return m, nil
}

const defaultOrderingLimit = 8192

func (m *model) buildEvents() error {
	var activations []Activation
	if len(m.batch.Activations) == 0 {
		// When the host supplies only signature intervals, treat each
		// signature as becoming active at the start of its validity.  This
		// keeps the common two-configuration case usable without inventing
		// endpoint-specific observations.
		for i := range m.batch.Configs {
			c := &m.batch.Configs[i]
			if c.Validity.Empty() {
				continue
			}
			activations = append(activations, Activation{
				EventID:  "implicit:" + c.ID,
				ConfigID: c.ID,
				At:       c.Validity.From,
			})
		}
	} else {
		activations = append(activations, m.batch.Activations...)
	}

	m.events = make([]internalEvent, 0, len(activations))
	for i, a := range activations {
		if a.ConfigID == "" {
			return fmt.Errorf("mxadmit: activation %d has an empty config ID", i)
		}
		if _, ok := m.config(a.ConfigID); !ok {
			return fmt.Errorf("mxadmit: activation %q references unknown config %q", a.EventID, a.ConfigID)
		}
		id := a.EventID
		if id == "" {
			id = canonicalEventID(a.ConfigID, a.Endpoint, a.At)
			a.EventID = id
		}
		if _, exists := m.eventByID[id]; exists {
			return fmt.Errorf("mxadmit: duplicate activation event ID %q", id)
		}
		a.EventID = id
		m.eventByID[id] = len(m.events)
		m.events = append(m.events, internalEvent{Activation: a})
	}
	return nil
}

func (m *model) collectEndpoints() {
	seen := make(map[string]struct{})
	add := func(s string) {
		if s != "" {
			seen[s] = struct{}{}
		}
	}
	for _, e := range m.events {
		add(e.Endpoint)
	}
	for _, r := range m.batch.Receipts {
		add(r.Endpoint)
	}
	for _, d := range m.batch.Deliveries {
		add(d.Endpoint)
	}
	m.endpoints = m.endpoints[:0]
	for e := range seen {
		m.endpoints = append(m.endpoints, e)
	}
	sort.Strings(m.endpoints)
}

// EnumerateOrderings returns every total order compatible with the batch's
// partial order and exact activation ticks.  max must be positive; if more
// than max orders exist, ErrTooManyOrderings is returned.
func EnumerateOrderings(b Batch, max int) ([]Ordering, error) {
	if max <= 0 {
		return nil, fmt.Errorf("mxadmit: ordering limit must be positive")
	}
	m, err := buildModel(b, max)
	if err != nil {
		return nil, err
	}
	if err := m.enumerate(b, max); err != nil {
		return nil, err
	}
	out := make([]Ordering, 0, len(m.worlds))
	for _, w := range m.worlds {
		ids := make([]string, len(w.events))
		for i, ei := range w.events {
			ids[i] = m.events[ei].EventID
		}
		out = append(out, Ordering{EventIDs: ids})
	}
	return out, nil
}

func (m *model) enumerate(_ Batch, max int) error {
	n := len(m.events)
	if n == 0 {
		m.worlds = []world{{}}
		return nil
	}

	adj := make([][]int, n)
	indeg := make([]int, n)
	seenEdges := make(map[[2]int]struct{})
	addEdge := func(from, to int) {
		if from == to {
			return
		}
		k := [2]int{from, to}
		if _, ok := seenEdges[k]; ok {
			return
		}
		seenEdges[k] = struct{}{}
		adj[from] = append(adj[from], to)
		indeg[to]++
	}

	for _, c := range m.batch.Order {
		from, ok := m.eventByID[c.Before]
		if !ok {
			return fmt.Errorf("%w: unknown event %q in constraint", ErrInconsistentOrder, c.Before)
		}
		to, ok := m.eventByID[c.After]
		if !ok {
			return fmt.Errorf("%w: unknown event %q in constraint", ErrInconsistentOrder, c.After)
		}
		addEdge(from, to)
	}

	// Exact activation ticks are facts.  If A happened at an earlier tick than
	// B, every compatible order must place A first.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if m.events[i].At < m.events[j].At {
				addEdge(i, j)
			}
		}
	}

	if hasCycle(adj, indeg) {
		return fmt.Errorf("%w: supplied order contradicts activation ticks", ErrInconsistentOrder)
	}

	for i := range adj {
		sort.Slice(adj[i], func(a, b int) bool {
			return m.eventSortKey(adj[i][a]) < m.eventSortKey(adj[i][b])
		})
	}

	m.worlds = m.worlds[:0]
	var prefix []int
	var overflow bool
	var walk func(indeg []int, available []int)
	walk = func(curIndeg []int, available []int) {
		if overflow {
			return
		}
		if len(prefix) == n {
			if len(m.worlds) >= max {
				overflow = true
				return
			}
			cp := append([]int(nil), prefix...)
			m.worlds = append(m.worlds, world{events: cp})
			return
		}
		sort.Slice(available, func(a, b int) bool {
			return m.eventSortKey(available[a]) < m.eventSortKey(available[b])
		})
		for idx, node := range available {
			nextAvail := make([]int, 0, len(available)-1+len(adj[node]))
			nextAvail = append(nextAvail, available[:idx]...)
			nextAvail = append(nextAvail, available[idx+1:]...)
			nextIndeg := append([]int(nil), curIndeg...)
			for _, to := range adj[node] {
				nextIndeg[to]--
				if nextIndeg[to] == 0 {
					nextAvail = append(nextAvail, to)
				}
			}
			prefix = append(prefix, node)
			walk(nextIndeg, nextAvail)
			prefix = prefix[:len(prefix)-1]
			if overflow {
				return
			}
		}
	}

	available := make([]int, 0, n)
	for i, d := range indeg {
		if d == 0 {
			available = append(available, i)
		}
	}
	walk(indeg, available)
	if overflow {
		return fmt.Errorf("%w: limit %d", ErrTooManyOrderings, max)
	}
	return nil
}

func (m *model) eventSortKey(i int) string {
	e := m.events[i]
	return fmt.Sprintf("%020d|%s|%s|%s", int64(e.At), e.Endpoint, e.ConfigID, e.EventID)
}

func hasCycle(adj [][]int, indeg []int) bool {
	deg := append([]int(nil), indeg...)
	q := make([]int, 0, len(deg))
	for i, d := range deg {
		if d == 0 {
			q = append(q, i)
		}
	}
	count := 0
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		count++
		for _, to := range adj[n] {
			deg[to]--
			if deg[to] == 0 {
				q = append(q, to)
			}
		}
	}
	return count != len(deg)
}

func eventApplies(eventEndpoint, endpoint string) bool {
	if eventEndpoint == "" {
		return true
	}
	return eventEndpoint == endpoint
}

// authorityAt returns the latest activation that is still a valid, signed
// authority for domain at tick t.  A configuration that mentions other domains
// only is skipped; it cannot supersede this domain.  Configurations whose
// signature is not effective at t are also skipped, so a newer but expired
// update does not erase an older authority that is still signed and effective.
func (m *model) authorityAt(w *world, endpoint, domain string, t Tick) *SignedConfig {
	for i := len(w.events) - 1; i >= 0; i-- {
		e := m.events[w.events[i]]
		if e.At > t {
			continue
		}
		if !eventApplies(e.Endpoint, endpoint) {
			continue
		}
		c := m.config(e.ConfigID)
		if c == nil || !c.Validity.Contains(t) || !c.MentionsDomain(domain) {
			continue
		}
		return c
	}
	return nil
}

func (m *model) authorityAtAll(endpoint, domain string, t Tick) []*SignedConfig {
	out := make([]*SignedConfig, 0, len(m.worlds))
	for wi := range m.worlds {
		out = append(out, m.authorityAt(&m.worlds[wi], endpoint, domain, t))
	}
	return out
}

func (m *model) hasWorlds() bool {
	return len(m.worlds) > 0
}

func (m *model) endpointCount() int {
	return len(m.endpoints)
}

func (m *model) singleEndpoint() (string, bool) {
	if len(m.endpoints) == 1 {
		return m.endpoints[0], true
	}
	return "", false
}

func (m *model) resolveEndpoint(d Delivery, r *Receipt) (string, error) {
	if d.Endpoint != "" {
		return d.Endpoint, nil
	}
	if r != nil && r.Endpoint != "" {
		return r.Endpoint, nil
	}
	if e, ok := m.singleEndpoint(); ok {
		return e, nil
	}
	if len(m.endpoints) == 0 {
		return "", nil
	}
	return "", fmt.Errorf("mxadmit: delivery %q has ambiguous endpoint %s",
		d.ID, strings.Join(m.endpoints, ","))
}

// allowedPairs returns the (public key, path digest) pairs jointly authorized
// by one entry in cfg for the given domain, MX and DNS generation.
func allowedPairs(cfg *SignedConfig, domain, mx string, dns int64) map[[2]string]struct{} {
	out := make(map[[2]string]struct{})
	if cfg == nil {
		return out
	}
	for _, e := range cfg.Entries {
		if e.Domain != domain || e.MX != mx || e.DNSGeneration != dns {
			continue
		}
		if e.PathDigest != "" {
			out[[2]string{e.PublicKey, e.PathDigest}] = struct{}{}
		}
		for _, p := range e.PathDigests {
			out[[2]string{e.PublicKey, p}] = struct{}{}
		}
	}
	return out
}

func intersectPairs(acc map[[2]string]struct{}, next map[[2]string]struct{}) map[[2]string]struct{} {
	if len(acc) == 0 {
		return make(map[[2]string]struct{})
	}
	out := make(map[[2]string]struct{})
	for pair := range acc {
		if _, ok := next[pair]; ok {
			out[pair] = struct{}{}
		}
	}
	return out
}

// certainPairs returns pairs authorized by every compatible world at t.
func (m *model) certainPairs(endpoint, domain, mx string, dns int64, t Tick) map[[2]string]struct{} {
	var out map[[2]string]struct{}
	for wi := range m.worlds {
		cfg := m.authorityAt(&m.worlds[wi], endpoint, domain, t)
		pairs := allowedPairs(cfg, domain, mx, dns)
		if wi == 0 {
			out = pairs
		} else {
			out = intersectPairs(out, pairs)
		}
		if len(out) == 0 {
			break
		}
	}
	if out == nil {
		out = make(map[[2]string]struct{})
	}
	return out
}

func pairAllowed(pairs map[[2]string]struct{}, key, path string) bool {
	_, ok := pairs[[2]string{key, path}]
	return ok
}
