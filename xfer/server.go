package xfer

import (
	"net/netip"
)

// Rule is one admission entry for a zone. Within a rule every populated
// condition must hold: if Networks is non-empty the source address must fall
// inside one of the prefixes, and if KeyIDs is non-empty the request must
// carry a valid TSIG from one of those keys. A rule with neither set matches
// any source, which is only useful for deliberately open zones.
type Rule struct {
	Networks []netip.Prefix
	KeyIDs   []string
	Types    []RequestType // allowed transfer types; empty means none
	TCPOnly  bool          // require TCP transport
}

// Policy maps zone names to their admission rules. A zone without an entry
// is denied outright: the default posture is refuse.
type Policy map[string][]Rule

func (r Rule) allowsType(t RequestType) bool {
	for _, at := range r.Types {
		if at == t {
			return true
		}
	}
	return false
}

func (r Rule) allowsSource(addr netip.Addr) bool {
	if len(r.Networks) == 0 {
		return true
	}
	for _, p := range r.Networks {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (r Rule) keyAllowed(id string) bool {
	for _, k := range r.KeyIDs {
		if k == id {
			return true
		}
	}
	return false
}

// Server evaluates transfer requests against a policy and streams authorized
// transfers from consistent zone snapshots.
type Server struct {
	policy Policy
	store  *Store
	keys   *Keyring
}

// NewServer wires a policy, zone store and keyring into a transfer server.
func NewServer(policy Policy, store *Store, keys *Keyring) *Server {
	return &Server{policy: policy, store: store, keys: keys}
}

// authorize decides whether the request may be answered. It returns nil on
// success or a *DenyError. On success the request's replay slot is consumed.
func (s *Server) authorize(req *Request) error {
	if req.Type == TypeAXFR && req.Proto != ProtoTCP {
		return denyf("AXFR over %s is never served", req.Proto)
	}
	rules, ok := s.policy[req.Zone]
	if !ok {
		return denyf("no transfer policy for zone %q", req.Zone)
	}
	matched := false
	for _, rule := range rules {
		if !rule.allowsType(req.Type) {
			continue
		}
		if rule.TCPOnly && req.Proto != ProtoTCP {
			continue
		}
		if !rule.allowsSource(req.Source) {
			continue
		}
		if len(rule.KeyIDs) > 0 {
			if req.TSIG == nil || !rule.keyAllowed(req.TSIG.KeyID) {
				continue
			}
			if !s.keys.valid(req) {
				continue
			}
		}
		matched = true
		break
	}
	if !matched {
		return denyf("source %s not admitted for %s %s", req.Source, req.Zone, req.Type)
	}
	// A rule required a signature: verify it for real, which also enforces
	// replay protection. Rules without keys skip this entirely.
	if req.TSIG != nil {
		if err := s.keys.verify(req); err != nil {
			return err
		}
	}
	return nil
}

// Begin authorizes the request and, if admitted, pins a consistent snapshot
// of the zone and returns a Transfer streaming it in chunks. Unauthorized
// requests receive only a *DenyError and no records.
func (s *Server) Begin(req *Request) (*Transfer, error) {
	if err := s.authorize(req); err != nil {
		return nil, err
	}
	snap, ok := s.store.pin(req.Zone)
	if !ok {
		return nil, denyf("unknown zone %q", req.Zone)
	}
	stream := s.buildStream(req, snap)
	return &Transfer{
		store:   s.store,
		zone:    req.Zone,
		serial:  snap.serial,
		records: stream,
	}, nil
}

// buildStream lays out the full record sequence for the transfer. AXFR is
// SOA, records, SOA. IXFR against the current serial is a single SOA; IXFR
// with journal coverage is the delta sequence; otherwise it falls back to a
// full AXFR-style stream.
func (s *Server) buildStream(req *Request, snap *snapshot) []Record {
	axfr := func() []Record {
		out := make([]Record, 0, len(snap.records)+2)
		out = append(out, snap.soa)
		out = append(out, snap.records...)
		out = append(out, snap.soa)
		return out
	}
	if req.Type == TypeAXFR {
		return axfr()
	}
	if req.ClientSerial == snap.serial {
		return []Record{snap.soa}
	}
	deltas, ok := s.store.deltas(req.Zone, req.ClientSerial)
	if !ok {
		return axfr()
	}
	var out []Record
	out = append(out, snap.soa)
	for _, d := range deltas {
		out = append(out, d.Removed...)
		out = append(out, d.Added...)
	}
	out = append(out, snap.soa)
	return out
}

// Transfer streams one pinned zone version in chunks. The first chunk starts
// with the SOA and the final chunk ends with it. If the zone changes in the
// store before the stream finishes, Next returns ErrZoneChanged and the
// transfer must be abandoned: old and new versions are never mixed.
type Transfer struct {
	store   *Store
	zone    string
	serial  uint32
	records []Record
	pos     int
}

// Serial is the zone version this transfer serves.
func (t *Transfer) Serial() uint32 { return t.serial }

// Next returns the next chunk of at most n records. The second return value
// is true when the transfer is complete. Any zone change detected before a
// chunk is emitted aborts the stream with ErrZoneChanged.
func (t *Transfer) Next(n int) ([]Record, bool, error) {
	if n < 1 {
		n = 1
	}
	cur, ok := t.store.currentSerial(t.zone)
	if !ok || cur != t.serial {
		return nil, false, ErrZoneChanged
	}
	if t.pos >= len(t.records) {
		return nil, true, nil
	}
	end := t.pos + n
	if end > len(t.records) {
		end = len(t.records)
	}
	chunk := t.records[t.pos:end]
	t.pos = end
	done := t.pos >= len(t.records)
	if done {
		// The closing SOA is part of the stream; re-check consistency so the
		// final chunk never ships after the zone moved on.
		cur, ok = t.store.currentSerial(t.zone)
		if !ok || cur != t.serial {
			return nil, false, ErrZoneChanged
		}
	}
	return chunk, done, nil
}
