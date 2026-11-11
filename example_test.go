package authbackoff_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.org/authbackoff"
)

// passwordChecker stands in for the real credential verification backend.
type passwordChecker struct {
	secret string
	failed bool
}

func (c *passwordChecker) Verify(ctx context.Context, password string) error {
	if c.failed {
		return errors.New("directory unavailable")
	}
	if password != c.secret {
		return errBadPassword
	}
	return nil
}

var errBadPassword = errors.New("bad password")

// loginHandler wires the attempt lease into a request handler. The handler
// maps every verification path to exactly one lease outcome.
type loginHandler struct {
	leases *authbackoff.Manager
	users  map[string]*passwordChecker
}

func (h *loginHandler) Login(ctx context.Context, accountID, password string) error {
	lease, unlockAt, err := h.leases.Begin(accountID)
	if err != nil {
		if errors.Is(err, authbackoff.ErrAccountLocked) {
			return fmt.Errorf("%w: retry after %s", err, time.Until(unlockAt).Round(time.Second))
		}
		return err
	}

	done := make(chan error, 1)
	go func() { done <- h.users[accountID].Verify(ctx, password) }()

	select {
	case verifyErr := <-done:
		switch {
		case verifyErr == nil:
			err = lease.End(authbackoff.AuthSuccess)
		case errors.Is(verifyErr, errBadPassword):
			if endErr := lease.End(authbackoff.AuthFailure); endErr != nil {
				return endErr
			}
			return verifyErr
		default:
			if endErr := lease.End(authbackoff.SystemFault); endErr != nil {
				return endErr
			}
			return verifyErr
		}
	case <-ctx.Done():
		if endErr := lease.End(authbackoff.Timeout); endErr != nil {
			return endErr
		}
		return ctx.Err()
	}
	return err
}

func ExampleManager() {
	// In production the clock is omitted (nil); tests inject a fake clock.
	mgr := authbackoff.NewManager(authbackoff.Config{
		BaseBackoff: time.Second,
		MaxBackoff:  30 * time.Second,
		MaxFailures: 5,
	})
	handler := &loginHandler{
		leases: mgr,
		users: map[string]*passwordChecker{
			"alice": {secret: "correct horse battery staple"},
		},
	}
	ctx := context.Background()

	if err := handler.Login(ctx, "alice", "wrong"); err != nil {
		fmt.Println("attempt 1:", err)
	}
	if _, _, err := mgr.Begin("alice"); errors.Is(err, authbackoff.ErrAccountLocked) {
		fmt.Println("attempt 2: account locked")
	}

	// Output:
	// attempt 1: bad password
	// attempt 2: account locked
}
