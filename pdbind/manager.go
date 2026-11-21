package pdbind

import (
	"net/netip"
	"strconv"
	"sync"
	"time"
)

const (
	defaultPreferredLft = uint32(86400)
	defaultValidLft     = uint32(2 * 86400)
)

// Manager owns the delegation bindings and coordinates routing table changes.
// It is safe for concurrent use.
type Manager struct {
	cfg    Config
	rt     RouteTable
	mu     sync.Mutex
	binds  map[BindingKey]*Binding
	owners map[uint64]BindingKey
}

// NewManager creates a Manager from cfg and routes prefix changes through rt.
func NewManager(cfg Config, rt RouteTable) *Manager {
	if cfg.MaxRelayDepth <= 0 {
		cfg.MaxRelayDepth = DefaultMaxRelayDepth
	}
	if cfg.PreferredLft == 0 {
		cfg.PreferredLft = defaultPreferredLft
	}
	if cfg.ValidLft == 0 {
		cfg.ValidLft = defaultValidLft
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manager{
		cfg:    cfg,
		rt:     rt,
		binds:  make(map[BindingKey]*Binding),
		owners: make(map[uint64]BindingKey),
	}
}

// Decision is the staged outcome of processing one message. The caller
// serialises a Reply carrying Status and IAPDs; Commit must be called only
// after the Reply has been accepted for transmission. Abort discards every
// staged binding and routing change without side effects.
type Decision struct {
	Status StatusCode
	IAPDs  []IAPD

	mgr       *Manager
	committed bool
	aborted   bool

	upserts map[BindingKey]*Binding
	deletes map[BindingKey]netip.Prefix
	routes  []stagedRoute
}

type stagedRoute struct {
	install bool
	route   Route
	remove  netip.Prefix
}

// Commit applies the staged binding and routing changes. It is safe to call
// once per Decision; later calls are no-ops.
func (d *Decision) Commit() error {
	if d == nil || d.committed || d.aborted {
		return nil
	}
	d.committed = true

	d.mgr.mu.Lock()
	for key, prefix := range d.deletes {
		delete(d.mgr.binds, key)
		delete(d.mgr.owners, d.mgr.cfg.Pool.indexOf(prefix))
	}
	for key, binding := range d.upserts {
		d.mgr.binds[key] = binding
		d.mgr.owners[d.mgr.cfg.Pool.indexOf(binding.Prefix)] = key
	}
	d.mgr.mu.Unlock()

	for _, op := range d.routes {
		var err error
		if op.install {
			err = d.mgr.rt.Install(op.route)
		} else {
			err = d.mgr.rt.Remove(op.remove)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Abort drops all staged changes without touching bindings or routes.
func (d *Decision) Abort() {
	if d == nil {
		return
	}
	d.aborted = true
	d.upserts = nil
	d.deletes = nil
	d.routes = nil
}

// usesIndex reports whether the decision already stages a delegation for the
// given pool slot (a new binding or an existing one being renewed).
func (d *Decision) usesIndex(idx uint64) bool {
	for _, binding := range d.upserts {
		if d.mgr.cfg.Pool.indexOf(binding.Prefix) == idx {
			return true
		}
	}
	return false
}

// Lookup returns a copy of the live binding for key, or false when no current
// (non-expired) binding exists.
func (m *Manager) Lookup(key BindingKey) (Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.lookupLocked(key)
	if !ok {
		return Binding{}, false
	}
	return *binding, true
}

// Bindings returns copies of every currently valid binding.
func (m *Manager) Bindings() []Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	out := make([]Binding, 0, len(m.binds))
	for _, binding := range m.binds {
		if !binding.ValidUntil.After(now) {
			continue
		}
		out = append(out, *binding)
	}
	return out
}

// SweepExpired removes every binding whose valid lifetime has elapsed and
// withdraws its route. It returns the prefixes that were reclaimed.
func (m *Manager) SweepExpired() []netip.Prefix {
	m.mu.Lock()
	now := m.cfg.Now()
	var reclaimed []netip.Prefix
	var removed []netip.Prefix
	for key, binding := range m.binds {
		if binding.ValidUntil.After(now) {
			continue
		}
		reclaimed = append(reclaimed, binding.Prefix)
		removed = append(removed, binding.Prefix)
		delete(m.binds, key)
		delete(m.owners, m.cfg.Pool.indexOf(binding.Prefix))
	}
	m.mu.Unlock()

	for _, prefix := range removed {
		_ = m.rt.Remove(prefix)
	}
	return reclaimed
}

// Process handles one parsed client message. relay is nil for messages
// received directly from a client. The returned Decision is always non-nil;
// its Status is encoded into the Reply by the caller.
func (m *Manager) Process(msg ClientMessage, relay []RelayHop) (*Decision, error) {
	if err := m.validateRelay(relay); err != nil {
		return nil, err
	}
	if len(msg.DUID) == 0 {
		return nil, ErrNoDUID
	}

	d := &Decision{
		mgr:     m,
		upserts: make(map[BindingKey]*Binding),
		deletes: make(map[BindingKey]netip.Prefix),
	}

	switch msg.Type {
	case MsgSolicit, MsgRequest:
		m.assign(msg, relay, d)
	case MsgRenew:
		m.renew(msg, relay, d)
	case MsgRebind:
		m.rebind(msg, relay, d)
	case MsgRelease:
		m.release(msg, relay, d)
	case MsgConfirm:
		m.confirm(msg, d)
	default:
		return nil, ErrUnsupportedMessage
	}
	return d, nil
}

func itoa(v uint32) string { return strconv.Itoa(int(v)) }
