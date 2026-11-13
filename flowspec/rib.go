package flowspec

import "net/netip"

// Route is a unicast route as known to the local routing process.
// NeighborAS identifies the neighboring AS the route was received from
// (the leftmost AS of the AS_PATH for eBGP routes).
type Route struct {
	Prefix     netip.Prefix
	NeighborAS uint32
}

// UnicastRIB abstracts the unicast routing table the validator consults.
type UnicastRIB interface {
	// BestMatch returns the longest-prefix-match unicast route covering
	// the given prefix, and whether any route matched at all.
	BestMatch(prefix netip.Prefix) (Route, bool)
	// MoreSpecifics returns all unicast routes strictly more specific
	// than (i.e. contained in) the given prefix.
	MoreSpecifics(prefix netip.Prefix) []Route
}

// Table is a simple in-memory UnicastRIB suitable for embedding into a
// routing process or for tests.
type Table struct {
	routes []Route
}

// NewTable returns a Table pre-populated with the given routes.
func NewTable(routes ...Route) *Table {
	t := &Table{}
	t.routes = append(t.routes, routes...)
	return t
}

// Add inserts a route into the table.
func (t *Table) Add(r Route) {
	t.routes = append(t.routes, r)
}

// BestMatch implements UnicastRIB.
func (t *Table) BestMatch(p netip.Prefix) (Route, bool) {
	var best Route
	found := false
	for _, r := range t.routes {
		if r.Prefix.Bits() > p.Bits() {
			continue
		}
		if !r.Prefix.Contains(p.Addr()) {
			continue
		}
		if !found || r.Prefix.Bits() > best.Prefix.Bits() {
			best, found = r, true
		}
	}
	return best, found
}

// MoreSpecifics implements UnicastRIB.
func (t *Table) MoreSpecifics(p netip.Prefix) []Route {
	var out []Route
	for _, r := range t.routes {
		if r.Prefix.Bits() > p.Bits() && p.Contains(r.Prefix.Masked().Addr()) {
			out = append(out, r)
		}
	}
	return out
}
