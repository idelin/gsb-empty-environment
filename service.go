package netsec

import (
	"context"
	"sync"
)

// CallOutcome records which identity executed which request payload. It is
// the ground truth used by tests and by the trace search to detect identity
// confusion.
type CallOutcome struct {
	InstanceID uint64
	RequestID  uint64
	Identity   Identity
	Payload    []byte
}

// Service is the privileged IPC host. All externally visible failures are
// collapsed to ErrRequestFailed.
type Service struct {
	host *Host

	mu   sync.Mutex
	seen map[seenKey]struct{}

	// testHook, when set, runs inside Call after the context check. Tests
	// use it to force a panic on the sensitive path.
	testHook func()
}

type seenKey struct {
	instanceID uint64
	generation uint64
	requestID  uint64
}

func NewService(h *Host) *Service {
	return &Service{host: h, seen: make(map[seenKey]struct{})}
}

func (s *Service) markSeen(instanceID, generation, requestID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := seenKey{instanceID, generation, requestID}
	if _, dup := s.seen[k]; dup {
		return false
	}
	s.seen[k] = struct{}{}
	return true
}

// Transaction is one in-flight request on the secure service, split into
// steps so a controllable client can act between them.
type Transaction struct {
	svc          *Service
	pipe         *PipeInstance
	req          *Request
	snap         ConnectionSnapshot
	handle       *ImpersonationHandle
	impersonated bool
	reverted     bool
}

// Begin performs ReadRequest and rejects duplicate request ids within the
// same connection generation.
func (s *Service) Begin(p *PipeInstance) (*Transaction, error) {
	req, snap, err := p.ReadRequest()
	if err != nil {
		return nil, Opaque(err)
	}
	if !s.markSeen(snap.InstanceID, snap.Generation, req.ID()) {
		return nil, Opaque(errDuplicateRequest)
	}
	return &Transaction{svc: s, pipe: p, req: req, snap: snap}, nil
}

// Impersonate validates the snapshot captured at Begin against the live
// connection at the linearization point inside the pipe.
func (t *Transaction) Impersonate() error {
	h, err := t.pipe.Impersonate(t.snap)
	if err != nil {
		return Opaque(err)
	}
	t.handle = h
	t.impersonated = true
	return nil
}

// Call re-validates the one-shot handle immediately before performing the
// sensitive operation.
func (t *Transaction) Call(ctx context.Context) (CallOutcome, error) {
	if !t.impersonated || t.handle == nil {
		return CallOutcome{}, Opaque(errProtocolState)
	}
	if err := ctx.Err(); err != nil {
		return CallOutcome{}, Opaque(errCanceled)
	}
	if t.svc.testHook != nil {
		t.svc.testHook()
	}
	id, err := t.pipe.activeIdentity(t.handle)
	if err != nil {
		return CallOutcome{}, Opaque(err)
	}
	return CallOutcome{
		InstanceID: t.snap.InstanceID,
		RequestID:  t.req.ID(),
		Identity:   id,
		Payload:    t.req.Bytes(),
	}, nil
}

// Revert ends the impersonation. The next request is only processed after
// Revert has succeeded.
func (t *Transaction) Revert() error {
	if t.reverted {
		return Opaque(errProtocolState)
	}
	t.reverted = true
	return Opaque(t.pipe.Revert())
}

// ServeOnce runs the full ReadRequest -> Impersonate -> Call -> Revert
// cycle. Revert runs even when Call panics, so the instance always returns
// to Connected before the next request is handled.
func (s *Service) ServeOnce(ctx context.Context, p *PipeInstance) (outcome CallOutcome, err error) {
	t, err := s.Begin(p)
	if err != nil {
		return CallOutcome{}, err
	}
	if err := t.Impersonate(); err != nil {
		return CallOutcome{}, err
	}
	defer func() {
		if rerr := t.pipe.Revert(); rerr != nil && err == nil {
			err = Opaque(rerr)
			outcome = CallOutcome{}
		}
	}()
	return t.Call(ctx)
}
