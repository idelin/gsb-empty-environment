package netsec_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0485"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)}
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

func newManager(t *testing.T, cfg netsec.Config) *netsec.Manager {
	t.Helper()
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	m, err := netsec.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return m
}

func login(t *testing.T, m *netsec.Manager, userID, deviceID string) *netsec.Session {
	t.Helper()
	session, err := m.Authenticate(netsec.LoginRequest{
		UserID:   userID,
		TenantID: "tenant-a",
		DeviceID: deviceID,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	return session
}

func TestAuthenticateGeneratesHighEntropyIdentifiers(t *testing.T) {
	m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow})

	const attempts = 256
	seen := make(map[string]struct{}, attempts)
	for i := 0; i < attempts; i++ {
		session, err := m.Authenticate(netsec.LoginRequest{
			UserID:   "user",
			DeviceID: "device",
		})
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}

		if got, want := len(session.ID), 43; got != want {
			t.Fatalf("len(session.ID) = %d, want %d", got, want)
		}
		raw, err := base64.RawURLEncoding.DecodeString(session.ID)
		if err != nil {
			t.Fatalf("session ID is not base64url: %v", err)
		}
		if got, want := len(raw), netsec.SessionIDBytes; got != want {
			t.Fatalf("decoded entropy length = %d, want %d", got, want)
		}
		if _, duplicate := seen[session.ID]; duplicate {
			t.Fatalf("duplicate session ID %q", session.ID)
		}
		seen[session.ID] = struct{}{}

		if !session.Authenticated {
			t.Fatal("new session is not marked authenticated")
		}
		if session.Generation != 1 {
			t.Fatalf("Generation = %d, want 1", session.Generation)
		}
	}
}

func TestSessionFixationPrevention(t *testing.T) {
	const (
		attackerID = "attacker-chosen-session-id"
		deviceID   = "victim-device"
	)

	m := newManager(t, netsec.DefaultConfig())
	if _, err := m.Validate(attackerID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(fixed ID) error = %v, want ErrSessionInvalid", err)
	}

	session, err := m.Authenticate(netsec.LoginRequest{
		UserID:   "victim",
		DeviceID: deviceID,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if session.ID == attackerID {
		t.Fatal("authentication reused the attacker-fixed identifier")
	}

	// Even after a successful victim login, the identifier known to the
	// attacker remains invalid.
	if _, err := m.Validate(attackerID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(fixed ID after login) error = %v, want ErrSessionInvalid", err)
	}
	if _, err := m.Validate(session.ID, deviceID); err != nil {
		t.Fatalf("Validate(victim ID) error = %v", err)
	}
}

func TestRotationInvalidatesOldIdentifier(t *testing.T) {
	const deviceID = "device-1"

	m := newManager(t, netsec.DefaultConfig())
	original := login(t, m, "alice", deviceID)

	rotated, err := m.Rotate(original.ID, deviceID)
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if rotated.ID == original.ID {
		t.Fatal("Rotate() reused the old identifier")
	}
	if rotated.Generation != 2 {
		t.Fatalf("Generation = %d, want 2", rotated.Generation)
	}
	if _, err := m.Validate(original.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(old ID) error = %v, want ErrSessionInvalid", err)
	}
	if _, err := m.Validate(rotated.ID, deviceID); err != nil {
		t.Fatalf("Validate(new ID) error = %v", err)
	}

	// Replaying the first identifier must not revoke or disturb the current
	// session.
	if _, err := m.Rotate(original.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Rotate(old ID) error = %v, want ErrSessionInvalid", err)
	}
	if _, err := m.Validate(rotated.ID, deviceID); err != nil {
		t.Fatalf("Validate(new ID after replay) error = %v", err)
	}

	secondRotation, err := m.RotateSession(rotated.ID, deviceID)
	if err != nil {
		t.Fatalf("RotateSession() error = %v", err)
	}
	if secondRotation.Generation != 3 {
		t.Fatalf("Generation = %d, want 3", secondRotation.Generation)
	}
	if _, err := m.Validate(rotated.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(intermediate ID) error = %v, want ErrSessionInvalid", err)
	}
}

func TestPrivilegeElevationRotatesIdentifier(t *testing.T) {
	const deviceID = "device-1"

	m := newManager(t, netsec.DefaultConfig())
	session, err := m.Authenticate(netsec.LoginRequest{
		UserID:      "alice",
		TenantID:    "tenant-a",
		DeviceID:    deviceID,
		AuthLevel:   1,
		Roles:       []string{"member"},
		Permissions: []string{"read"},
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	elevated, err := m.Elevate(session.ID, deviceID, netsec.Elevation{
		AuthLevel:   2,
		Roles:       []string{"admin"},
		Permissions: []string{"write"},
	})
	if err != nil {
		t.Fatalf("Elevate() error = %v", err)
	}
	if elevated.ID == session.ID {
		t.Fatal("Elevate() did not rotate the session identifier")
	}
	if elevated.Generation != 2 {
		t.Fatalf("Generation = %d, want 2", elevated.Generation)
	}
	if elevated.AuthLevel != 2 {
		t.Fatalf("AuthLevel = %d, want 2", elevated.AuthLevel)
	}
	if got, want := elevated.Roles, []string{"member", "admin"}; !equalStrings(got, want) {
		t.Fatalf("Roles = %v, want %v", got, want)
	}
	if got, want := elevated.Permissions, []string{"read", "write"}; !equalStrings(got, want) {
		t.Fatalf("Permissions = %v, want %v", got, want)
	}
	if _, err := m.Validate(session.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(pre-elevation ID) error = %v, want ErrSessionInvalid", err)
	}
	if _, err := m.Validate(elevated.ID, deviceID); err != nil {
		t.Fatalf("Validate(post-elevation ID) error = %v", err)
	}

	if _, err := m.Elevate(elevated.ID, deviceID, netsec.Elevation{AuthLevel: 1}); !errors.Is(err, netsec.ErrPrivilegeDowngrade) {
		t.Fatalf("Elevate(downgrade) error = %v, want ErrPrivilegeDowngrade", err)
	}
	if _, err := m.Validate(elevated.ID, deviceID); err != nil {
		t.Fatalf("failed elevation must not invalidate the session: %v", err)
	}

	if _, err := m.Elevate(elevated.ID, deviceID, netsec.Elevation{
		AuthLevel: 2,
		Roles:     []string{"admin"},
	}); !errors.Is(err, netsec.ErrNoPrivilegeChange) {
		t.Fatalf("Elevate(no-op) error = %v, want ErrNoPrivilegeChange", err)
	}
	if _, err := m.Validate(elevated.ID, deviceID); err != nil {
		t.Fatalf("no-op elevation must not invalidate the session: %v", err)
	}

	withRole, err := m.ElevateRoles(elevated.ID, deviceID, "auditor")
	if err != nil {
		t.Fatalf("ElevateRoles() error = %v", err)
	}
	if withRole.Generation != 3 {
		t.Fatalf("Generation = %d, want 3", withRole.Generation)
	}
	if _, err := m.Validate(elevated.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(pre-role-elevation ID) error = %v, want ErrSessionInvalid", err)
	}
}

func TestTenantSwitchRotatesIdentifierAndDropsInheritedPrivileges(t *testing.T) {
	const deviceID = "device-1"

	m := newManager(t, netsec.DefaultConfig())
	session, err := m.Authenticate(netsec.LoginRequest{
		UserID:      "alice",
		TenantID:    "tenant-a",
		DeviceID:    deviceID,
		AuthLevel:   3,
		Roles:       []string{"tenant-a-admin"},
		Permissions: []string{"tenant-a-write"},
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	switched, err := m.SwitchTenant(session.ID, deviceID, netsec.TenantSwitch{
		TenantID:  "tenant-b",
		AuthLevel: 0,
		Roles:     []string{"viewer"},
	})
	if err != nil {
		t.Fatalf("SwitchTenant() error = %v", err)
	}
	if switched.ID == session.ID {
		t.Fatal("SwitchTenant() did not rotate the session identifier")
	}
	if switched.Generation != 2 {
		t.Fatalf("Generation = %d, want 2", switched.Generation)
	}
	if switched.TenantID != "tenant-b" {
		t.Fatalf("TenantID = %q, want tenant-b", switched.TenantID)
	}
	if switched.AuthLevel != 0 {
		t.Fatalf("AuthLevel = %d, want 0", switched.AuthLevel)
	}
	if got, want := switched.Roles, []string{"viewer"}; !equalStrings(got, want) {
		t.Fatalf("Roles = %v, want %v", got, want)
	}
	if len(switched.Permissions) != 0 {
		t.Fatalf("Permissions = %v, want none", switched.Permissions)
	}
	if _, err := m.Validate(session.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(pre-switch ID) error = %v, want ErrSessionInvalid", err)
	}
	if _, err := m.Validate(switched.ID, deviceID); err != nil {
		t.Fatalf("Validate(post-switch ID) error = %v", err)
	}

	if _, err := m.SwitchTenant(switched.ID, deviceID, netsec.TenantSwitch{TenantID: "tenant-b"}); !errors.Is(err, netsec.ErrTenantUnchanged) {
		t.Fatalf("SwitchTenant(same tenant) error = %v, want ErrTenantUnchanged", err)
	}
	if _, err := m.Validate(switched.ID, deviceID); err != nil {
		t.Fatalf("no-op tenant switch must not invalidate the session: %v", err)
	}

	final, err := m.SwitchTenantTo(switched.ID, deviceID, "tenant-c")
	if err != nil {
		t.Fatalf("SwitchTenantTo() error = %v", err)
	}
	if final.TenantID != "tenant-c" {
		t.Fatalf("TenantID = %q, want tenant-c", final.TenantID)
	}
	if len(final.Roles) != 0 || len(final.Permissions) != 0 || final.AuthLevel != 0 {
		t.Fatalf("tenant switch inherited privileges: %+v", final)
	}
}

func TestDeviceBindingIsEnforced(t *testing.T) {
	m := newManager(t, netsec.DefaultConfig())
	session := login(t, m, "alice", "device-1")

	if _, err := m.Validate(session.ID, "device-2"); !errors.Is(err, netsec.ErrDeviceMismatch) {
		t.Fatalf("Validate(other device) error = %v, want ErrDeviceMismatch", err)
	}
	if _, err := m.Validate(session.ID, "Device-1"); !errors.Is(err, netsec.ErrDeviceMismatch) {
		t.Fatalf("Validate(case-modified device) error = %v, want ErrDeviceMismatch", err)
	}
	if _, err := m.Validate(session.ID, "device-1"); err != nil {
		t.Fatalf("Validate(bound device) error = %v", err)
	}

	if _, err := m.Rotate(session.ID, "device-2"); !errors.Is(err, netsec.ErrDeviceMismatch) {
		t.Fatalf("Rotate(other device) error = %v, want ErrDeviceMismatch", err)
	}
	if _, err := m.Validate(session.ID, "device-1"); err != nil {
		t.Fatalf("failed rotation must not invalidate the session: %v", err)
	}

	if _, err := m.Authenticate(netsec.LoginRequest{UserID: "alice"}); !errors.Is(err, netsec.ErrDeviceIDRequired) {
		t.Fatalf("Authenticate(no device) error = %v, want ErrDeviceIDRequired", err)
	}
	if _, err := m.Validate(session.ID, " "); !errors.Is(err, netsec.ErrDeviceIDRequired) {
		t.Fatalf("Validate(blank device) error = %v, want ErrDeviceIDRequired", err)
	}
}

func TestSessionExpiration(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, netsec.Config{
		IdleTimeout:           time.Minute,
		AbsoluteLifetime:      10 * time.Minute,
		ConcurrentLoginPolicy: netsec.ConcurrentLoginRejectNew,
		Clock:                 clock.Now,
	})
	session := login(t, m, "alice", "device-1")

	clock.Advance(30 * time.Second)
	refreshed, err := m.Validate(session.ID, "device-1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, want := refreshed.IdleExpiresAt, clock.Now().Add(time.Minute); !got.Equal(want) {
		t.Fatalf("IdleExpiresAt = %v, want %v", got, want)
	}

	clock.Advance(time.Minute)
	if _, err := m.Validate(session.ID, "device-1"); !errors.Is(err, netsec.ErrSessionExpired) {
		t.Fatalf("Validate(at idle boundary) error = %v, want ErrSessionExpired", err)
	}
	if _, err := m.Validate(session.ID, "device-1"); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(expired ID after purge) error = %v, want ErrSessionInvalid", err)
	}
}

func TestAbsoluteLifetimeSurvivesRotation(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, netsec.Config{
		IdleTimeout:           20 * time.Minute,
		AbsoluteLifetime:      10 * time.Minute,
		ConcurrentLoginPolicy: netsec.ConcurrentLoginRejectNew,
		Clock:                 clock.Now,
	})
	session := login(t, m, "alice", "device-1")

	clock.Advance(time.Minute)
	rotated, err := m.Rotate(session.ID, "device-1")
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if !rotated.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("rotation changed absolute expiry from %v to %v", session.ExpiresAt, rotated.ExpiresAt)
	}

	clock.Advance(8 * time.Minute)
	if _, err := m.Validate(rotated.ID, "device-1"); err != nil {
		t.Fatalf("Validate(before absolute expiry) error = %v", err)
	}

	clock.Advance(time.Minute) // exactly 10 minutes after login
	if _, err := m.Validate(rotated.ID, "device-1"); !errors.Is(err, netsec.ErrSessionExpired) {
		t.Fatalf("Validate(at absolute expiry) error = %v, want ErrSessionExpired", err)
	}
}

func TestConcurrentLoginPolicies(t *testing.T) {
	t.Run("reject new", func(t *testing.T) {
		m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginRejectNew})
		first := login(t, m, "alice", "device-1")
		if _, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-2"}); !errors.Is(err, netsec.ErrConcurrentLoginRejected) {
			t.Fatalf("second login error = %v, want ErrConcurrentLoginRejected", err)
		}
		if _, err := m.Validate(first.ID, "device-1"); err != nil {
			t.Fatalf("rejected login invalidated the first session: %v", err)
		}
	})

	t.Run("revoke existing", func(t *testing.T) {
		m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginRevokeExisting})
		first := login(t, m, "alice", "device-1")
		second, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-2"})
		if err != nil {
			t.Fatalf("second login error = %v", err)
		}
		if _, err := m.Validate(first.ID, "device-1"); !errors.Is(err, netsec.ErrSessionInvalid) {
			t.Fatalf("Validate(replaced session) error = %v, want ErrSessionInvalid", err)
		}
		if _, err := m.Validate(second.ID, "device-2"); err != nil {
			t.Fatalf("Validate(new session) error = %v", err)
		}
		if got := m.ActiveSessionCount("alice"); got != 1 {
			t.Fatalf("ActiveSessionCount() = %d, want 1", got)
		}
	})

	t.Run("allow multiple", func(t *testing.T) {
		m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow})
		first := login(t, m, "alice", "device-1")
		second := login(t, m, "alice", "device-2")
		if first.ID == second.ID {
			t.Fatal("concurrent logins share an identifier")
		}
		if _, err := m.Validate(first.ID, "device-1"); err != nil {
			t.Fatalf("Validate(first session) error = %v", err)
		}
		if _, err := m.Validate(second.ID, "device-2"); err != nil {
			t.Fatalf("Validate(second session) error = %v", err)
		}
		if got := m.ActiveSessionCount("alice"); got != 2 {
			t.Fatalf("ActiveSessionCount() = %d, want 2", got)
		}

		if err := m.Logout(first.ID); err != nil {
			t.Fatalf("Logout() error = %v", err)
		}
		if _, err := m.Validate(first.ID, "device-1"); !errors.Is(err, netsec.ErrSessionInvalid) {
			t.Fatalf("Validate(logged-out session) error = %v, want ErrSessionInvalid", err)
		}
		if _, err := m.Validate(second.ID, "device-2"); err != nil {
			t.Fatalf("logout affected an independent session: %v", err)
		}
	})

	t.Run("expired session does not block login", func(t *testing.T) {
		clock := newFakeClock()
		m := newManager(t, netsec.Config{
			IdleTimeout:           time.Minute,
			AbsoluteLifetime:      time.Hour,
			ConcurrentLoginPolicy: netsec.ConcurrentLoginRejectNew,
			Clock:                 clock.Now,
		})
		first := login(t, m, "alice", "device-1")
		clock.Advance(2 * time.Minute)

		second, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-2"})
		if err != nil {
			t.Fatalf("login after expiry error = %v", err)
		}
		if second.ID == first.ID {
			t.Fatal("new login reused the expired identifier")
		}
		if _, err := m.Validate(first.ID, "device-1"); !errors.Is(err, netsec.ErrSessionInvalid) {
			t.Fatalf("Validate(expired session) error = %v, want ErrSessionInvalid", err)
		}
	})

	t.Run("zero value rejects", func(t *testing.T) {
		m := newManager(t, netsec.Config{})
		login(t, m, "alice", "device-1")
		if _, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-2"}); !errors.Is(err, netsec.ErrConcurrentLoginRejected) {
			t.Fatalf("second login error = %v, want ErrConcurrentLoginRejected", err)
		}
	})
}

func TestConcurrentAuthenticationObeysPolicy(t *testing.T) {
	m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginRejectNew})

	const goroutines = 64
	var wg sync.WaitGroup
	var successes atomic.Int32
	var rejections atomic.Int32
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := m.Authenticate(netsec.LoginRequest{
				UserID:   "alice",
				DeviceID: "device-1",
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, netsec.ErrConcurrentLoginRejected):
				rejections.Add(1)
			default:
				t.Errorf("Authenticate() error = %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful logins = %d, want 1", got)
	}
	if got := rejections.Load(); got != goroutines-1 {
		t.Fatalf("rejected logins = %d, want %d", got, goroutines-1)
	}
	if got := m.ActiveSessionCount("alice"); got != 1 {
		t.Fatalf("ActiveSessionCount() = %d, want 1", got)
	}
}

func TestConcurrentRotationHasSingleWinner(t *testing.T) {
	const deviceID = "device-1"

	m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow})
	session := login(t, m, "alice", deviceID)

	const goroutines = 64
	var wg sync.WaitGroup
	var successes atomic.Int32
	var invalid atomic.Int32
	start := make(chan struct{})
	newIDs := make(chan string, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rotated, err := m.Rotate(session.ID, deviceID)
			switch {
			case err == nil:
				successes.Add(1)
				newIDs <- rotated.ID
			case errors.Is(err, netsec.ErrSessionInvalid):
				invalid.Add(1)
			default:
				t.Errorf("Rotate() error = %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(newIDs)

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful rotations = %d, want 1", got)
	}
	if got := invalid.Load(); got != goroutines-1 {
		t.Fatalf("invalid rotations = %d, want %d", got, goroutines-1)
	}
	if _, err := m.Validate(session.ID, deviceID); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(old ID) error = %v, want ErrSessionInvalid", err)
	}

	var current string
	for id := range newIDs {
		current = id
	}
	if current == "" {
		t.Fatal("rotation winner did not return a new identifier")
	}
	if _, err := m.Validate(current, deviceID); err != nil {
		t.Fatalf("Validate(winning ID) error = %v", err)
	}
}

func TestRevocationAndCounts(t *testing.T) {
	m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow})
	aliceOne := login(t, m, "alice", "device-1")
	aliceTwo := login(t, m, "alice", "device-2")
	bob := login(t, m, "bob", "device-3")

	if got := m.SessionCount(); got != 3 {
		t.Fatalf("SessionCount() = %d, want 3", got)
	}
	if got := m.RevokeUser("alice"); got != 2 {
		t.Fatalf("RevokeUser() = %d, want 2", got)
	}
	for _, id := range []string{aliceOne.ID, aliceTwo.ID} {
		if _, err := m.Validate(id, "device-1"); !errors.Is(err, netsec.ErrSessionInvalid) {
			t.Fatalf("Validate(revoked ID) error = %v, want ErrSessionInvalid", err)
		}
	}
	if _, err := m.Validate(bob.ID, "device-3"); err != nil {
		t.Fatalf("RevokeUser affected another user: %v", err)
	}

	if got := m.RevokeAll(); got != 1 {
		t.Fatalf("RevokeAll() = %d, want 1", got)
	}
	if _, err := m.Validate(bob.ID, "device-3"); !errors.Is(err, netsec.ErrSessionInvalid) {
		t.Fatalf("Validate(after RevokeAll) error = %v, want ErrSessionInvalid", err)
	}
}

func TestConfigurationValidationAndDefaults(t *testing.T) {
	if _, err := netsec.NewManager(netsec.Config{IdleTimeout: -time.Second}); !errors.Is(err, netsec.ErrInvalidConfig) {
		t.Fatalf("negative idle timeout error = %v, want ErrInvalidConfig", err)
	}
	if _, err := netsec.NewManager(netsec.Config{AbsoluteLifetime: -time.Second}); !errors.Is(err, netsec.ErrInvalidConfig) {
		t.Fatalf("negative absolute lifetime error = %v, want ErrInvalidConfig", err)
	}
	if _, err := netsec.NewManager(netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginPolicy(99)}); !errors.Is(err, netsec.ErrInvalidConcurrentPolicy) {
		t.Fatalf("invalid policy error = %v, want ErrInvalidConcurrentPolicy", err)
	}

	clock := newFakeClock()
	m, err := netsec.NewManager(netsec.Config{
		Clock: clock.Now,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session := login(t, m, "alice", "device-1")
	if got, want := session.ExpiresAt.Sub(session.CreatedAt), netsec.DefaultAbsoluteLifetime; got != want {
		t.Fatalf("absolute lifetime = %v, want %v", got, want)
	}
	if got, want := session.IdleExpiresAt.Sub(session.CreatedAt), netsec.DefaultIdleTimeout; got != want {
		t.Fatalf("idle timeout = %v, want %v", got, want)
	}

	if _, err := netsec.New(netsec.DefaultConfig()); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := netsec.NewSessionManager(netsec.DefaultConfig()); err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestEntropyFailureDoesNotCreateSession(t *testing.T) {
	m, err := netsec.NewManager(netsec.Config{
		ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow,
		Rand:                  failingReader{},
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if _, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-1"}); !errors.Is(err, netsec.ErrRandomSource) {
		t.Fatalf("Authenticate() error = %v, want ErrRandomSource", err)
	}
	if got := m.SessionCount(); got != 0 {
		t.Fatalf("SessionCount() = %d, want 0", got)
	}
}

func TestShortEntropyReadIsRejected(t *testing.T) {
	m, err := netsec.NewManager(netsec.Config{
		ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow,
		Rand:                  bytes.NewReader([]byte("too short")),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if _, err := m.Authenticate(netsec.LoginRequest{UserID: "alice", DeviceID: "device-1"}); !errors.Is(err, netsec.ErrRandomSource) {
		t.Fatalf("Authenticate() error = %v, want ErrRandomSource", err)
	}
}

func TestSnapshotsDoNotExposeMutableState(t *testing.T) {
	m := newManager(t, netsec.Config{ConcurrentLoginPolicy: netsec.ConcurrentLoginAllow})
	session, err := m.Authenticate(netsec.LoginRequest{
		UserID:      "alice",
		DeviceID:    "device-1",
		Roles:       []string{"member"},
		Permissions: []string{"read"},
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	session.Roles[0] = "mutated"
	session.Permissions[0] = "mutated"

	current, err := m.Validate(session.ID, "device-1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, want := current.Roles, []string{"member"}; !equalStrings(got, want) {
		t.Fatalf("Roles = %v, want %v", got, want)
	}
	if got, want := current.Permissions, []string{"read"}; !equalStrings(got, want) {
		t.Fatalf("Permissions = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
