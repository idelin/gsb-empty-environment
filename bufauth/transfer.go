package bufauth

import (
	"crypto/rand"
	"encoding/hex"
	"syscall"
)

// TransferState is the lifecycle state of a pending buffer handoff.
type TransferState uint8

const (
	// StatePending waits for the receiver to claim the token.
	StatePending TransferState = iota
	// StateClaimed was successfully delivered to a receiver.
	StateClaimed
	// StateReclaimed was canceled, usually by cascade revocation.
	StateReclaimed
)

func (s TransferState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateClaimed:
		return "claimed"
	case StateReclaimed:
		return "reclaimed"
	default:
		return "unknown"
	}
}

// Transfer is one ledger entry for a buffer file descriptor waiting to be
// handed over through a Unix domain socket.
type Transfer struct {
	token    string
	grant    *Grant
	bufferID string
	fd       int
	state    TransferState
}

// Token is the unguessable handle the receiver presents to claim the fd.
func (t *Transfer) Token() string {
	return t.token
}

// GrantID returns the derived grant governing this transfer.
func (t *Transfer) GrantID() int64 {
	return t.grant.id
}

// BufferID returns the buffer identifier captured when the transfer began.
func (t *Transfer) BufferID() string {
	return t.bufferID
}

// FD returns the duplicated file descriptor owned by the ledger entry. It
// remains valid until the transfer is claimed or reclaimed.
func (t *Transfer) FD() int {
	return t.fd
}

// State returns the current lifecycle state.
func (t *Transfer) State() TransferState {
	return t.state
}

func (t *Transfer) isPending() bool {
	return t.state == StatePending
}

func (t *Transfer) reclaim() {
	t.state = StateReclaimed
	// Closing the ledger-owned dup drops the local reference. An fd already
	// queued in a socket stays alive in kernel space until claimed or the
	// socket closes, but the token is revoked so Claim rejects it.
	_ = syscall.Close(t.fd)
}

// BeginTransfer registers a handoff of fd under grant. The authority dups
// the descriptor so revocation can reclaim the ledger copy independently of
// the caller's fd. The grant must be live, must hold PermRead and
// PermReexport, and fd must belong to bufferID.
func (a *Authority) BeginTransfer(grant *Grant, bufferID string, fd int) (*Transfer, error) {
	if err := a.Check(grant, PermRead); err != nil {
		return nil, err
	}
	if !grant.Perms().Contains(PermReexport) {
		return nil, ErrReexportDenied
	}
	if grant.BufferID() != bufferID {
		return nil, ErrBufferMismatch
	}
	dupFD, err := syscall.Dup(fd)
	if err != nil {
		return nil, ErrBadFD
	}
	syscall.CloseOnExec(dupFD)

	token, err := newToken()
	if err != nil {
		_ = syscall.Close(dupFD)
		return nil, err
	}

	transfer := &Transfer{
		token:    token,
		grant:    grant,
		bufferID: bufferID,
		fd:       dupFD,
		state:    StatePending,
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Re-check under the authority lock: a concurrent Revoke must win.
	grant.mu.RLock()
	live := grant.live
	grant.mu.RUnlock()
	if !live {
		_ = syscall.Close(dupFD)
		return nil, ErrGrantRevoked
	}
	a.transfers[token] = transfer
	return transfer, nil
}

// Claim completes a pending transfer and transfers ownership of its
// duplicated file descriptor to the caller. A reclaimed token, for example
// one whose grant was revoked while the message was in flight, is rejected
// even if the kernel still holds the descriptor in the socket buffer.
func (a *Authority) Claim(token string) (*Transfer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	transfer, ok := a.transfers[token]
	if !ok {
		return nil, ErrTransferGone
	}
	if !transfer.isPending() {
		return nil, ErrTransferGone
	}
	transfer.grant.mu.RLock()
	live := transfer.grant.live
	transfer.grant.mu.RUnlock()
	if !live {
		transfer.reclaim()
		delete(a.transfers, token)
		return nil, ErrGrantRevoked
	}
	transfer.state = StateClaimed
	delete(a.transfers, token)
	return transfer, nil
}

// PendingTransfer looks up a transfer without changing its state.
func (a *Authority) PendingTransfer(token string) (*Transfer, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	transfer, ok := a.transfers[token]
	return transfer, ok
}

func newToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
