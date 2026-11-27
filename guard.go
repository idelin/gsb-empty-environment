package streamguard

import (
	"errors"
	"fmt"
)

// Guard combines safe incremental framing with the strict StartTLS state
// machine. All bytes read ahead from the network should be supplied to Write
// or Feed so UpgradeTLS can discard them atomically.
type Guard struct {
	cfg     Config
	machine *Machine
	reader  *FrameReader

	failed         error
	discardedBytes int
}

// NewGuard creates a secure XML stream guard.
func NewGuard(cfgs ...Config) *Guard {
	cfg := DefaultConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0].normalize()
	}
	return &Guard{
		cfg:     cfg,
		machine: NewMachine(cfg),
		reader:  NewFrameReader(cfg.Limits),
	}
}

// Machine exposes the underlying state machine.
func (g *Guard) Machine() *Machine { return g.machine }

// State returns the current protocol state.
func (g *Guard) State() State { return g.machine.State() }

// IsTLS reports whether the guard has passed the StartTLS upgrade point.
func (g *Guard) IsTLS() bool { return g.machine.IsTLS() }

// Buffered returns the number of unprocessed bytes in the guarded read-ahead
// buffer.
func (g *Guard) Buffered() int { return g.reader.Buffered() }

// DiscardedBytes returns bytes discarded by StartTLS buffer resets.
func (g *Guard) DiscardedBytes() int { return g.discardedBytes }

// Err returns the terminal security/protocol error.
func (g *Guard) Err() error { return g.failed }

// Write appends raw bytes to the guarded incremental buffer.
func (g *Guard) Write(data []byte) (int, error) {
	if g.failed != nil {
		return 0, g.failed
	}
	n, err := g.reader.Write(data)
	if err != nil {
		g.fail(err)
		return n, err
	}
	return n, nil
}

// ReadFrame parses and validates exactly one next frame. ErrNeedMore means
// more bytes are required. After a StartTLS frame, further reads return
// ErrTLSUpgradePending until UpgradeTLS/ResetBufferAndStream is called.
func (g *Guard) ReadFrame() (*Frame, error) {
	if g.failed != nil {
		return nil, g.failed
	}
	if g.machine.State() == StateStartTLS {
		return nil, ErrTLSUpgradePending
	}

	frame, err := g.reader.NextFrame()
	if err != nil {
		if !errors.Is(err, ErrNeedMore) {
			g.fail(err)
		}
		return nil, err
	}

	if err := g.machine.Observe(frame); err != nil {
		g.fail(err)
		return frame, err
	}
	return frame, nil
}

// Feed appends a chunk and returns currently available validated frames. It
// deliberately stops after StartTLS, leaving any pipelined/smuggled bytes
// unparsed so UpgradeTLS can discard them.
func (g *Guard) Feed(data []byte) ([]*Frame, error) {
	if _, err := g.Write(data); err != nil {
		return nil, err
	}

	var frames []*Frame
	for {
		frame, err := g.ReadFrame()
		if frame != nil {
			frames = append(frames, frame)
		}
		if errors.Is(err, ErrNeedMore) {
			return frames, nil
		}
		if err != nil {
			return frames, err
		}
		if frame != nil && frame.Kind == FrameStartTLS {
			return frames, nil
		}
	}
}

// UpgradeTLS transitions to ResetBufferAndStream and discards every unprocessed
// plaintext byte. Call this immediately before handing the connection to a
// TLS handshake.
func (g *Guard) UpgradeTLS() error {
	if g.failed != nil {
		return g.failed
	}
	if g.machine.State() != StateStartTLS {
		return fmt.Errorf("%w: cannot upgrade from %s", ErrInvalidState, g.machine.State())
	}

	discarded := g.reader.Buffered()
	g.reader.Reset()
	g.discardedBytes += discarded

	if err := g.machine.UpgradeTLS(); err != nil {
		return err
	}
	return nil
}

// ResetBufferAndStream is the explicitly named StartTLS defense operation.
func (g *Guard) ResetBufferAndStream() error {
	return g.UpgradeTLS()
}

// SASLSuccess forwards SASL authentication completion to the state machine.
func (g *Guard) SASLSuccess() error {
	if g.failed != nil {
		return g.failed
	}
	return g.machine.SASLSuccess()
}

// BindSuccess forwards resource-binding completion.
func (g *Guard) BindSuccess() error {
	if g.failed != nil {
		return g.failed
	}
	return g.machine.BindSuccess()
}

// Close marks the guard closed.
func (g *Guard) Close() error {
	if g.failed != nil {
		return g.failed
	}
	return g.machine.Close()
}

func (g *Guard) fail(err error) {
	if g.failed == nil {
		g.failed = err
	}
}
