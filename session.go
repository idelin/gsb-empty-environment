package netsec

import "sync"

// Session is an authenticated FTP-style control session.
//
// Each authorization mints a fresh one-shot transfer instance. Revocation
// events (Cancel, Reauthenticate, Close) invalidate every live instance and
// never leave an old approval reusable.
type Session struct {
	conn Connector

	mu      sync.Mutex
	account string
	epoch   uint64
	nextID  uint64
	live    map[uint64]*Transfer
	closed  bool
}

// NewSession creates a session for account using conn as its transport.
func NewSession(account string, conn Connector) (*Session, error) {
	if account == "" || conn == nil {
		return nil, ErrInvalidApproval
	}
	return &Session{
		account: account,
		conn:    conn,
		live:    map[uint64]*Transfer{},
	}, nil
}

// Account returns the currently authenticated account.
func (s *Session) Account() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

// Epoch returns the current authentication epoch. Reauthenticate bumps it.
func (s *Session) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// Authorize binds approval to a brand-new one-shot transfer instance.
//
// The approval must belong to the current account and is consumed by the
// returned transfer; a later transfer requires a freshly issued approval.
func (s *Session) Authorize(approval Approval) (*Transfer, error) {
	if err := approval.validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrTransferRevoked
	}
	if approval.Account != s.account {
		return nil, ErrInvalidApproval
	}

	s.nextID++
	transfer := newTransfer(s.nextID, s.epoch, s, s.conn, approval.clone())
	s.live[transfer.id] = transfer
	return transfer, nil
}

func (s *Session) removeTransfer(id uint64) {
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}

// Cancel revokes every live transfer in the current authentication epoch.
func (s *Session) Cancel() {
	s.revokeAll(ErrTransferRevoked)
}

// Reauthenticate switches the session to a new account epoch. Every transfer
// minted under the previous authentication is revoked: its data channel can no
// longer attach and its approval cannot be reused.
func (s *Session) Reauthenticate(account string) error {
	if account == "" {
		return ErrInvalidApproval
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrTransferRevoked
	}
	s.epoch++
	s.account = account
	live := snapshotLive(s.live)
	s.mu.Unlock()

	for _, transfer := range live {
		transfer.terminate(ErrTransferRevoked)
	}
	return nil
}

// Close ends the session and revokes every outstanding transfer.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	live := snapshotLive(s.live)
	s.live = map[uint64]*Transfer{}
	s.mu.Unlock()

	for _, transfer := range live {
		transfer.terminate(ErrTransferRevoked)
	}
	return nil
}

func (s *Session) revokeAll(err error) {
	s.mu.Lock()
	live := snapshotLive(s.live)
	s.mu.Unlock()
	for _, transfer := range live {
		transfer.terminate(err)
	}
}

func snapshotLive(live map[uint64]*Transfer) []*Transfer {
	out := make([]*Transfer, 0, len(live))
	for _, transfer := range live {
		out = append(out, transfer)
	}
	return out
}

// State describes the lifecycle of a transfer instance.
type State int

const (
	// Pending means the instance is armed and waiting for its data channel.
	Pending State = iota
	// Attached means the single authorized connection has been delivered.
	Attached
	// Done means the transfer completed normally.
	Done
	// Canceled means the instance was revoked before/without completion.
	Canceled
	// Failed means the transport could not establish the channel.
	Failed
)

// Terminal reports whether the instance can no longer accept a channel.
func (st State) Terminal() bool {
	return st == Done || st == Canceled || st == Failed
}
