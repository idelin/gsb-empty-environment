// Package writestall re-exports the WriteStall controller.
package writestall

import (
	"context"
	"time"

	root "prescreen.local/ps-b366051703"
)

type (
	State      = root.WriteStallState
	Config     = root.WriteStallConfig
	Snapshot   = root.WriteStallSnapshot
	Transition = root.StallTransition
	Controller = root.WriteStallController
	WriteStall = root.WriteStallController
)

const (
	StallNormal  = root.StallNormal
	StallDelayed = root.StallDelayed
	StallStopped = root.StallStopped

	Normal  = root.StallNormal
	Delayed = root.StallDelayed
	Stopped = root.StallStopped

	StateNormal  = root.StallNormal
	StateDelayed = root.StallDelayed
	StateStopped = root.StallStopped

	WriteStallNormal  = root.StallNormal
	WriteStallDelayed = root.StallDelayed
	WriteStallStopped = root.StallStopped

	WriteStallStateNormal  = root.StallNormal
	WriteStallStateDelayed = root.StallDelayed
	WriteStallStateStopped = root.StallStopped
)

var ErrClosed = root.ErrWriteStallClosed

func DefaultConfig() Config { return root.DefaultWriteStallConfig() }
func New(configs ...Config) *Controller {
	return root.NewWriteStallController(configs...)
}
func NewController(configs ...Config) *Controller {
	return root.NewWriteStallController(configs...)
}
func NewWriteStallController(configs ...Config) *Controller {
	return root.NewWriteStallController(configs...)
}
func NewWriteStall(configs ...Config) *Controller {
	return root.NewWriteStall(configs...)
}

// Wait is a small package-level convenience for callers that only need the
// admission check.
func Wait(ctx context.Context, c *Controller) error {
	if c == nil {
		return nil
	}
	return c.WaitForWrite(ctx)
}

// Delay returns the controller's soft-limit delay.
func Delay(c *Controller) time.Duration {
	if c == nil {
		return 0
	}
	return c.Delay()
}
