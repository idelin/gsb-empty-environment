package netsec

import (
	"errors"
	"io"
	"sync"
)

// Transport is the byte channel toward the embedded host. Implementations
// are expected to honor the ordinary io.Writer contract, including short
// writes where Write returns (n < len(p), nil).
type Transport interface {
	io.ReadWriter
	io.Closer
}

// Logger receives the redacted, detailed view of each failure for local
// diagnostics. Log entries never contain secret values and never leave the
// process through the public error path.
type Logger interface {
	Log(entry LogEntry)
}

// LogEntry is the sanitized diagnostic record handed to the Logger exactly
// once per reported failure.
type LogEntry struct {
	// CorrelationID matches the reference carried by the public error, so
	// host-side reports can be correlated with local diagnostics.
	CorrelationID string
	// InternalCategory is the detailed in-process failure stage.
	InternalCategory string
	// PublicCategory is the allow-listed class sent to the host.
	PublicCategory PublicCategory
	// Message is the operator-facing internal description.
	Message string
	// SensitiveFields lists secret field names; every value is replaced by
	// the constant redaction marker.
	SensitiveFields map[string]any
	// Cause is the optional underlying error text.
	Cause string
}

// FailureCallback is invoked exactly once per reported failure with the
// fixed public error. It must never be called with the internal error.
type FailureCallback func(pub PublicError)

// ErrAlreadyReported is returned by Report when a failure has already been
// converged on this guard, so callers can distinguish the no-op path.
var ErrAlreadyReported = errors.New("netsec: failure already reported")

// Guard owns one host-facing connection and enforces the boundary
// guarantees: at most one failure is reported, each failure triggers at
// most one log entry and one callback, and Close runs at most once.
type Guard struct {
	transport Transport
	logger    Logger
	onFailure FailureCallback

	closeOnce sync.Once
	closeErr  error

	reportMu sync.Mutex
	reported bool
}

// Option configures a Guard at construction.
type Option func(*Guard)

// WithLogger attaches the diagnostic logger.
func WithLogger(logger Logger) Option {
	return func(g *Guard) { g.logger = logger }
}

// WithFailureCallback attaches the host notification callback.
func WithFailureCallback(cb FailureCallback) Option {
	return func(g *Guard) { g.onFailure = cb }
}

// NewGuard wraps transport with the error-convergence boundary.
func NewGuard(transport Transport, opts ...Option) *Guard {
	g := &Guard{transport: transport}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Report converts an internal failure to the fixed public shape, emits the
// constant-length frame on the transport, and dispatches the redacted log
// entry and failure callback. Concurrent or repeated calls after the first
// are no-ops and return the zero PublicError plus ErrAlreadyReported, so
// logging and the callback each run exactly once even under races.
func (g *Guard) Report(err error) (PublicError, error) {
	if err == nil {
		return PublicError{}, errors.New("netsec: Report requires a non-nil error")
	}

	g.reportMu.Lock()
	if g.reported {
		g.reportMu.Unlock()
		return PublicError{}, ErrAlreadyReported
	}
	g.reported = true
	g.reportMu.Unlock()

	pub := Publicize(err)

	// Frame write happens before notification, but a transport error must
	// not suppress the single log entry or callback.
	_ = g.writeFrame(pub)

	g.dispatchLog(err, pub)
	if g.onFailure != nil {
		g.onFailure(pub)
	}
	return pub, nil
}

// Run executes handshake with the guard's guarantees. A nil result is
// returned as success; a non-nil result is reported exactly once through
// Report and returned as the fixed PublicError.
func (g *Guard) Run(handshake func() error) (PublicError, error) {
	if err := handshake(); err != nil {
		return g.Report(err)
	}
	return PublicError{}, nil
}

// Close releases the underlying transport at most once. Concurrent and
// repeated calls converge on the result of the single underlying Close.
func (g *Guard) Close() error {
	g.closeOnce.Do(func() {
		g.closeErr = g.transport.Close()
	})
	return g.closeErr
}

func (g *Guard) dispatchLog(err error, pub PublicError) {
	if g.logger == nil {
		return
	}
	entry := LogEntry{
		CorrelationID:   pub.CorrelationID,
		PublicCategory:  pub.Category,
		SensitiveFields: map[string]any{},
	}
	var detailed *internalError
	if errors.As(err, &detailed) {
		entry.InternalCategory = string(detailed.stage)
		entry.Message = detailed.message
		entry.SensitiveFields = detailed.redactedFields()
		if detailed.underlying != nil {
			entry.Cause = detailed.underlying.Error()
		}
	} else {
		entry.InternalCategory = string(stageUnknown)
	}
	g.logger.Log(entry)
}

// writeFull tolerates short writes by looping until the whole frame is
// consumed or the transport returns an error.
func (g *Guard) writeFull(frame []byte) error {
	for len(frame) > 0 {
		n, err := g.transport.Write(frame)
		if n > 0 {
			frame = frame[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (g *Guard) writeFrame(pub PublicError) error {
	frame, err := pub.MarshalText()
	if err != nil {
		return err
	}
	return g.writeFull(frame)
}
