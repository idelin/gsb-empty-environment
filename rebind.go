package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type rebindRecord struct {
	request RebindRequest
}

// RequestRebind starts a review of a complete terminal rebind. The new
// merchant, acquirer, account, and currency must already be contracted
// together. The current binding remains effective until the request is
// approved and its delay has elapsed.
func (a *Attributor) RequestRebind(
	terminalID string,
	proposed TerminalBinding,
	requestedBy string,
	options ...RebindOptions,
) (RebindRequest, error) {
	terminalID, ok := normalizedID(terminalID)
	if !ok {
		return RebindRequest{}, fmt.Errorf("%w: terminal id", ErrInvalidID)
	}
	requester, ok := normalizedID(requestedBy)
	if !ok {
		return RebindRequest{}, ErrRequesterRequired
	}
	if proposed.TerminalID != "" && proposed.TerminalID != terminalID {
		return RebindRequest{}, fmt.Errorf("%w: proposed terminal id differs from path", ErrInvalidRequest)
	}
	proposed.TerminalID = terminalID
	proposed, err := a.normalizeBinding(proposed)
	if err != nil {
		return RebindRequest{}, err
	}
	var reason string
	for _, option := range options {
		if option.Reason != "" {
			reason = option.Reason
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	terminal, exists := a.terminals[terminalID]
	if !exists {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrUnsignedTerminal, terminalID)
	}
	now := a.cfg.clock()
	a.applyDueRebindLocked(terminal, now)
	if !terminal.active {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrInactiveTerminal, terminalID)
	}
	if terminal.pending != nil {
		return RebindRequest{}, fmt.Errorf(
			"%w: terminal %q has request %q in state %s",
			ErrRebindPending, terminalID, terminal.pending.request.ID, terminal.pending.request.Status,
		)
	}
	if sameBinding(terminal.binding, proposed) {
		return RebindRequest{}, ErrRebindNoChange
	}
	if err := a.validateBindingLocked(proposed); err != nil {
		return RebindRequest{}, fmt.Errorf("%w: %w", ErrRebindAccountInvalid, err)
	}

	id, err := newRebindID()
	if err != nil {
		return RebindRequest{}, err
	}
	request := RebindRequest{
		ID:          id,
		TerminalID:  terminalID,
		Current:     terminal.binding,
		Proposed:    proposed,
		Status:      RebindPendingReview,
		RequestedBy: requester,
		RequestedAt: now,
		Reason:      reason,
	}
	record := &rebindRecord{request: request}
	a.rebinds[id] = record
	terminal.pending = record
	return request, nil
}

// ProposeRebind is an alias for RequestRebind.
func (a *Attributor) ProposeRebind(
	terminalID string,
	proposed TerminalBinding,
	requestedBy string,
	options ...RebindOptions,
) (RebindRequest, error) {
	return a.RequestRebind(terminalID, proposed, requestedBy, options...)
}

// ApproveRebind approves a pending request and starts the mandatory activation
// delay. The old binding remains effective until EffectiveAt.
func (a *Attributor) ApproveRebind(id, reviewer string) (RebindRequest, error) {
	reviewer, ok := normalizedID(reviewer)
	if !ok {
		return RebindRequest{}, ErrReviewerRequired
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	record, ok := a.rebinds[id]
	if !ok {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrRebindNotFound, id)
	}
	if record.request.Status != RebindPendingReview {
		return RebindRequest{}, fmt.Errorf(
			"%w: %q is %s", ErrRebindNotPending, id, record.request.Status,
		)
	}
	if err := a.validateBindingLocked(record.request.Proposed); err != nil {
		return RebindRequest{}, fmt.Errorf("%w: %w", ErrRebindAccountInvalid, err)
	}

	now := a.cfg.clock()
	record.request.Status = RebindApproved
	record.request.ReviewedBy = reviewer
	record.request.ReviewedAt = now
	record.request.EffectiveAt = now.Add(a.cfg.rebindDelay)
	return record.request, nil
}

// RejectRebind rejects a pending request. The current terminal binding is not
// changed.
func (a *Attributor) RejectRebind(id, reviewer, reason string) (RebindRequest, error) {
	reviewer, ok := normalizedID(reviewer)
	if !ok {
		return RebindRequest{}, ErrReviewerRequired
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	record, ok := a.rebinds[id]
	if !ok {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrRebindNotFound, id)
	}
	if record.request.Status != RebindPendingReview {
		return RebindRequest{}, fmt.Errorf(
			"%w: %q is %s", ErrRebindNotPending, id, record.request.Status,
		)
	}

	record.request.Status = RebindRejected
	record.request.ReviewedBy = reviewer
	record.request.ReviewedAt = a.cfg.clock()
	record.request.Reason = reason
	if terminal, ok := a.terminals[record.request.TerminalID]; ok && terminal.pending == record {
		terminal.pending = nil
	}
	return record.request, nil
}

// CancelRebind cancels a request that has not activated yet. An approved
// request may be cancelled during its waiting period.
func (a *Attributor) CancelRebind(id, actor string) (RebindRequest, error) {
	actor, ok := normalizedID(actor)
	if !ok {
		return RebindRequest{}, ErrRequesterRequired
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	record, ok := a.rebinds[id]
	if !ok {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrRebindNotFound, id)
	}
	now := a.cfg.clock()
	if terminal, ok := a.terminals[record.request.TerminalID]; ok {
		a.applyDueRebindLocked(terminal, now)
	}
	if record.request.Status != RebindPendingReview && record.request.Status != RebindApproved {
		return RebindRequest{}, fmt.Errorf(
			"%w: %q is %s", ErrRebindNotPending, id, record.request.Status,
		)
	}

	record.request.Status = RebindCancelled
	record.request.ReviewedBy = actor
	record.request.ReviewedAt = now
	if terminal, ok := a.terminals[record.request.TerminalID]; ok && terminal.pending == record {
		terminal.pending = nil
	}
	return record.request, nil
}

// Rebind returns an audit copy of a rebind request, applying any due effective
// change first.
func (a *Attributor) Rebind(id string) (RebindRequest, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	record, ok := a.rebinds[id]
	if !ok {
		return RebindRequest{}, fmt.Errorf("%w: %q", ErrRebindNotFound, id)
	}
	now := a.cfg.clock()
	if terminal, ok := a.terminals[record.request.TerminalID]; ok {
		a.applyDueRebindLocked(terminal, now)
	}
	return record.request, nil
}

// PendingRebind returns a terminal's pending review request, if any.
func (a *Attributor) PendingRebind(terminalID string) (RebindRequest, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	terminal, ok := a.terminals[terminalID]
	if !ok {
		return RebindRequest{}, false
	}
	a.applyDueRebindLocked(terminal, a.cfg.clock())
	if terminal.pending == nil {
		return RebindRequest{}, false
	}
	return terminal.pending.request, true
}

func (a *Attributor) applyDueRebindLocked(terminal *terminalRecord, now time.Time) {
	pending := terminal.pending
	if pending == nil || pending.request.Status != RebindApproved {
		return
	}
	if now.Before(pending.request.EffectiveAt) {
		return
	}

	terminal.binding = pending.request.Proposed
	terminal.pending = nil
	pending.request.Status = RebindActive
	pending.request.ActivatedAt = now
}

func newRebindID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate rebind id: %w", err)
	}
	return "rb_" + hex.EncodeToString(raw[:]), nil
}
