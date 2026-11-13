package netsec

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func testRules() Rules {
	return Rules{
		TrustedVerifierIDs: []string{"https://verifier.example.com"},
		PolicyDigest:       DigestSet{"sha256": "policynew"},
		now:                func() time.Time { return testNow },
	}
}

func validVSA() *VSA {
	return &VSA{
		Verifier:           Verifier{ID: "https://verifier.example.com"},
		TimeVerified:       testNow.Add(-time.Hour),
		ResourceURI:        "pkg:oci/app@sha256:abc",
		Policy:             PolicyRef{URI: "https://example.com/policy", Digest: DigestSet{"sha256": "policynew"}},
		VerificationResult: "PASSED",
		VerifiedLevels:     []string{"SLSA_BUILD_LEVEL_3"},
		Subject: []Subject{{
			Name:   "app",
			Digest: DigestSet{"sha256": "abc"},
		}},
	}
}

func validRequest() Request {
	return Request{
		ResourceURI:   "pkg:oci/app@sha256:abc",
		Digest:        DigestSet{"sha256": "abc"},
		RequiredLevel: "SLSA_BUILD_LEVEL_3",
	}
}

func admitErr(t *testing.T, v *VSA, req Request) error {
	t.Helper()
	j, err := NewJudge(testRules())
	if err != nil {
		t.Fatalf("NewJudge: %v", err)
	}
	return j.Admit(v, req)
}

func TestAdmitValid(t *testing.T) {
	if err := admitErr(t, validVSA(), validRequest()); err != nil {
		t.Fatalf("valid VSA rejected: %v", err)
	}
}

func TestRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(v *VSA, req *Request)
		want   error
	}{
		{
			name:   "self-signed verifier not in trust list",
			mutate: func(v *VSA, req *Request) { v.Verifier.ID = "https://attacker.example.com" },
			want:   ErrUntrustedVerifier,
		},
		{
			name:   "empty verifier id",
			mutate: func(v *VSA, req *Request) { v.Verifier.ID = "" },
			want:   ErrUntrustedVerifier,
		},
		{
			name:   "subject digest of another artifact",
			mutate: func(v *VSA, req *Request) { v.Subject[0].Digest["sha256"] = "deadbeef" },
			want:   ErrSubjectMismatch,
		},
		{
			name:   "request without digest never binds",
			mutate: func(v *VSA, req *Request) { req.Digest = nil },
			want:   ErrSubjectMismatch,
		},
		{
			name:   "resourceUri of another artifact",
			mutate: func(v *VSA, req *Request) { v.ResourceURI = "pkg:oci/other@sha256:abc" },
			want:   ErrResourceMismatch,
		},
		{
			name:   "verification failed",
			mutate: func(v *VSA, req *Request) { v.VerificationResult = "FAILED" },
			want:   ErrNotPassed,
		},
		{
			name:   "required level not verified",
			mutate: func(v *VSA, req *Request) { req.RequiredLevel = "SLSA_BUILD_LEVEL_4" },
			want:   ErrLevelMissing,
		},
		{
			name:   "policy relaxed since verification",
			mutate: func(v *VSA, req *Request) { v.Policy.Digest["sha256"] = "policyold" },
			want:   ErrPolicyMismatch,
		},
		{
			name:   "policy digest missing",
			mutate: func(v *VSA, req *Request) { v.Policy.Digest = nil },
			want:   ErrPolicyMismatch,
		},
		{
			name:   "older than 24h",
			mutate: func(v *VSA, req *Request) { v.TimeVerified = testNow.Add(-24*time.Hour - time.Minute) },
			want:   ErrStale,
		},
		{
			name:   "timestamp in the future",
			mutate: func(v *VSA, req *Request) { v.TimeVerified = testNow.Add(time.Hour) },
			want:   ErrStale,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, req := validVSA(), validRequest()
			tc.mutate(v, &req)
			err := admitErr(t, v, req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAdmitBoundary(t *testing.T) {
	v := validVSA()
	v.TimeVerified = testNow.Add(-24 * time.Hour)
	if err := admitErr(t, v, validRequest()); err != nil {
		t.Fatalf("exactly 24h old VSA should be admitted: %v", err)
	}
}

func TestSubjectMatchesAmongMultiple(t *testing.T) {
	v := validVSA()
	v.Subject = append([]Subject{{Name: "other", Digest: DigestSet{"sha256": "zzz"}}}, v.Subject...)
	if err := admitErr(t, v, validRequest()); err != nil {
		t.Fatalf("matching subject among several should admit: %v", err)
	}
}

func TestNewJudgeValidatesRules(t *testing.T) {
	if _, err := NewJudge(Rules{PolicyDigest: DigestSet{"sha256": "x"}}); err == nil {
		t.Fatal("empty trust list accepted")
	}
	if _, err := NewJudge(Rules{TrustedVerifierIDs: []string{"v"}}); err == nil {
		t.Fatal("empty policy digest accepted")
	}
	r := testRules()
	r.MaxAge = -time.Hour
	if _, err := NewJudge(r); err == nil {
		t.Fatal("negative MaxAge accepted")
	}
}

func TestNilVSA(t *testing.T) {
	if err := admitErr(t, nil, validRequest()); err == nil {
		t.Fatal("nil VSA admitted")
	}
}
