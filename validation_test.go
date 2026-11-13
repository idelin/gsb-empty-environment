package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSTSResultValidation(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	identity := testIdentity("tenant-a", "role")

	tests := []struct {
		name    string
		mutate  func(*STSResult)
		margin  time.Duration
		skew    time.Duration
		wantErr error
	}{
		{
			name: "wrong role",
			mutate: func(result *STSResult) {
				result.RoleID = "role-name"
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "wrong tenant",
			mutate: func(result *STSResult) {
				result.TenantID = "tenant-b"
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "wrong audience",
			mutate: func(result *STSResult) {
				result.Audience = "another-audience"
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "wrong tag digest",
			mutate: func(result *STSResult) {
				result.SessionTagsDigest = DigestBytes([]byte("different-tags"))
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "empty access key",
			mutate: func(result *STSResult) {
				result.AccessKeyID = ""
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "empty session token",
			mutate: func(result *STSResult) {
				result.SessionToken = ""
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "missing secret handle",
			mutate: func(result *STSResult) {
				result.Secret = nil
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "issue time beyond allowed skew",
			mutate: func(result *STSResult) {
				result.IssuedAt = now.Add(2 * time.Minute)
				result.ExpiresAt = result.IssuedAt.Add(time.Hour)
			},
			skew:    time.Minute,
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "expiry not after issue",
			mutate: func(result *STSResult) {
				result.ExpiresAt = result.IssuedAt
			},
			wantErr: ErrInvalidSTSResult,
		},
		{
			name: "already expired",
			mutate: func(result *STSResult) {
				result.IssuedAt = now.Add(-2 * time.Hour)
				result.ExpiresAt = now.Add(-time.Hour)
			},
			wantErr: ErrCredentialExpired,
		},
		{
			name: "inside safety margin",
			mutate: func(result *STSResult) {
				result.ExpiresAt = now.Add(time.Minute)
			},
			margin:  5 * time.Minute,
			wantErr: ErrCredentialExpired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newTestClock(now)
			sts := &testSTS{}
			var resultSecret *MemorySecret
			sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
				result := testResult(identity, clock, time.Hour, uniqueSecretID("validation"), "AKIA", "material")
				tc.mutate(result)
				if result.Secret != nil {
					resultSecret = result.Secret.(*MemorySecret)
				} else {
					resultSecret = nil
				}
				return result, nil
			}
			options := []Option{WithClock(clock.Now), WithSafetyMargin(tc.margin), WithClockSkew(tc.skew)}
			cache := newTestCache(t, sts, options...)

			_, err := cache.Borrow(context.Background(), identity)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Borrow error = %v, want %v", err, tc.wantErr)
			}
			if cache.Len() != 0 {
				t.Fatalf("invalid result was cached")
			}
			if resultSecret != nil && !resultSecret.State().Destroyed {
				t.Fatal("rejected result secret was not destroyed")
			}
		})
	}
}

func TestSafetyMarginStopsLendingBeforeExpiry(t *testing.T) {
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	clock := newTestClock(start)
	identity := testIdentity("tenant-a", "role")
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		secretID := uniqueSecretID("margin")
		result := &STSResult{
			AccessKeyID:       "AKIA",
			Secret:            sts.recordSecret(NewMemorySecret(secretID, []byte("material"))),
			SessionToken:      "token",
			RoleID:            identity.RoleID,
			TenantID:          identity.TenantID,
			Audience:          identity.Audience,
			SessionTagsDigest: identity.SessionTagsDigest,
			IssuedAt:          start,
			ExpiresAt:         start.Add(10 * time.Second),
		}
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(3*time.Second))

	lease := mustBorrow(t, cache, identity)
	defer lease.Release()
	if sts.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", sts.Calls())
	}

	clock.Advance(6 * time.Second)
	lease2 := mustBorrow(t, cache, identity)
	defer lease2.Release()
	if sts.Calls() != 1 {
		t.Fatalf("cached borrow within margin made an STS call")
	}

	clock.Advance(time.Second)
	if _, err := cache.Borrow(context.Background(), identity); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("borrow at usable deadline error = %v, want ErrCredentialExpired", err)
	}
	if cache.Len() != 0 {
		t.Fatalf("credential inside safety margin remains cached")
	}
}

func TestMaxTTLCapsCacheExpiry(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	clock := newTestClock(now)
	identity := testIdentity("tenant-a", "role")
	sts := &testSTS{}
	sts.fetch = func(_ context.Context, identity Identity) (*STSResult, error) {
		result := testResult(identity, clock, time.Hour, uniqueSecretID("ttl"), "AKIA", "material")
		result.Secret = sts.recordSecret(result.Secret.(*MemorySecret))
		return result, nil
	}
	cache := newTestCache(t, sts, WithClock(clock.Now), WithSafetyMargin(time.Second), WithMaxTTL(4*time.Second))

	lease := mustBorrow(t, cache, identity)
	defer lease.Release()

	cache.mu.Lock()
	entry := cache.entries[identity.CacheKey()]
	cache.mu.Unlock()
	if entry == nil {
		t.Fatal("entry missing")
	}
	if want := now.Add(time.Hour); !entry.expiresAt.Equal(want) {
		t.Fatalf("lease expiry = %s, want STS expiry %s", entry.expiresAt, want)
	}
	if want := now.Add(3 * time.Second); !entry.usableUntil.Equal(want) {
		t.Fatalf("usable until = %s, want %s", entry.usableUntil, want)
	}
}
