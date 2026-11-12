package netsec

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
)

type connResult struct {
	conn net.Conn
	err  error
}

// Transfer is a one-time, one-connection data channel instance.
//
// It binds exactly one approval (operation summary, account, mode and peer
// set) and accepts at most one matching data connection. Any other inbound
// connection is closed without receiving a body.
type Transfer struct {
	id      uint64
	epoch   uint64
	session *Session
	conn    Connector

	approval Approval

	mu            sync.Mutex
	state         State
	listener      DataListener
	opened        bool
	attached      bool
	attachTaken   bool
	attachStarted bool
	data          net.Conn
	terminal      bool
	terminalErr   error

	lifecycle context.Context
	cancel    context.CancelFunc

	result chan connResult
}

func newTransfer(id, epoch uint64, session *Session, conn Connector, approval Approval) *Transfer {
	lifecycle, cancel := context.WithCancel(context.Background())
	return &Transfer{
		id:        id,
		epoch:     epoch,
		session:   session,
		conn:      conn,
		approval:  approval,
		state:     Pending,
		lifecycle: lifecycle,
		cancel:    cancel,
		result:    make(chan connResult, 1),
	}
}

// Approval returns a copy of the approval bound to this instance.
func (t *Transfer) Approval() Approval {
	return t.approval.clone()
}

// Digest returns the fixed operation summary.
func (t *Transfer) Digest() Digest { return t.approval.Digest }

// Account returns the bound account.
func (t *Transfer) Account() string { return t.approval.Account }

// Mode returns the pinned channel mode.
func (t *Transfer) Mode() Mode { return t.approval.Mode }

// Epoch returns the authentication epoch under which the instance was minted.
func (t *Transfer) Epoch() uint64 { return t.epoch }

// ID returns the unique one-shot instance identifier within the session.
func (t *Transfer) ID() uint64 { return t.id }

// State returns the current lifecycle state.
func (t *Transfer) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Conn returns the attached data connection, or nil before attachment.
// It does not consume or duplicate the one-shot channel delivered by Attach.
func (t *Transfer) Conn() net.Conn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.data
}

// LocalAddrPort is the passive listener endpoint, valid after OpenPassive.
func (t *Transfer) LocalAddrPort() (netip.AddrPort, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener == nil {
		return netip.AddrPort{}, false
	}
	return t.listener.LocalAddrPort(), true
}

// OpenPassive arms the passive listener. It must match the approval mode and
// may only be called once per instance.
func (t *Transfer) OpenPassive(ctx context.Context) (netip.AddrPort, error) {
	if t.approval.Mode != Passive {
		return netip.AddrPort{}, ErrModeMismatch
	}

	t.mu.Lock()
	if t.terminal {
		err := t.terminalErr
		t.mu.Unlock()
		return netip.AddrPort{}, err
	}
	if t.opened {
		t.mu.Unlock()
		return netip.AddrPort{}, ErrTransferInUse
	}
	t.opened = true
	t.mu.Unlock()

	listener, addr, err := t.conn.Listen(ctx, netip.AddrPort{})
	if err != nil {
		t.terminate(err)
		return netip.AddrPort{}, err
	}

	t.mu.Lock()
	if t.terminal {
		t.mu.Unlock()
		_ = listener.Close()
		return netip.AddrPort{}, t.terminalErr
	}
	t.listener = listener
	t.mu.Unlock()

	go t.acceptLoop(listener)
	return addr, nil
}

// ConnectActive establishes the active data channel by dialing target.
// target must be a member of the approved peer set and the approval must pin
// Active mode. The single authorized connection is delivered through Attach.
func (t *Transfer) ConnectActive(ctx context.Context, target netip.AddrPort) error {
	if t.approval.Mode != Active {
		t.finish(Failed, ErrModeMismatch)
		return ErrModeMismatch
	}
	if !t.approval.allows(target) {
		t.finish(Failed, ErrPeerNotAllowed)
		return ErrPeerNotAllowed
	}

	t.mu.Lock()
	if t.terminal {
		err := t.terminalErr
		t.mu.Unlock()
		return err
	}
	if t.opened && t.attached {
		t.mu.Unlock()
		return ErrTransferInUse
	}
	t.opened = true
	t.mu.Unlock()

	dialCtx, stopDial := context.WithCancel(mergeContext(ctx, t.lifecycle))
	defer stopDial()

	conn, err := t.conn.Dial(dialCtx, target)
	if err != nil {
		if errors.Is(t.lifecycle.Err(), context.Canceled) {
			t.terminate(ErrTransferRevoked)
			return ErrTransferRevoked
		}
		t.finish(Failed, err)
		return err
	}

	t.mu.Lock()
	if t.terminal {
		t.mu.Unlock()
		_ = conn.Close()
		return t.terminalErr
	}
	t.data = conn
	t.attached = true
	t.state = Attached
	t.mu.Unlock()

	t.deliver(connResult{conn: conn})
	return nil
}

// Attach waits for and returns the single data connection bound to this
// instance. Only a connection whose remote endpoint matches the approved peer
// set and arrives while the instance is live is delivered; every other
// connection is closed by the listener and carries no body.
func (t *Transfer) Attach(ctx context.Context) (net.Conn, error) {
	t.mu.Lock()
	if t.terminal {
		err := t.terminalErr
		t.mu.Unlock()
		return nil, err
	}
	// Exactly one caller may consume the one-shot channel. If the data
	// connection already exists it is handed to that single caller; any
	// concurrent or later caller is refused.
	if t.attachTaken || t.attachStarted {
		t.mu.Unlock()
		return nil, ErrTransferInUse
	}
	t.attachStarted = true
	t.mu.Unlock()

	select {
	case res := <-t.result:
		if res.err != nil {
			return nil, res.err
		}
		t.mu.Lock()
		t.attachTaken = true
		t.mu.Unlock()
		return res.conn, nil
	case <-ctx.Done():
		// Hand-off may already have raced with this timeout; prefer the data.
		t.mu.Lock()
		if t.attached && t.data != nil {
			t.attachTaken = true
			data := t.data
			t.mu.Unlock()
			return data, nil
		}
		t.attachStarted = false
		t.mu.Unlock()
		return nil, ctx.Err()
	case <-t.lifecycle.Done():
		t.mu.Lock()
		if t.attached && t.data != nil {
			t.attachTaken = true
			data := t.data
			t.mu.Unlock()
			return data, nil
		}
		t.mu.Unlock()
		return nil, ErrTransferRevoked
	}
}

// Complete marks a finished transfer and releases its listener. The attached
// connection is owned by the caller and must be closed by the caller.
func (t *Transfer) Complete() error {
	return t.finish(Done, nil)
}

// Cancel revokes this instance and rejects any future or in-flight channel.
func (t *Transfer) Cancel() error {
	t.terminate(ErrTransferRevoked)
	return nil
}

func (t *Transfer) finish(state State, err error) error {
	t.mu.Lock()
	if t.terminal {
		prev := t.terminalErr
		t.mu.Unlock()
		return prev
	}
	t.terminal = true
	t.terminalErr = err
	t.state = state
	listener := t.listener
	t.listener = nil
	t.mu.Unlock()

	t.cancel()
	if listener != nil {
		_ = listener.Close()
	}
	if err != nil {
		t.deliver(connResult{err: err})
	}
	t.session.removeTransfer(t.id)
	return nil
}

// terminate revokes the instance and drains unmatched or late channels.
func (t *Transfer) terminate(err error) {
	t.mu.Lock()
	if t.terminal {
		t.mu.Unlock()
		return
	}
	t.terminal = true
	t.terminalErr = err
	if t.state != Attached {
		t.state = Canceled
	}
	listener := t.listener
	t.listener = nil
	t.mu.Unlock()

	t.cancel()
	if listener != nil {
		_ = listener.Close()
	}
	t.deliver(connResult{err: err})
	t.session.removeTransfer(t.id)
}

func (t *Transfer) acceptLoop(listener DataListener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		remote, ok := RemoteAddrPort(conn)
		if !ok || !t.approval.allows(remote) {
			// Unauthorized peer: close immediately so it receives a zero body.
			_ = conn.Close()
			continue
		}
		if !t.offer(conn) {
			_ = conn.Close()
		}
		return
	}
}

// offer delivers conn only while the instance is live and still unattached.
func (t *Transfer) offer(conn net.Conn) bool {
	t.mu.Lock()
	if t.terminal || t.attached {
		t.mu.Unlock()
		return false
	}
	t.attached = true
	t.data = conn
	t.state = Attached
	t.mu.Unlock()

	t.deliver(connResult{conn: conn})
	return true
}

func (t *Transfer) deliver(res connResult) {
	select {
	case t.result <- res:
	default:
	}
}
