package netsec

import "context"

// WorkFunc is the expensive backend operation for one selected backend. The
// context is canceled when the owning stream is reset, so the function should
// return promptly in that case.
type WorkFunc func(ctx context.Context, b Backend) error

// RunBackend drives one admitted stream's backend side through the state
// machine: starting (enumeration), working (the expensive call), then
// WorkFinished. It is safe against late callbacks: once the ticket has been
// fully released, every controller call becomes a no-op stale-ticket error.
//
// The caller owns the stream side and must call MarkReset or StreamEnd. This
// function always reclaims the work side exactly once, including when
// enumeration or work fails or is canceled.
func (c *Controller) RunBackend(ctx context.Context, t Ticket, en *Enumerator, work WorkFunc) error {
	if err := c.MarkBackendStarting(t); err != nil {
		return err
	}

	b, err := en.Next(ctx)
	if err != nil {
		// Enumeration never produced work; reclaim the work side. A stale
		// ticket means the entry was already fully released elsewhere.
		if ferr := c.WorkFinished(t); ferr != nil && ferr != ErrStaleTicket {
			return ferr
		}
		return err
	}

	if err := c.MarkBackendWorking(t); err != nil && err != ErrStaleTicket {
		return err
	}

	workCtx, cancelWork := c.ResetContext(ctx, t)
	workErr := work(workCtx, b)
	cancelWork()

	if ferr := c.WorkFinished(t); ferr != nil && ferr != ErrStaleTicket {
		return ferr
	}
	return workErr
}
