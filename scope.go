package netsec

import "context"

// Scope binds one Thread to one authenticated Request. It is the importable
// component a service handler uses; Enter, Exit, access checks, and async
// spawning all remain constrained to that request.
type Scope struct {
	host    *Host
	thread  *Thread
	request *Request
}

// NewScope binds thread to req. A nil thread creates a fresh host thread.
func (h *Host) NewScope(req *Request, thread *Thread) (*Scope, error) {
	if h == nil {
		return nil, ErrDifferentHost
	}
	if req == nil || req.host != h {
		return nil, ErrRequestMismatch
	}
	if thread == nil {
		thread = h.NewThread()
	} else if thread.host != h {
		return nil, ErrDifferentHost
	}
	if thread.BoundRequest() != req {
		if err := thread.Bind(req); err != nil {
			return nil, err
		}
	}
	return &Scope{host: h, thread: thread, request: req}, nil
}

func (h *Host) ScopeFor(req *Request) (*Scope, error) {
	return h.NewScope(req, nil)
}

// Scope is an alias for NewScope.
func (h *Host) Scope(req *Request, thread *Thread) (*Scope, error) {
	return h.NewScope(req, thread)
}

func (s *Scope) Host() *Host {
	if s == nil {
		return nil
	}
	return s.host
}

func (s *Scope) Thread() *Thread {
	if s == nil {
		return nil
	}
	return s.thread
}

func (s *Scope) Request() *Request {
	if s == nil {
		return nil
	}
	return s.request
}

// Enter pushes a token only if it belongs to this scope's request.
func (s *Scope) Enter(tok *Token) error {
	if s == nil || s.thread == nil {
		return ErrNoCurrentRequest
	}
	return s.thread.EnterFor(s.request, tok)
}

// Exit pops exactly tok. It cannot be used to pop an outer frame.
func (s *Scope) Exit(tok *Token) error {
	if s == nil || s.thread == nil {
		return ErrNoCurrentRequest
	}
	if tok == nil || tok.request != s.request {
		return ErrTokenRequestMismatch
	}
	return s.thread.Exit(tok)
}

func (s *Scope) RunScoped(tok *Token, fn func() error) error {
	if s == nil || s.thread == nil {
		return ErrNoCurrentRequest
	}
	if tok == nil || tok.request != s.request {
		return ErrTokenRequestMismatch
	}
	return s.thread.RunScoped(tok, fn)
}

func (s *Scope) RunScopedContext(
	ctx context.Context,
	tok *Token,
	fn func(context.Context) error,
) error {
	if s == nil || s.thread == nil {
		return ErrNoCurrentRequest
	}
	if tok == nil || tok.request != s.request {
		return ErrTokenRequestMismatch
	}
	return s.thread.RunScopedContext(ctx, tok, fn)
}

func (s *Scope) Current() *Token {
	if s == nil || s.thread == nil {
		return nil
	}
	return s.thread.Current()
}

func (s *Scope) CheckAccess(target ResourceKind) error {
	if s == nil {
		return ErrNoCurrentRequest
	}
	return s.host.CheckThreadAccess(s.thread, target)
}

func (s *Scope) Access(target ResourceKind, call func() error) error {
	if err := s.CheckAccess(target); err != nil {
		return err
	}
	if call == nil {
		return nil
	}
	return call()
}

// SpawnAsync is the request- and thread-strict spawn form for this scope.
func (s *Scope) SpawnAsync(
	policy SpawnPolicy,
	fn AsyncFunc,
	options ...SpawnOption,
) (*Task, error) {
	if s == nil {
		return nil, ErrNoCurrentRequest
	}
	return s.host.SpawnAsyncOn(s.thread, s.request, policy, fn, options...)
}

// Release drops the request binding. It refuses while impersonation frames
// remain on the thread.
func (s *Scope) Release() error {
	if s == nil || s.thread == nil {
		return ErrNoCurrentRequest
	}
	return s.thread.Unbind(s.request)
}
