package netsec

import (
	"context"
	"sync"
)

// Thread is the explicit host-thread simulation stack. It is not a goroutine
// local: the service passes the same *Thread through a request handler, which
// also makes the stack visible to deterministic schedulers and tests.
type Thread struct {
	mu sync.Mutex

	host    *Host
	primary *Token
	stack   []*Token
	bound   *Request
}

type threadState struct {
	stack []*Token
	bound *Request
}

func (t *Thread) ID() string {
	if t == nil || t.host == nil {
		return ""
	}
	return "thread"
}

// Bind associates the thread with the request it is currently serving. A
// thread may not switch to another active request while impersonation frames
// remain on its stack.
func (t *Thread) Bind(req *Request) error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	if req == nil || req.host != t.host {
		return ErrRequestMismatch
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.stack) != 0 {
		return ErrWrongRequest
	}
	t.host.mu.Lock()
	err := t.host.verifyRequestLocked(req)
	if err == nil {
		if t.bound != nil && t.bound != req && t.bound.active {
			err = ErrWrongRequest
		} else {
			t.bound = req
		}
	}
	t.host.mu.Unlock()
	return err
}

// Unbind releases a request binding. It refuses when impersonation frames are
// still present, because silently dropping them is the class of bug this
// component is designed to prevent.
func (t *Thread) Unbind(req *Request) error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.stack) != 0 {
		return ErrWrongRequest
	}
	if req != nil && t.bound != nil && t.bound != req {
		return ErrWrongRequest
	}
	t.bound = nil
	return nil
}

// Release unbinds whatever request the thread currently holds.
func (t *Thread) Release() error {
	return t.Unbind(nil)
}

func (t *Thread) BoundRequest() *Request {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bound
}

// Enter pushes a live token issued for the thread's current request. If the
// thread has no binding, the first Enter binds it to that token's request. A
// token from a different active request is rejected.
func (t *Thread) Enter(tok *Token) error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enterLocked(tok, nil)
}

// EnterFor is the strict form: it requires the caller to name the current
// request in addition to presenting the token.
func (t *Thread) EnterFor(req *Request, tok *Token) error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	if req == nil {
		return ErrNoCurrentRequest
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enterLocked(tok, req)
}

// EnterRequest is an alias for EnterFor.
func (t *Thread) EnterRequest(req *Request, tok *Token) error {
	return t.EnterFor(req, tok)
}

// enterLocked assumes t.mu is held.
func (t *Thread) enterLocked(tok *Token, explicit *Request) error {
	if tok == nil || tok.host != t.host {
		return ErrInvalidToken
	}
	if tok.primary {
		return ErrPrimaryToken
	}

	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	if err := t.host.verifyTokenLocked(tok); err != nil {
		return err
	}
	req := tok.request
	if explicit != nil && req != explicit {
		return ErrTokenRequestMismatch
	}
	if t.bound != nil && t.bound != req {
		if len(t.stack) == 0 && !t.bound.active {
			t.bound = req
		} else {
			return ErrWrongRequest
		}
	} else if t.bound == nil {
		t.bound = req
	}
	t.stack = append(t.stack, tok)
	return nil
}

// Exit pops exactly the top token. It deliberately does not implement a broad
// "revert to process" operation, so an inner scope cannot discard an outer
// impersonation frame.
func (t *Thread) Exit(tok *Token) error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.stack) == 0 {
		if tok != nil && tok.primary {
			return ErrPrimaryToken
		}
		return ErrNoImpersonation
	}
	top := t.stack[len(t.stack)-1]
	if !sameToken(top, tok) {
		return ErrStackMismatch
	}
	t.stack = t.stack[:len(t.stack)-1]
	return nil
}

func sameToken(a, b *Token) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || (a.host == b.host && a.id == b.id && a.request == b.request &&
		a.level == b.level && a.deadline == b.deadline && a.primary == b.primary)
}

// Current returns the effective token. The process primary token is returned
// when no client frame is present.
func (t *Thread) Current() *Token {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.stack) == 0 {
		return t.primary
	}
	return t.stack[len(t.stack)-1]
}

// CurrentValid revalidates the effective token against its request and host
// deadline.
func (t *Thread) CurrentValid() (*Token, error) {
	tok := t.Current()
	if tok == nil {
		return nil, ErrInvalidToken
	}
	if err := tok.Valid(); err != nil {
		return tok, err
	}
	return tok, nil
}

func (t *Thread) Depth() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.stack)
}

// Stack returns a bottom-to-top snapshot excluding the implicit primary base.
func (t *Thread) Stack() []Snapshot {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Snapshot, 0, len(t.stack))
	for _, tok := range t.stack {
		out = append(out, tok.Snapshot())
	}
	return out
}

func (t *Thread) accessBinding() (*Token, *Request) {
	if t == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tok := t.primary
	if len(t.stack) > 0 {
		tok = t.stack[len(t.stack)-1]
	}
	return tok, t.bound
}

func (t *Thread) saveStateLocked() threadState {
	return threadState{
		stack: append([]*Token(nil), t.stack...),
		bound: t.bound,
	}
}

func (t *Thread) restoreStateLocked(state threadState) {
	t.stack = append(t.stack[:0], state.stack...)
	t.bound = state.bound
}

// RunScoped enters tok, runs fn, and restores the exact prior stack whether fn
// returns, panics, or is canceled. The restore is a stack snapshot, not a
// RevertToSelf, so an outer impersonation frame survives an inner failure.
func (t *Thread) RunScoped(tok *Token, fn func() error) (err error) {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	t.mu.Lock()
	saved := t.saveStateLocked()
	if err := t.enterLocked(tok, nil); err != nil {
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.restoreStateLocked(saved)
		t.mu.Unlock()
		if recovered := recover(); recovered != nil {
			panic(recovered)
		}
	}()
	if fn == nil {
		return nil
	}
	return fn()
}

// RunScopedContext is RunScoped with cancellation. Cancellation is cooperative
// in Go; once fn returns (normally or via panic), the prior stack is restored
// before the cancellation error is returned.
func (t *Thread) RunScopedContext(
	ctx context.Context,
	tok *Token,
	fn func(context.Context) error,
) (err error) {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	t.mu.Lock()
	saved := t.saveStateLocked()
	if err := t.enterLocked(tok, nil); err != nil {
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.restoreStateLocked(saved)
		t.mu.Unlock()
		if recovered := recover(); recovered != nil {
			panic(recovered)
		}
	}()

	if fn != nil {
		err = fn(ctx)
	}
	if ctxErr := ctx.Err(); ctxErr != nil && err == nil {
		err = ctxErr
	}
	return err
}

func (t *Thread) WithToken(tok *Token, fn func()) error {
	return t.RunScoped(tok, func() error {
		if fn != nil {
			fn()
		}
		return nil
	})
}

// RevertToSelf is retained for source compatibility with service code that
// used the unsafe Windows-style primitive. It only succeeds when exactly one
// frame remains, otherwise it refuses rather than deleting an outer scope.
func (t *Thread) RevertToSelf() error {
	if t == nil || t.host == nil {
		return ErrDifferentHost
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch len(t.stack) {
	case 0:
		return nil
	case 1:
		t.stack = t.stack[:0]
		return nil
	default:
		return ErrNestedImpersonation
	}
}
