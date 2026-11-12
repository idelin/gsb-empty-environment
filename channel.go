package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"sync"
)

// AuthRequest describes a host-side EXTERNAL AUTHENTICATE.
//
// InitialChainingValue is the chaining value derived during INITIALIZE UPDATE.
// It is used on the first authentication only; re-authentication continues with
// the chaining value already held by the channel.
type AuthRequest struct {
	Level                SecurityLevel
	InitialChainingValue []byte
	Counter              uint32
	APDU                 []byte
	MAC                  []byte
}

// Channel is the secure-channel state used by an APDU dispatcher. It owns the
// negotiated security level, MAC chaining value, and command counter.
//
// A Channel is safe for concurrent use. The injected MACFunc is called while
// the channel lock is held, so it must not call back into the channel.
type Channel struct {
	mu         sync.Mutex
	dispatchMu sync.Mutex

	mac MACFunc

	level         SecurityLevel
	authenticated bool
	invalid       *invalidChannelError

	chaining []byte
	next     uint32
	last     uint32
	commands uint64
}

// State is an alias kept for hosts that call the object a secure-channel state.
type State = Channel

// NewChannel creates an unauthenticated secure-channel state with an
// caller-injected MAC implementation.
func NewChannel(mac MACFunc) *Channel {
	return &Channel{mac: mac}
}

// New is an alias for NewChannel.
func New(mac MACFunc) *Channel {
	return NewChannel(mac)
}

// NewState is an alias for NewChannel.
func NewState(mac MACFunc) *State {
	return NewChannel(mac)
}

// SetMACFunc installs the MAC implementation. It is only permitted before the
// channel has been authenticated; changing it afterwards would invalidate the
// chaining relationship between commands.
func (c *Channel) SetMACFunc(mac MACFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.invalid != nil {
		return c.invalid
	}
	if c.authenticated {
		return errors.New("netsec: cannot replace MAC function on an authenticated channel")
	}
	if mac == nil {
		return ErrNoMACFunction
	}
	c.mac = mac
	return nil
}

// ExternalAuthenticate applies an EXTERNAL AUTHENTICATE selected security
// level. The request MAC is checked before any state is changed. On an
// authenticated channel the level may only gain bits; callers cannot reduce
// C-MAC or C-DECRYPTION.
func (c *Channel) ExternalAuthenticate(req AuthRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.usableError(); err != nil {
		return err
	}
	if !req.Level.Valid() {
		return fmt.Errorf("%w: requested %s", ErrInvalidSecurityLevel, req.Level)
	}
	if c.authenticated && !req.Level.Has(c.level) {
		return fmt.Errorf("%w: requested %s, current %s", ErrSecurityLevelDowngrade, req.Level, c.level)
	}
	if !req.Level.HasMAC() {
		return fmt.Errorf("%w: requested %s without C-MAC", ErrInvalidSecurityLevel, req.Level)
	}
	if c.mac == nil {
		return c.invalidate(ErrNoMACFunction)
	}
	if _, err := commandINS(req.APDU); err != nil {
		return c.invalidate(fmt.Errorf("%w: EXTERNAL AUTHENTICATE", err))
	}

	chaining := c.chaining
	if !c.authenticated {
		chaining = cloneBytes(req.InitialChainingValue)
	} else if err := c.checkCounter(req.Counter); err != nil {
		return err
	}

	computed, err := c.computeMAC(req.Counter, chaining, req.APDU)
	if err != nil {
		return c.invalidate(err)
	}
	if !equalMAC(computed, req.MAC) {
		return c.invalidate(ErrMACMismatch)
	}

	c.level = req.Level
	c.authenticated = true
	c.chaining = cloneBytes(computed)
	c.last = req.Counter
	c.commands++
	if req.Counter == math.MaxUint32 {
		c.next = 0
		c.invalidate(ErrCounterExhausted)
		return nil
	}
	c.next = req.Counter + 1
	return nil
}

// Authenticate is an alias for ExternalAuthenticate.
func (c *Channel) Authenticate(req AuthRequest) error {
	return c.ExternalAuthenticate(req)
}

// VerifyCommand authenticates a command APDU with C-MAC protection and
// advances the MAC chaining value and counter exactly once. A bad MAC, a
// replayed counter, a skipped counter, a callback error, or malformed input
// permanently invalidates the channel.
//
// The counter must be the immediate successor of the preceding accepted
// command. This makes an old C-MAC command unusable even if its MAC is
// otherwise valid.
func (c *Channel) VerifyCommand(counter uint32, apdu, receivedMAC []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.usableError(); err != nil {
		return err
	}
	if !c.authenticated {
		return ErrNotAuthenticated
	}
	if !c.level.HasMAC() {
		return c.invalidate(fmt.Errorf("%w: command has no C-MAC protection", ErrInsufficientSecurity))
	}
	if c.mac == nil {
		return c.invalidate(ErrNoMACFunction)
	}
	ins, err := commandINS(apdu)
	if err != nil {
		return c.invalidate(fmt.Errorf("%w: command APDU", err))
	}
	if err := c.checkCounter(counter); err != nil {
		return err
	}

	computed, err := c.computeMAC(counter, c.chaining, apdu)
	if err != nil {
		return c.invalidate(err)
	}
	if !equalMAC(computed, receivedMAC) {
		return c.invalidate(ErrMACMismatch)
	}

	c.chaining = cloneBytes(computed)
	c.last = counter
	c.commands++
	if counter == math.MaxUint32 {
		c.next = 0
		c.invalidate(ErrCounterExhausted)
	} else {
		c.next = counter + 1
	}

	if RequiresDualProtection(ins) && !c.level.Has(LevelFull) {
		return fmt.Errorf("%w: INS 0x%02x requires C-MAC+C-DECRYPTION", ErrInsufficientSecurity, ins)
	}
	return nil
}

// Verify is an alias for VerifyCommand.
func (c *Channel) Verify(counter uint32, apdu, receivedMAC []byte) error {
	return c.VerifyCommand(counter, apdu, receivedMAC)
}

// Dispatch verifies a command and then invokes the APDU handler. If the handler
// fails, the secure channel is invalidated because the host can no longer
// assume that the command sequence completed successfully.
func (c *Channel) Dispatch(counter uint32, apdu, receivedMAC []byte, handler func() error) error {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()

	if err := c.VerifyCommand(counter, apdu, receivedMAC); err != nil {
		return err
	}
	if handler == nil {
		return nil
	}
	if err := handler(); err != nil {
		return c.Abort(err)
	}
	return nil
}

// Abort permanently invalidates the channel. The returned error matches both
// ErrChannelInvalid and the supplied cause with errors.Is.
func (c *Channel) Abort(cause error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cause == nil {
		cause = errors.New("netsec: command processing aborted")
	}
	return c.invalidate(cause)
}

// SecurityLevel returns the currently negotiated security level.
func (c *Channel) SecurityLevel() SecurityLevel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.level
}

// Level is an alias for SecurityLevel.
func (c *Channel) Level() SecurityLevel {
	return c.SecurityLevel()
}

// Authenticated reports whether EXTERNAL AUTHENTICATE succeeded and the
// channel has not subsequently been invalidated.
func (c *Channel) Authenticated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authenticated && c.invalid == nil
}

// Established is an alias for Authenticated.
func (c *Channel) Established() bool {
	return c.Authenticated()
}

// Invalidated reports whether the channel has been permanently closed.
func (c *Channel) Invalidated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalid != nil
}

// Err returns the reason the channel was invalidated, or nil.
func (c *Channel) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invalid == nil {
		return nil
	}
	return c.invalid
}

// NextCounter returns the only counter accepted by the next command.
func (c *Channel) NextCounter() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next
}

// LastCounter returns the most recently accepted counter.
func (c *Channel) LastCounter() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Counter is an alias for LastCounter.
func (c *Channel) Counter() uint32 {
	return c.LastCounter()
}

// CommandCount returns the number of accepted authenticated commands.
func (c *Channel) CommandCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commands
}

// ChainingValue returns a copy of the current MAC chaining value.
func (c *Channel) ChainingValue() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneBytes(c.chaining)
}

// MACChainingValue is an alias for ChainingValue.
func (c *Channel) MACChainingValue() []byte {
	return c.ChainingValue()
}

func (c *Channel) usableError() error {
	if c.invalid != nil {
		return c.invalid
	}
	return nil
}

func (c *Channel) invalidate(cause error) error {
	if c.invalid == nil {
		c.invalid = &invalidChannelError{cause: cause}
	}
	return c.invalid
}

func (c *Channel) checkCounter(got uint32) error {
	if got == c.next {
		return nil
	}

	mismatch := ErrCounterGap
	if got < c.next {
		mismatch = ErrCounterRollback
	}
	return c.invalidate(fmt.Errorf("%w: got %d, want %d", mismatch, got, c.next))
}

func (c *Channel) computeMAC(counter uint32, chaining, apdu []byte) (result []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("%w: panic: %v", ErrMACGeneration, recovered)
		}
	}()

	result, err = c.mac(counter, cloneBytes(chaining), cloneBytes(apdu))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMACGeneration, err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: empty MAC", ErrMACGeneration)
	}
	return cloneBytes(result), nil
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

func equalMAC(left, right []byte) bool {
	if len(left) != len(right) || len(left) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(left, right) == 1
}

// EqualMAC exposes the constant-time comparison used for verification. It is
// useful when a host also needs to validate an EXTERNAL AUTHENTICATE response.
func EqualMAC(left, right []byte) bool {
	return equalMAC(left, right)
}
