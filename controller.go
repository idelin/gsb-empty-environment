package handoff

import "crypto/ed25519"

// Controller is the stateful online entry point for a route-control host.
type Controller struct {
	Plan        *Plan
	Book        *ReceiptBook
	cutExecuted bool
}

// NewController builds the safe handoff plan and trusted receipt book.
func NewController(spec Spec, keys map[EgressID]ed25519.PublicKey) (*Controller, error) {
	plan, err := BuildSafePlan(spec)
	if err != nil {
		return nil, err
	}
	return &Controller{Plan: plan, Book: NewReceiptBook(keys)}, nil
}

// Observe verifies and records one signed egress receipt.
func (c *Controller) Observe(receipt SignedReceipt) (bool, error) {
	return c.Book.Observe(receipt)
}

// NextBatch returns the next safe action. It never skips an ACK barrier.
func (c *Controller) NextBatch() Batch {
	if c.cutExecuted {
		return Batch{Phase: PhaseDone, Done: true}
	}
	return c.Plan.NextBatch(c.Book)
}

// MarkCutExecuted records that the host applied Batch.Cut to actual authz.
func (c *Controller) MarkCutExecuted() {
	if c.Plan.CurrentPhase(c.Book) == PhaseCut {
		c.cutExecuted = true
	}
}

// MissingAcks returns the current exact-digest obligations.
func (c *Controller) MissingAcks() []AckRequirement {
	return c.Plan.MissingAcks(c.Book)
}
