package scripted

import (
	"context"
	"sync"
	"time"

	"example.org/netsec/q0422"
)

// Envelope is the scripted implementation of netsec.Envelope.
type Envelope struct {
	OpIDVal  int
	Terminal bool
	EntryVal *netsec.Entry
	ErrVal   error
}

func (e *Envelope) OpID() int            { return e.OpIDVal }
func (e *Envelope) IsTerminal() bool     { return e.Terminal }
func (e *Envelope) Entry() *netsec.Entry { return e.EntryVal }
func (e *Envelope) Err() error           { return e.ErrVal }

// Conn is one scripted directory connection.
type Conn struct {
	service *Service
	id      int

	mu        sync.Mutex
	boundUser string
	boundTen  string
	closed    bool
	nextOpID  int

	// terminal holds the release gate for held search-done messages.
	terminal map[int]chan struct{}

	inbox     chan *Envelope
	closeOnce chan struct{}
}

// ID returns the per-service connection id.
func (c *Conn) ID() int { return c.id }

// BoundTenant returns the tenant the last successful bind selected, or "".
func (c *Conn) BoundTenant() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundTen
}

// BoundUser returns the username of the last successful bind, or "".
func (c *Conn) BoundUser() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundUser
}

// Bind implements netsec.Conn.
func (c *Conn) Bind(ctx context.Context, username, password string) error {
	rec := BindRecord{
		ConnID:   c.id,
		Username: username,
		Password: password,
		At:       time.Now(),
	}
	defer c.service.record(rec)

	// Anonymous bind: empty username. Allowed only as an explicit
	// anonymous bind; the lease layer refuses named/empty-password binds
	// before reaching here.
	if username == "" {
		c.mu.Lock()
		c.boundUser = ""
		c.boundTen = ""
		c.mu.Unlock()
		return nil
	}

	rule, ok := c.service.snapshotRule(username)
	if !ok {
		return ErrInvalidCredentials
	}
	if rule.Err != nil {
		return rule.Err
	}
	if rule.Password != password {
		return ErrInvalidCredentials
	}

	rec.Tenant = rule.Tenant
	c.mu.Lock()
	c.boundUser = username
	c.boundTen = rule.Tenant
	c.mu.Unlock()
	return nil
}

// WhoAmI simulates the LDAP WhoAmI extended operation used by the identity
// confirmation callback.
func (c *Conn) WhoAmI() (string, error) {
	c.mu.Lock()
	user := c.boundUser
	c.mu.Unlock()
	if user == "" {
		return "", nil
	}
	rule, ok := c.service.snapshotRule(user)
	if !ok {
		return "", nil
	}
	if rule.WhoAmIErr != nil {
		return "", rule.WhoAmIErr
	}
	if rule.WhoAmI != "" {
		return rule.WhoAmI, nil
	}
	return user, nil
}

// Send implements netsec.Conn: it schedules scripted entries and (possibly
// held) terminal messages for the search.
func (c *Conn) Send(ctx context.Context, req netsec.SearchRequest) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, ErrConnClosed
	}
	opID := c.nextOpID
	c.nextOpID++
	plan := c.service.snapshotPlan(req.Tenant.ID)
	gate := make(chan struct{})
	c.terminal[opID] = gate
	tenant := c.boundTen
	c.mu.Unlock()

	_ = tenant

	// Entries are stamped with the tenant they were produced under so
	// tests can assert that cross-tenant entries never get delivered.
	go func(opID int, plan SearchPlan, gate chan struct{}) {
		for _, entry := range plan.Entries {
			stamped := stampTenant(entry, plan.Tenant)
			c.push(&Envelope{OpIDVal: opID, EntryVal: stamped})
		}
		if plan.HoldTerminal {
			<-gate
		}
		c.push(&Envelope{OpIDVal: opID, Terminal: true})
	}(opID, plan, gate)

	return opID, nil
}

// ReleaseTerminal releases a held search-done message for opID.
func (c *Conn) ReleaseTerminal(opID int) {
	c.mu.Lock()
	gate := c.terminal[opID]
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}
}

// InjectLateEntry pushes an arbitrary entry envelope for opID without going
// through a search plan, simulating a late protocol message after rebind.
func (c *Conn) InjectLateEntry(opID int, entry *netsec.Entry) {
	c.push(&Envelope{OpIDVal: opID, EntryVal: entry})
}

// InjectTerminal pushes an explicit terminal envelope for opID.
func (c *Conn) InjectTerminal(opID int) {
	c.push(&Envelope{OpIDVal: opID, Terminal: true})
}

// Recv implements netsec.Conn.
func (c *Conn) Recv(ctx context.Context) (netsec.Envelope, error) {
	select {
	case env := <-c.inbox:
		return env, nil
	case <-c.closeOnce:
		// Drain anything queued before surfacing closure.
		select {
		case env := <-c.inbox:
			return env, nil
		default:
			return nil, ErrConnClosed
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close implements netsec.Conn.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closeOnce)
	c.mu.Unlock()
	return nil
}

// IsClosed reports whether Close has been called.
func (c *Conn) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Conn) push(env *Envelope) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	select {
	case c.inbox <- env:
	case <-c.closeOnce:
	}
}

func stampTenant(e *netsec.Entry, tenant string) *netsec.Entry {
	attrs := make(map[string][]string, len(e.Attributes)+1)
	for k, v := range e.Attributes {
		attrs[k] = v
	}
	attrs["x-scripted-tenant"] = []string{tenant}
	return &netsec.Entry{DN: e.DN, Attributes: attrs}
}

// EntryTenant returns the tenant stamped on an entry, if present.
func EntryTenant(e *netsec.Entry) string {
	if v, ok := e.Attributes["x-scripted-tenant"]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// Confirmer adapts Conn.WhoAmI into a netsec.IdentityConfirmer.
func Confirmer() netsec.IdentityConfirmerFunc {
	return func(ctx context.Context, c netsec.Conn) (string, error) {
		sc, ok := c.(*Conn)
		if !ok {
			return "", ErrInvalidCredentials
		}
		return sc.WhoAmI()
	}
}
