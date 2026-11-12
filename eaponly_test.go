package netsec

import (
	"errors"
	"testing"
)

func TestNotifyAndEAPConstants(t *testing.T) {
	if NotifyEAPOnlyAuthentication != 16417 {
		t.Fatalf("EAP_ONLY_AUTHENTICATION = %d, want 16417", NotifyEAPOnlyAuthentication)
	}
	if EAPTypeTLS != 13 || EAPTypeAKAPrime != 50 {
		t.Fatalf("allowed method constants wrong: TLS=%d AKA'=%d", EAPTypeTLS, EAPTypeAKAPrime)
	}
	if EAPTypeMSCHAPv2 != 26 || EAPTypeGTC != 6 {
		t.Fatalf("forbidden method constants wrong: MSCHAPv2=%d GTC=%d", EAPTypeMSCHAPv2, EAPTypeGTC)
	}
}

func TestDefaultPolicyAllowList(t *testing.T) {
	policy := NewDefaultServerAuthPolicy()

	for _, method := range []EAPType{EAPTypeTLS, EAPTypeAKAPrime} {
		if !policy.EAPMethodAllowed(method) {
			t.Errorf("method %d must be allowed by the default policy", method)
		}
	}

	for _, method := range []EAPType{EAPTypeMSCHAPv2, EAPTypeGTC, EAPTypeMD5Challenge, EAPTypeIdentity} {
		if policy.EAPMethodAllowed(method) {
			t.Errorf("method %d must not be allowed by the default policy", method)
		}
	}

	defaults := DefaultAllowedEAPMethods()
	if len(defaults) != 2 {
		t.Fatalf("default allow list length = %d, want 2", len(defaults))
	}
}

func TestNewServerAuthPolicyRejectsWeakMethods(t *testing.T) {
	t.Parallel()

	for _, method := range []EAPType{EAPTypeGTC, EAPTypeMSCHAPv2, EAPTypeMD5Challenge} {
		method := method
		t.Run("forbidden", func(t *testing.T) {
			t.Parallel()
			if _, err := NewServerAuthPolicy([]EAPType{EAPTypeTLS, method}); err == nil {
				t.Fatalf("policy with forbidden method %d must fail to construct", method)
			}
		})
	}

	if _, err := NewServerAuthPolicy(nil); err == nil {
		t.Fatal("empty allow list must fail to construct")
	}

	custom, err := NewServerAuthPolicy([]EAPType{EAPTypeTLS})
	if err != nil {
		t.Fatalf("TLS-only policy construction failed: %v", err)
	}
	if custom.EAPMethodAllowed(EAPTypeAKAPrime) {
		t.Fatal("custom policy must honor the caller-supplied allow list")
	}
}

func TestVerifyServerAuth(t *testing.T) {
	t.Parallel()

	policy := NewDefaultServerAuthPolicy()

	tests := []struct {
		name    string
		auth    IKEAuthServerAuth
		wantErr error
	}{
		{
			name: "missing AUTH with neither side using EAP-only",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  false,
				ResponseHasEAPOnlyNotify: false,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeMSCHAPv2,
			},
			wantErr: ErrAuthPayloadMissing,
		},
		{
			name: "missing AUTH when only request carries the notify",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: false,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeTLS,
			},
			wantErr: ErrEAPOnlyAsymmetric,
		},
		{
			name: "missing AUTH when only response carries the notify",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  false,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeTLS,
			},
			wantErr: ErrEAPOnlyAsymmetric,
		},
		{
			name: "EAP-only negotiated but responder omits AUTH and EAP payload",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                0,
			},
			wantErr: ErrNoEAPMethod,
		},
		{
			name: "EAP-only with MSCHAPv2 is rejected",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeMSCHAPv2,
			},
			wantErr: ErrEAPMethodNotAllowed,
		},
		{
			name: "EAP-only with GTC is rejected",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeGTC,
			},
			wantErr: ErrEAPMethodNotAllowed,
		},
		{
			name: "EAP-only with MD5-Challenge is rejected",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeMD5Challenge,
			},
			wantErr: ErrEAPMethodNotAllowed,
		},
		{
			name: "EAP-only with an unlisted method is rejected",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                55,
			},
			wantErr: ErrEAPMethodNotAllowed,
		},
		{
			name: "EAP-only with EAP-TLS is accepted",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeTLS,
			},
			wantErr: nil,
		},
		{
			name: "EAP-only with EAP-AKA' is accepted",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  true,
				ResponseHasEAPOnlyNotify: true,
				ResponseHasAUTH:          false,
				EAPMethod:                EAPTypeAKAPrime,
			},
			wantErr: nil,
		},
		{
			name: "AUTH payload present is accepted on the normal path",
			auth: IKEAuthServerAuth{
				RequestHadEAPOnlyNotify:  false,
				ResponseHasEAPOnlyNotify: false,
				ResponseHasAUTH:          true,
				EAPMethod:                0,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := policy.VerifyServerAuth(tt.auth)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("VerifyServerAuth() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("VerifyServerAuth() error = nil, want %v", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyServerAuth() error = %v, want errors.Is %v", err, tt.wantErr)
			}
			if !errors.Is(err, ErrServerAuthFailed) {
				t.Fatalf("every failure must wrap ErrServerAuthFailed, got %v", err)
			}
		})
	}
}

func TestVerifyServerAuthNoFallbackToPassword(t *testing.T) {
	t.Parallel()

	policy := NewDefaultServerAuthPolicy()

	// A downgrade attempt: the responder drops AUTH, does not echo the
	// EAP-only notify, and offers password-only EAP-MSCHAPv2. The policy must
	// fail the exchange instead of accepting username/password verification.
	err := policy.VerifyServerAuth(IKEAuthServerAuth{
		RequestHadEAPOnlyNotify:  true,
		ResponseHasEAPOnlyNotify: false,
		ResponseHasAUTH:          false,
		EAPMethod:                EAPTypeMSCHAPv2,
	})
	if err == nil {
		t.Fatal("downgrade to password-only verification must be rejected")
	}
	if !errors.Is(err, ErrServerAuthFailed) {
		t.Fatalf("downgrade rejection must wrap ErrServerAuthFailed, got %v", err)
	}
	if !errors.Is(err, ErrEAPOnlyAsymmetric) {
		t.Fatalf("expected asymmetric notify failure, got %v", err)
	}
}

func TestVerifyServerAuthCustomAllowList(t *testing.T) {
	t.Parallel()

	policy, err := NewServerAuthPolicy([]EAPType{EAPTypeTLS})
	if err != nil {
		t.Fatalf("custom policy construction failed: %v", err)
	}

	auth := IKEAuthServerAuth{
		RequestHadEAPOnlyNotify:  true,
		ResponseHasEAPOnlyNotify: true,
		ResponseHasAUTH:          false,
		EAPMethod:                EAPTypeAKAPrime,
	}
	if err := policy.VerifyServerAuth(auth); !errors.Is(err, ErrEAPMethodNotAllowed) {
		t.Fatalf("AKA' under TLS-only policy = %v, want ErrEAPMethodNotAllowed", err)
	}

	auth.EAPMethod = EAPTypeTLS
	if err := policy.VerifyServerAuth(auth); err != nil {
		t.Fatalf("TLS under TLS-only policy = %v, want nil", err)
	}
}
