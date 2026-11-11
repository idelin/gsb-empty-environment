package netsec

import (
	"crypto/rand"
	"errors"
	"sort"
	"sync"
	"time"
)

// candidate holds the validation state machine for one path.
type candidate struct {
	path      Path
	seq       int
	deadline  time.Time
	nonces    map[Nonce]struct{}
	timer     Timer
	validated bool
}

// Validator runs path validation for one transport host. A Validator is safe
// for concurrent use.
type Validator struct {
	mu         sync.Mutex
	conn       ConnID
	clock      Clock
	interval   time.Duration
	nonceSrc   NonceSource
	send       SendFunc
	strategy   Strategy
	onEvent    func(Event)
	candidates map[Path]*candidate
	active     *Path
	nextSeq    int
	closed     bool
}

// NewValidator constructs a validator from cfg.
func NewValidator(cfg Config) (*Validator, error) {
	if cfg.Send == nil {
		return nil, errSendRequired
	}
	clock := cfg.Clock
	if clock == nil {
		clock = wallClock{}
	}
	interval := cfg.RetryInterval
	if interval <= 0 {
		interval = DefaultRetryInterval
	}
	nonceSrc := cfg.NonceSource
	if nonceSrc == nil {
		nonceSrc = randomNonce
	}
	strategy := cfg.Strategy
	if strategy == nil {
		strategy = latestStrategy{}
	}
	return &Validator{
		conn:       cfg.Conn,
		clock:      clock,
		interval:   interval,
		nonceSrc:   nonceSrc,
		send:       cfg.Send,
		strategy:   strategy,
		onEvent:    cfg.OnEvent,
		candidates: make(map[Path]*candidate),
	}, nil
}

var errSendRequired = errors.New("netsec: Config.Send is required")

// randomNonce produces an 8-byte cryptographically random challenge nonce.
func randomNonce() (Nonce, error) {
	var n Nonce
	_, err := rand.Read(n[:])
	return n, err
}

// latestStrategy selects the candidate registered last, mirroring a host that
// prefers the path it most recently started migrating onto.
type latestStrategy struct{}

func (latestStrategy) Select(validated []Path) (Path, bool) {
	if len(validated) == 0 {
		return Path{}, false
	}
	return validated[len(validated)-1], true
}

// actions collects side effects to run after releasing the validator lock so
// external callbacks can never deadlock against the validator.
type actions []func()

func (a *actions) add(fn func()) { *a = append(*a, fn) }

func (a actions) run() {
	for _, fn := range a {
		fn()
	}
}

// AddCandidate registers an unvalidated candidate bound to the current
// connection instance and immediately sends its first challenge. The deadline
// is absolute and fixed: later retries never extend it.
func (v *Validator) AddCandidate(path Path, deadline time.Time) error {
	v.mu.Lock()
	var acts actions
	err := v.addCandidateLocked(path, deadline, &acts)
	v.mu.Unlock()
	acts.run()
	return err
}

func (v *Validator) addCandidateLocked(path Path, deadline time.Time, acts *actions) error {
	if v.closed {
		return ErrClosed
	}
	if path.Conn != v.conn {
		return ErrStaleConnection
	}
	if _, exists := v.candidates[path]; exists {
		return ErrDuplicateCandidate
	}
	if !deadline.After(v.clock.Now()) {
		return ErrInvalidDeadline
	}
	c := &candidate{
		path:     path,
		seq:      v.nextSeq,
		deadline: deadline,
		nonces:   make(map[Nonce]struct{}),
	}
	v.nextSeq++
	if err := v.issueChallengeLocked(c, acts); err != nil {
		return err
	}
	v.candidates[path] = c
	v.scheduleLocked(c)
	return nil
}

// issueChallengeLocked mints a unique nonce, records it as outstanding on the
// candidate, and queues a probe send on the candidate's own addresses.
func (v *Validator) issueChallengeLocked(c *candidate, acts *actions) error {
	n, err := v.nonceSrc()
	if err != nil {
		return err
	}
	for attempts := 0; attempts < 8; attempts++ {
		collision := false
		for _, other := range v.candidates {
			if _, ok := other.nonces[n]; ok {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
		if n, err = v.nonceSrc(); err != nil {
			return err
		}
	}
	c.nonces[n] = struct{}{}
	path, nonce := c.path, n
	acts.add(func() {
		// Transient send errors are retried by the timer; the fixed deadline
		// still applies, so a flapping socket cannot extend validation.
		_ = v.send(path, nonce)
	})
	return nil
}

// scheduleLocked arms the single retry/deadline timer. The fire time is clamped
// to the deadline, which is never modified.
func (v *Validator) scheduleLocked(c *candidate) {
	now := v.clock.Now()
	fireAt := now.Add(v.interval)
	if fireAt.After(c.deadline) {
		fireAt = c.deadline
	}
	key := c.path
	c.timer = v.clock.AfterFunc(fireAt.Sub(now), func() {
		v.onTimer(key)
	})
}

// onTimer either retries the challenge or fails the candidate once the fixed
// deadline has elapsed.
func (v *Validator) onTimer(key Path) {
	v.mu.Lock()
	var acts actions
	c, ok := v.candidates[key]
	if ok && !c.validated {
		c.timer = nil
		if !v.clock.Now().Before(c.deadline) {
			v.failLocked(c, ReasonDeadline, &acts)
		} else if err := v.issueChallengeLocked(c, &acts); err != nil {
			v.failLocked(c, ReasonNonceSource, &acts)
		} else {
			v.scheduleLocked(c)
		}
	}
	v.mu.Unlock()
	acts.run()
}

// failLocked removes an unvalidated candidate and emits EventFailed.
func (v *Validator) failLocked(c *candidate, reason FailureReason, acts *actions) {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	delete(v.candidates, c.path)
	path := c.path
	acts.add(func() {
		v.emit(Event{Kind: EventFailed, Path: path, Reason: reason})
	})
}

func (v *Validator) emit(ev Event) {
	if v.onEvent != nil {
		v.onEvent(ev)
	}
}

// validatedPathsLocked returns validated candidates in registration order.
func (v *Validator) validatedPathsLocked() []Path {
	var out []Path
	for _, c := range v.candidates {
		if c.validated {
			out = append(out, c.path)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return v.candidates[out[i]].seq < v.candidates[out[j]].seq
	})
	return out
}
