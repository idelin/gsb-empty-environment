package netsec

import (
	"errors"
	"testing"
)

func TestPolicyEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		registered []string
		allowed    bool
		reason     Reason
		canonical  string
		flow       Flow
		mode       ResponseMode
		wantErr    error
	}{
		{
			name:      "standalone code",
			raw:       "code",
			allowed:   true,
			canonical: "code",
			flow:      FlowAuthorizationCode,
			mode:      ResponseModeQuery,
		},
		{
			name:       "standalone code with registrations",
			raw:        "code",
			registered: []string{"code id_token"},
			allowed:    true,
			canonical:  "code",
			flow:       FlowAuthorizationCode,
			mode:       ResponseModeQuery,
		},
		{
			name:       "code token is rejected",
			raw:        "code token",
			registered: []string{"code id_token", "code token"},
			reason:     ReasonAccessTokenInFrontend,
			wantErr:    ErrAccessTokenInFrontend,
		},
		{
			name:    "token alone is rejected",
			raw:     "token",
			reason:  ReasonAccessTokenInFrontend,
			wantErr: ErrAccessTokenInFrontend,
		},
		{
			name:       "id_token token is rejected",
			raw:        "id_token token",
			registered: []string{"code id_token"},
			reason:     ReasonAccessTokenInFrontend,
			wantErr:    ErrAccessTokenInFrontend,
		},
		{
			name:       "code id_token token is rejected",
			raw:        "code id_token token",
			registered: []string{"code id_token", "code id_token token"},
			reason:     ReasonAccessTokenInFrontend,
			wantErr:    ErrAccessTokenInFrontend,
		},
		{
			name:       "registered hybrid",
			raw:        "code id_token",
			registered: []string{"code id_token"},
			allowed:    true,
			canonical:  "code id_token",
			flow:       FlowHybrid,
			mode:       ResponseModeFragment,
		},
		{
			name:       "hybrid component order is normalized",
			raw:        "id_token code",
			registered: []string{"code id_token"},
			allowed:    true,
			canonical:  "code id_token",
			flow:       FlowHybrid,
			mode:       ResponseModeFragment,
		},
		{
			name:       "registration order is normalized",
			raw:        "code id_token",
			registered: []string{"id_token code"},
			allowed:    true,
			canonical:  "code id_token",
			flow:       FlowHybrid,
			mode:       ResponseModeFragment,
		},
		{
			name:       "extra spaces are tolerated",
			raw:        "code   id_token",
			registered: []string{"code id_token"},
			allowed:    true,
			canonical:  "code id_token",
			flow:       FlowHybrid,
			mode:       ResponseModeFragment,
		},
		{
			name:       "unregistered hybrid",
			raw:        "code id_token",
			registered: []string{"code"},
			reason:     ReasonUnregisteredHybridResponseType,
			wantErr:    ErrUnregisteredHybridResponseType,
		},
		{
			name:       "standalone id_token is not hybrid",
			raw:        "id_token",
			registered: []string{"code id_token"},
			reason:     ReasonUnregisteredHybridResponseType,
			wantErr:    ErrUnregisteredHybridResponseType,
		},
		{
			name:    "empty response type",
			raw:     "",
			reason:  ReasonEmptyResponseType,
			wantErr: ErrEmptyResponseType,
		},
		{
			name:    "whitespace only response type",
			raw:     "   ",
			reason:  ReasonEmptyResponseType,
			wantErr: ErrEmptyResponseType,
		},
		{
			name:    "unknown response type",
			raw:     "code magic",
			reason:  ReasonUnsupportedResponseType,
			wantErr: ErrUnsupportedResponseType,
		},
		{
			name:    "duplicate response type",
			raw:     "code code",
			reason:  ReasonMalformedResponseType,
			wantErr: ErrMalformedResponseType,
		},
		{
			name:    "control characters",
			raw:     "code\tid_token",
			reason:  ReasonMalformedResponseType,
			wantErr: ErrMalformedResponseType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateResponseType(tt.raw, tt.registered)

			if got.Allowed != tt.allowed {
				t.Fatalf("Allowed = %v, want %v (reason %q)", got.Allowed, tt.allowed, got.Reason)
			}
			if got.Reason != tt.reason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tt.reason)
			}
			if tt.allowed {
				if got.ResponseType != tt.canonical {
					t.Fatalf("ResponseType = %q, want %q", got.ResponseType, tt.canonical)
				}
				if got.Flow != tt.flow {
					t.Fatalf("Flow = %q, want %q", got.Flow, tt.flow)
				}
				if got.ResponseMode != tt.mode {
					t.Fatalf("ResponseMode = %q, want %q", got.ResponseMode, tt.mode)
				}
				if got.Err != nil {
					t.Fatalf("Err = %v, want nil", got.Err)
				}
				return
			}

			if got.Flow != "" || got.ResponseMode != "" {
				t.Fatalf("denied decision has flow %q and mode %q", got.Flow, got.ResponseMode)
			}
			if tt.wantErr != nil && !errors.Is(got.Err, tt.wantErr) {
				t.Fatalf("Err = %v, want errors.Is(%v)", got.Err, tt.wantErr)
			}
		})
	}
}

func TestPolicyRegisterIgnoresUnsafeOrInvalidHybrids(t *testing.T) {
	policy := NewPolicy(
		"code id_token",
		"code token",
		"token",
		"id_token",
		"code",
		"code id_token token",
		"code bogus",
	)

	if !policy.Registered("code id_token") {
		t.Fatal("code id_token should be registered")
	}
	for _, unsafe := range []string{"code token", "token", "id_token", "code", "code id_token token"} {
		if policy.Registered(unsafe) {
			t.Fatalf("%q must not be registered", unsafe)
		}
	}

	if got := policy.Evaluate("code token"); got.Allowed {
		t.Fatal("code token must remain rejected")
	}
	if got := policy.Evaluate("code id_token"); !got.Allowed {
		t.Fatalf("code id_token should be allowed: %v", got.Err)
	}
}

func TestEvaluateHelpers(t *testing.T) {
	if !IsAllowed("code") {
		t.Fatal("standalone code should be allowed")
	}
	if IsAllowed("code token") {
		t.Fatal("code token should be rejected")
	}
	if got := Evaluate("code id_token", "code id_token"); !got.Allowed {
		t.Fatalf("registered hybrid should be allowed: %v", got.Err)
	}
}
