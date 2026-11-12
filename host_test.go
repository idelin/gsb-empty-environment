package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeDriver struct {
	calls []string

	failShutdown error
	failRestore  error
	failOpen     error
	failClose    error

	dmaActive bool
	debugOpen bool
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{dmaActive: true}
}

func (d *fakeDriver) ShutdownSensitiveDMA() error {
	d.calls = append(d.calls, "ShutdownSensitiveDMA")
	if d.failShutdown != nil {
		return d.failShutdown
	}
	d.dmaActive = false
	return nil
}

func (d *fakeDriver) RestoreSensitiveDMA() error {
	d.calls = append(d.calls, "RestoreSensitiveDMA")
	if d.failRestore != nil {
		return d.failRestore
	}
	d.dmaActive = true
	return nil
}

func (d *fakeDriver) OpenDebug() error {
	d.calls = append(d.calls, "OpenDebug")
	if d.failOpen != nil {
		return d.failOpen
	}
	d.debugOpen = true
	return nil
}

func (d *fakeDriver) CloseDebug() error {
	d.calls = append(d.calls, "CloseDebug")
	if d.failClose != nil {
		return d.failClose
	}
	d.debugOpen = false
	return nil
}

func (d *fakeDriver) countCalls(name string) int {
	n := 0
	for _, c := range d.calls {
		if c == name {
			n++
		}
	}
	return n
}

var errDrv = errors.New("driver fault")

var testChallenge = []byte("boot-challenge-0372")

func newFixture() (*Host, *fakeClock, *fakeDriver) {
	clk := newFakeClock()
	drv := newFakeDriver()
	return NewHost(clk, drv, testChallenge), clk, drv
}

// assertInvariant fails the test if both permission classes are enabled at
// once, or if the host's bookkeeping disagrees with the driver.
func assertInvariant(t *testing.T, h *Host, drv *fakeDriver) {
	t.Helper()
	debug, sensitive := h.Permissions()
	if debug && sensitive {
		t.Fatalf("SAFETY VIOLATION: debug and sensitive tasks both enabled (state %v)", h.State())
	}
	if debug != drv.debugOpen || sensitive != drv.dmaActive {
		t.Fatalf("host/driver mismatch: host(debug=%v sensitive=%v) driver(debug=%v dma=%v)",
			debug, sensitive, drv.debugOpen, drv.dmaActive)
	}
}

func TestHappyPath(t *testing.T) {
	h, _, drv := newFixture()
	auth := h.IssueAuthorization(10 * time.Minute)

	if err := h.EnterMaintenance(auth); err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateEnteringMaintenance {
		t.Fatalf("state = %v, want EnteringMaintenance", h.State())
	}
	if debug, _ := h.Permissions(); debug {
		t.Fatal("debug opened before zeroization confirmation")
	}

	if err := h.HandleDMAZeroed(); err != nil {
		t.Fatalf("HandleDMAZeroed: %v", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateMaintenance {
		t.Fatalf("state = %v, want Maintenance", h.State())
	}

	if err := h.LeaveMaintenance(); err != nil {
		t.Fatalf("LeaveMaintenance: %v", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
	if _, sensitive := h.Permissions(); !sensitive {
		t.Fatal("sensitive tasks not restored after leaving maintenance")
	}
}

func TestAuthorizationNotExtendable(t *testing.T) {
	h, clk, _ := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	deadline := auth.Deadline()

	if _, err := auth.Extend(time.Hour); !errors.Is(err, ErrWindowNotExtendable) {
		t.Fatalf("Extend err = %v, want ErrWindowNotExtendable", err)
	}
	if !auth.Deadline().Equal(deadline) {
		t.Fatal("deadline changed after Extend attempt")
	}

	clk.Advance(2 * time.Minute)
	if err := h.EnterMaintenance(auth); !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("EnterMaintenance with lapsed window err = %v, want ErrWindowExpired", err)
	}
}

func TestEnterRejectsChallengeMismatch(t *testing.T) {
	h, _, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	auth.challenge = []byte("stale-challenge-from-previous-boot")

	if err := h.EnterMaintenance(auth); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("err = %v, want ErrChallengeMismatch", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
}

func TestWindowExpiresDuringMaintenance(t *testing.T) {
	h, clk, drv := newFixture()
	auth := h.IssueAuthorization(5 * time.Minute)
	mustEnterMaintenance(t, h, auth)

	clk.Advance(6 * time.Minute)
	if err := h.Tick(); !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("Tick err = %v, want ErrWindowExpired", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
	if debug, sensitive := h.Permissions(); debug || !sensitive {
		t.Fatalf("permissions = (debug=%v sensitive=%v), want (false true)", debug, sensitive)
	}
}

func TestWindowExpiresBeforeZeroConfirmation(t *testing.T) {
	h, clk, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	if err := h.EnterMaintenance(auth); err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	clk.Advance(2 * time.Minute)
	if err := h.HandleDMAZeroed(); !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("HandleDMAZeroed err = %v, want ErrWindowExpired", err)
	}
	assertInvariant(t, h, drv)
	if drv.countCalls("OpenDebug") != 0 {
		t.Fatal("debug opened after authorization window expired")
	}
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
}

func TestWindowExpiresWhileEntering(t *testing.T) {
	h, clk, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	if err := h.EnterMaintenance(auth); err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	clk.Advance(2 * time.Minute)
	if err := h.Tick(); !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("Tick err = %v, want ErrWindowExpired", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
	if drv.countCalls("OpenDebug") != 0 {
		t.Fatal("debug opened during aborted transition")
	}
}

func TestDuplicateZeroConfirmation(t *testing.T) {
	h, _, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	mustEnterMaintenance(t, h, auth)

	if err := h.HandleDMAZeroed(); err != nil {
		t.Fatalf("duplicate confirmation: %v", err)
	}
	if err := h.HandleDMAZeroed(); err != nil {
		t.Fatalf("stale confirmation after leaving: %v", err)
	}
	assertInvariant(t, h, drv)
	if got := drv.countCalls("OpenDebug"); got != 1 {
		t.Fatalf("OpenDebug called %d times, want 1", got)
	}
	if h.State() != StateMaintenance {
		t.Fatalf("state = %v, want Maintenance", h.State())
	}
}

func TestStaleZeroConfirmationInNormal(t *testing.T) {
	h, _, drv := newFixture()
	if err := h.HandleDMAZeroed(); err != nil {
		t.Fatalf("stale confirmation: %v", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
	if drv.countCalls("OpenDebug") != 0 {
		t.Fatal("stale confirmation opened debug")
	}
}

func TestOpenDebugFailureRollsBack(t *testing.T) {
	h, _, drv := newFixture()
	drv.failOpen = errDrv
	auth := h.IssueAuthorization(time.Minute)
	if err := h.EnterMaintenance(auth); err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	if err := h.HandleDMAZeroed(); !errors.Is(err, errDrv) {
		t.Fatalf("HandleDMAZeroed err = %v, want driver fault", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal after rollback", h.State())
	}
	if debug, sensitive := h.Permissions(); debug || !sensitive {
		t.Fatalf("permissions = (debug=%v sensitive=%v), want (false true)", debug, sensitive)
	}
}

func TestShutdownDMAFailureStaysNormal(t *testing.T) {
	h, _, drv := newFixture()
	drv.failShutdown = errDrv
	auth := h.IssueAuthorization(time.Minute)

	if err := h.EnterMaintenance(auth); !errors.Is(err, errDrv) {
		t.Fatalf("EnterMaintenance err = %v, want driver fault", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateNormal {
		t.Fatalf("state = %v, want Normal", h.State())
	}
	if drv.countCalls("OpenDebug") != 0 {
		t.Fatal("debug opened despite DMA shutdown failure")
	}
}

func TestCloseDebugFailureGoesFault(t *testing.T) {
	h, _, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	mustEnterMaintenance(t, h, auth)

	drv.failClose = errDrv
	if err := h.LeaveMaintenance(); !errors.Is(err, errDrv) {
		t.Fatalf("LeaveMaintenance err = %v, want driver fault", err)
	}
	if h.State() != StateFault {
		t.Fatalf("state = %v, want Fault", h.State())
	}
	// Debug close could not be confirmed: sensitive tasks must NOT be
	// re-enabled, otherwise both permission classes could be live.
	if _, sensitive := h.Permissions(); sensitive {
		t.Fatal("sensitive tasks enabled while debug close unconfirmed")
	}
	if drv.countCalls("RestoreSensitiveDMA") != 0 {
		t.Fatal("sensitive DMA restored while debug close unconfirmed")
	}
}

func TestRestoreFailureAfterDebugCloseGoesFault(t *testing.T) {
	h, _, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	mustEnterMaintenance(t, h, auth)

	drv.failRestore = errDrv
	if err := h.LeaveMaintenance(); !errors.Is(err, errDrv) {
		t.Fatalf("LeaveMaintenance err = %v, want driver fault", err)
	}
	assertInvariant(t, h, drv)
	if h.State() != StateFault {
		t.Fatalf("state = %v, want Fault", h.State())
	}
	if debug, sensitive := h.Permissions(); debug || sensitive {
		t.Fatalf("permissions = (debug=%v sensitive=%v), want (false false)", debug, sensitive)
	}
}

func TestExpiryTickCloseFailureGoesFault(t *testing.T) {
	h, clk, drv := newFixture()
	auth := h.IssueAuthorization(time.Minute)
	mustEnterMaintenance(t, h, auth)

	drv.failClose = errDrv
	clk.Advance(2 * time.Minute)
	if err := h.Tick(); !errors.Is(err, errDrv) {
		t.Fatalf("Tick err = %v, want driver fault", err)
	}
	if h.State() != StateFault {
		t.Fatalf("state = %v, want Fault", h.State())
	}
	if _, sensitive := h.Permissions(); sensitive {
		t.Fatal("sensitive tasks enabled while debug close unconfirmed")
	}
}

func TestLeaveFromNormalRejected(t *testing.T) {
	h, _, drv := newFixture()
	if err := h.LeaveMaintenance(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("LeaveMaintenance err = %v, want ErrInvalidState", err)
	}
	assertInvariant(t, h, drv)
}

func mustEnterMaintenance(t *testing.T, h *Host, auth Authorization) {
	t.Helper()
	if err := h.EnterMaintenance(auth); err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}
	if err := h.HandleDMAZeroed(); err != nil {
		t.Fatalf("HandleDMAZeroed: %v", err)
	}
	if h.State() != StateMaintenance {
		t.Fatalf("state = %v, want Maintenance", h.State())
	}
}
