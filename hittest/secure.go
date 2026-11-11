package hittest

import (
	"errors"
	"sync"
)

// ClickStatus explains how a press or release was resolved.
type ClickStatus int

const (
	// ClickNone means no gesture was active or the event did not start a
	// gesture.
	ClickNone ClickStatus = iota
	// ClickArmed means a press was accepted and an authorization is pending
	// release.
	ClickArmed
	// ClickAuthorized means press and release matched the same receiver on
	// the same unchanged scene version.
	ClickAuthorized
	// ClickCanceledNoHit means the event did not land on any surface.
	ClickCanceledNoHit
	// ClickCanceledInsecure means the event landed on a surface that is not
	// a marked security confirmation control.
	ClickCanceledInsecure
	// ClickCanceledStale means the scene changed between press and release,
	// so the captured snapshot no longer represents the live hierarchy.
	ClickCanceledStale
	// ClickCanceledMoved means the release resolved to a different receiver
	// than the press (including another window or no receiver at all).
	ClickCanceledMoved
)

// String returns a stable diagnostic name for the status.
func (c ClickStatus) String() string {
	switch c {
	case ClickArmed:
		return "armed"
	case ClickAuthorized:
		return "authorized"
	case ClickCanceledNoHit:
		return "canceled-no-hit"
	case ClickCanceledInsecure:
		return "canceled-insecure"
	case ClickCanceledStale:
		return "canceled-stale"
	case ClickCanceledMoved:
		return "canceled-moved"
	default:
		return "none"
	}
}

// ErrNoGesture is returned by Release when no press is armed.
var ErrNoGesture = errors.New("hittest: no armed secure click")

// PressResult describes the outcome of a pointer press.
type PressResult struct {
	Status ClickStatus
	Hit    Hit
}

// ReleaseResult describes the outcome of a pointer release.
type ReleaseResult struct {
	Status ClickStatus
	// Hit is the receiver resolved at release time; it is zero when no node
	// received the release.
	Hit Hit
}

// SecureClick arbitrates one security-sensitive click against a live scene.
//
// The security rule is intentionally strict: authorization requires the
// press and release to resolve to the same node through the same immutable
// scene snapshot. Any scene mutation between the two events advances the
// version and forces cancellation, preventing clickjacking through overlays
// inserted or re-raised while the button is held.
//
// SecureClick is safe for concurrent use; each public method takes the mutex
// and scene mutations are externally serialized through *Scene.
type SecureClick struct {
	mu       sync.Mutex
	scene    *Scene
	snapshot *Snapshot
	receiver string
	press    Point
	armed    bool
}

// NewSecureClick binds a click arbiter to a live scene.
func NewSecureClick(scene *Scene) *SecureClick {
	return &SecureClick{scene: scene}
}

// Press records a pointer press at screen point p. Only a press landing on a
// node flagged Secure arms an authorization; every other press terminates
// immediately with a canceled status.
func (c *SecureClick) Press(p Point) PressResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A new press always replaces any half-finished gesture.
	c.reset()

	snap := c.scene.Snapshot()
	hit, ok := snap.HitTest(p)
	if !ok {
		return PressResult{Status: ClickCanceledNoHit}
	}
	if !hit.Node.Secure {
		return PressResult{Status: ClickCanceledInsecure, Hit: hit}
	}

	c.armed = true
	c.snapshot = snap
	c.receiver = hit.Node.ID
	c.press = p
	return PressResult{Status: ClickArmed, Hit: hit}
}

// Release resolves a pointer release at screen point p against the snapshot
// captured at press time. The gesture authorizes only when the scene is still
// at that exact version and the release reaches the same node. The gesture
// is consumed regardless of outcome.
func (c *SecureClick) Release(p Point) (ReleaseResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.armed {
		return ReleaseResult{Status: ClickNone}, ErrNoGesture
	}
	defer c.reset()

	// Any mutation since the press bumps the pending version past the
	// captured one. Re-capturing here would accept a hierarchy swap, so the
	// gesture must fail closed.
	if c.scene.Version() != c.snapshot.Version() {
		return ReleaseResult{Status: ClickCanceledStale}, nil
	}

	hit, ok := c.snapshot.HitTest(p)
	if !ok {
		return ReleaseResult{Status: ClickCanceledMoved}, nil
	}
	if hit.Node.ID != c.receiver {
		return ReleaseResult{Status: ClickCanceledMoved, Hit: hit}, nil
	}
	if !hit.Visible.Contains(p) || !hit.Visible.Contains(c.press) {
		return ReleaseResult{Status: ClickCanceledMoved, Hit: hit}, nil
	}
	return ReleaseResult{Status: ClickAuthorized, Hit: hit}, nil
}

// Cancel aborts any armed gesture, for example on pointer capture loss or an
// explicit compositor cancel event.
func (c *SecureClick) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset()
}

// Armed reports whether a press is currently awaiting release.
func (c *SecureClick) Armed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed
}

// Snapshot returns the immutable snapshot captured at press time, or nil when
// no gesture is armed.
func (c *SecureClick) Snapshot() *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot
}

func (c *SecureClick) reset() {
	c.armed = false
	c.snapshot = nil
	c.receiver = ""
	c.press = Point{}
}
