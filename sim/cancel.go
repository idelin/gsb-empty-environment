package sim

// ErrCanceled is reported to an attempt whose Ctl was canceled before it
// completed, and ErrDeadlineExceeded by a cancel triggered by the deadline.
type ErrCanceled struct{ Reason string }

func (e ErrCanceled) Error() string {
	if e.Reason == "" {
		return "sim: canceled"
	}
	return "sim: canceled: " + e.Reason
}

// Ctl is a cancellation handle forming a parent/child tree.
//
// Canceling a parent propagates synchronously to every live child, and each
// in-flight child runs its registered onCancel hook once (the transport uses
// it to record an aborted send). A Ctl is one-shot: Cancel after the first
// call is a no-op, which is why a late failure arriving after a success can
// never change the final state.
type Ctl struct {
	parent   *Ctl
	children map[*Ctl]struct{}
	onCancel func(reason string)
	canceled bool
	deadline bool
}

// RootCtl creates an uncanceled root handle.
func RootCtl(s *Sim) *Ctl {
	_ = s
	return &Ctl{children: map[*Ctl]struct{}{}}
}

// Child registers and returns a child handle. hook (may be nil) fires once if
// this child is canceled while still live.
func (c *Ctl) Child(hook func(reason string)) *Ctl {
	child := &Ctl{
		parent:   c,
		children: map[*Ctl]struct{}{},
		onCancel: hook,
	}
	if c.canceled {
		// Parent already canceled: the child can never send.
		child.Cancel(c.reason())
		return child
	}
	c.children[child] = struct{}{}
	return child
}

// Canceled reports whether this handle has fired.
func (c *Ctl) Canceled() bool { return c.canceled }

// DeadlineHit reports whether cancellation originated from the deadline timer.
func (c *Ctl) DeadlineHit() bool { return c.deadline }

func (c *Ctl) reason() string {
	if c.deadline {
		return "deadline exceeded"
	}
	return "parent canceled"
}

// Cancel propagates cancellation to the whole subtree exactly once.
func (c *Ctl) Cancel(reason string) {
	if c.canceled {
		return
	}
	c.canceled = true
	if reason == "deadline exceeded" {
		c.deadline = true
	}
	if c.parent != nil {
		delete(c.parent.children, c)
	}
	if c.onCancel != nil {
		hook := c.onCancel
		c.onCancel = nil
		hook(reason)
	}
	// Snapshot children: canceling one deletes it from the map.
	for child := range c.children {
		child.Cancel(reason)
	}
	c.children = nil
}
