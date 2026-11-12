package netsec

import (
	"errors"
	"testing"
)

func openTestManager(t *testing.T, dir string, opts ...Option) *Manager {
	t.Helper()
	m, err := NewManager(dir, opts...)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return m
}

func mustCreate(t *testing.T, m *Manager, id string, size int64, cred string) {
	t.Helper()
	if err := m.CreateRegion(id, size, cred); err != nil {
		t.Fatalf("CreateRegion(%q): %v", id, err)
	}
}

func mustStatus(t *testing.T, m *Manager, id string) Status {
	t.Helper()
	st, err := m.Status(id)
	if err != nil {
		t.Fatalf("Status(%q): %v", id, err)
	}
	return st
}

func checkPattern(t *testing.T, data []byte, size int64, pattern byte) {
	t.Helper()
	if int64(len(data)) != size {
		t.Fatalf("data length %d, want %d", len(data), size)
	}
	for i, b := range data {
		if b != pattern {
			t.Fatalf("byte %d is 0x%02x, want 0x%02x", i, b, pattern)
		}
	}
}

func TestLifecycleOnlyCleanAllocatable(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0xA5))

	const size = 2 * StripeSize
	mustCreate(t, m, "r1", size, "s3cret")
	st := mustStatus(t, m, "r1")
	if st.State != StateClean {
		t.Fatalf("new region state = %s, want clean", st.State)
	}
	if st.TotalStripes != 2 {
		t.Fatalf("total stripes = %d, want 2", st.TotalStripes)
	}
	for i := int64(0); i < 2; i++ {
		if n := mem.WriteCount("r1", i); n == 0 {
			t.Fatalf("provisioning did not overwrite stripe %d", i)
		}
	}
	checkPattern(t, mem.Data("r1"), size, 0xA5)

	lease, err := m.Allocate("r1", "tenant-a")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	st = mustStatus(t, m, "r1")
	if st.State != StateInUse || st.Tenant != "tenant-a" {
		t.Fatalf("after allocate: state=%s tenant=%q", st.State, st.Tenant)
	}
	if _, err := m.Allocate("r1", "tenant-b"); !errors.Is(err, ErrNotAllocatable) {
		t.Fatalf("second Allocate error = %v, want %v", err, ErrNotAllocatable)
	}

	if err := m.Release("r1", "wrong"); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong credential error = %v", err)
	}
	st = mustStatus(t, m, "r1")
	if st.State != StateInUse || st.FailedAuthAttempts != 1 {
		t.Fatalf("after wrong credential: state=%s failures=%d", st.State, st.FailedAuthAttempts)
	}
	if err := m.ReleaseLease(lease, "s3cret"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	st = mustStatus(t, m, "r1")
	if st.State != StateClean || st.Tenant != "" {
		t.Fatalf("after erase: state=%s tenant=%q", st.State, st.Tenant)
	}
	checkPattern(t, mem.Data("r1"), size, 0xA5)
	for i := int64(0); i < 2; i++ {
		if n := mem.WriteCount("r1", i); n < 2 {
			t.Fatalf("stripe %d written %d times, want at least 2", i, n)
		}
	}

	lease2, err := m.Allocate("r1", "tenant-b")
	if err != nil {
		t.Fatalf("allocate after clean: %v", err)
	}
	if err := m.ReleaseLease(lease, "s3cret"); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("stale lease error = %v, want %v", err, ErrLeaseMismatch)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateInUse {
		t.Fatalf("stale lease changed state to %s", st.State)
	}
	if err := m.ReleaseLease(lease2, "s3cret"); err != nil {
		t.Fatalf("correct lease release: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean {
		t.Fatalf("final state = %s, want clean", st.State)
	}
}

func TestBeginEraseBlocksAllocationUntilResumed(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0x5A))
	const size = 3 * StripeSize
	mustCreate(t, m, "r1", size, "cred")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if err := m.BeginErase("r1", "cred"); err != nil {
		t.Fatalf("BeginErase: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateErasing || st.Allocatable() {
		t.Fatalf("state=%s allocatable=%v", st.State, st.Allocatable())
	}
	if _, err := m.Allocate("r1", "attacker"); !errors.Is(err, ErrNotAllocatable) {
		t.Fatalf("Allocate during erasing = %v", err)
	}
	if err := m.ResumeErase("r1"); err != nil {
		t.Fatalf("ResumeErase: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateClean || st.VerifiedStripes != 0 {
		t.Fatalf("resumed state=%s verified=%d", st.State, st.VerifiedStripes)
	}
	checkPattern(t, mem.Data("r1"), size, 0x5A)
}

func TestCredentialLockoutUntilRestart(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemDevice()
	m := openTestManager(t, dir, WithDevice(mem), WithPattern(0x33), WithMaxAuthFailures(3))
	mustCreate(t, m, "r1", StripeSize, "right")
	if _, err := m.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	for i := 1; i <= 2; i++ {
		err := m.Release("r1", "wrong")
		if !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("failure %d error = %v", i, err)
		}
		st := mustStatus(t, m, "r1")
		if st.Locked || st.FailedAuthAttempts != i {
			t.Fatalf("failure %d: locked=%v count=%d", i, st.Locked, st.FailedAuthAttempts)
		}
	}
	err := m.Release("r1", "wrong")
	if !errors.Is(err, ErrRegionLocked) {
		t.Fatalf("third failure error = %v, want locked", err)
	}
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("third failure does not retain authentication error: %v", err)
	}
	st := mustStatus(t, m, "r1")
	if !st.Locked || st.State != StateInUse {
		t.Fatalf("after lock: locked=%v state=%s", st.Locked, st.State)
	}

	// The correct credential must not thaw the region before restart.
	if err := m.Release("r1", "right"); !errors.Is(err, ErrRegionLocked) {
		t.Fatalf("correct credential while locked error = %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.State != StateInUse || st.VerifiedStripes != 0 {
		t.Fatalf("locked release changed state=%s verified=%d", st.State, st.VerifiedStripes)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted := openTestManager(t, dir, WithDevice(mem), WithPattern(0x33), WithMaxAuthFailures(3))
	st = mustStatus(t, restarted, "r1")
	if st.Locked || st.FailedAuthAttempts != 0 || st.State != StateInUse {
		t.Fatalf("after restart: locked=%v failures=%d state=%s", st.Locked, st.FailedAuthAttempts, st.State)
	}
	if err := restarted.Release("r1", "right"); err != nil {
		t.Fatalf("release after restart: %v", err)
	}
	if st := mustStatus(t, restarted, "r1"); st.State != StateClean {
		t.Fatalf("state after restarted release = %s", st.State)
	}
}

func TestSuccessfulAuthenticationResetsConsecutiveFailures(t *testing.T) {
	dir := t.TempDir()
	m := openTestManager(t, dir, WithMaxAuthFailures(3))
	mustCreate(t, m, "r1", StripeSize, "right")
	if _, err := m.Allocate("r1"); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := m.Authorize("r1", "wrong"); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("wrong authorize %d: %v", i, err)
		}
	}
	if err := m.Authorize("r1", "right"); err != nil {
		t.Fatalf("correct authorize: %v", err)
	}
	if st := mustStatus(t, m, "r1"); st.FailedAuthAttempts != 0 {
		t.Fatalf("failures after success = %d", st.FailedAuthAttempts)
	}
	for i := 0; i < 2; i++ {
		if err := m.Authorize("r1", "wrong"); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("post-reset wrong authorize %d: %v", i, err)
		}
	}
	if err := m.Authorize("r1", "wrong"); !errors.Is(err, ErrRegionLocked) {
		t.Fatalf("third post-reset failure = %v", err)
	}
}

func TestLockedCleanRegionIsFrozenUntilRestart(t *testing.T) {
	dir := t.TempDir()
	m := openTestManager(t, dir, WithMaxAuthFailures(2))
	mustCreate(t, m, "r1", StripeSize, "right")
	for i := 0; i < 2; i++ {
		err := m.Authorize("r1", "wrong")
		if i == 0 && !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("first failure = %v", err)
		}
		if i == 1 && !errors.Is(err, ErrRegionLocked) {
			t.Fatalf("second failure = %v", err)
		}
	}
	st := mustStatus(t, m, "r1")
	if !st.Locked || st.State != StateClean {
		t.Fatalf("locked=%v state=%s", st.Locked, st.State)
	}
	if _, err := m.Allocate("r1", "tenant"); !errors.Is(err, ErrRegionLocked) {
		t.Fatalf("Allocate while frozen = %v", err)
	}
	if err := m.Authorize("r1", "right"); !errors.Is(err, ErrRegionLocked) {
		t.Fatalf("correct credential while frozen = %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restarted := openTestManager(t, dir, WithMaxAuthFailures(2))
	if st := mustStatus(t, restarted, "r1"); st.Locked {
		t.Fatalf("lock survived restart")
	}
	if _, err := restarted.Allocate("r1", "tenant"); err != nil {
		t.Fatalf("Allocate after restart: %v", err)
	}
}

func TestInvalidRegionsRejected(t *testing.T) {
	m := openTestManager(t, t.TempDir())
	if err := m.CreateRegion("", StripeSize, "c"); !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("empty ID error = %v", err)
	}
	if err := m.CreateRegion("r", StripeSize+1, "c"); !errors.Is(err, ErrInvalidRegion) {
		t.Fatalf("misaligned size error = %v", err)
	}
	if err := m.CreateRegion("r", StripeSize, ""); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("empty credential error = %v", err)
	}
	if err := m.CreateRegion("r", StripeSize, "c"); err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if err := m.CreateRegion("r", StripeSize, "c"); !errors.Is(err, ErrRegionExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
}
