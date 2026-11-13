package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestFiniteDifferenceSearchFindsTokenOnlyKey(t *testing.T) {
	finding, err := SearchWeakCacheKey(TokenOnlyWeakKey, AuditOptions{MaxTokens: 2})
	if err != nil {
		t.Fatalf("SearchWeakCacheKey: %v", err)
	}
	if finding == nil {
		t.Fatal("expected a finding for a token-only cache key")
	}
	if finding.TokenLength != 1 {
		t.Fatalf("expected the shortest token length 1, got %d", finding.TokenLength)
	}
	if finding.Writer.TenantID == finding.Reader.TenantID {
		t.Fatalf("finding is not cross-tenant: %q vs %q", finding.Writer.TenantID, finding.Reader.TenantID)
	}
	reason := finding.Reason()
	if !bytes.Contains([]byte(reason), []byte("system prompt digest")) {
		t.Fatalf("reason %q does not mention system prompt digest", reason)
	}
	decoded, err := DecodeMarkedKV(finding.MarkedKV)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.TenantID != finding.Writer.TenantID {
		t.Fatalf("marked KV owner = %q, writer = %q", decoded.TenantID, finding.Writer.TenantID)
	}
	if decoded.SecretToken != finding.LeakedToken {
		t.Fatalf("marked KV secret = %d, finding leak = %d", decoded.SecretToken, finding.LeakedToken)
	}
	if decoded.TenantID == finding.Reader.TenantID {
		t.Fatal("decoded KV belongs to the reader, not another tenant")
	}
}

func TestAuditSafeKeyHasNoFinding(t *testing.T) {
	finding, err := SearchWeakCacheKey(SafeAuditKey, AuditOptions{})
	if err != nil {
		t.Fatalf("SearchWeakCacheKey: %v", err)
	}
	if finding != nil {
		t.Fatalf("safe key produced a finding: %+v", finding)
	}
}

func TestAuditConstantKeyFindsCrossTenantLeak(t *testing.T) {
	constant := func(AuditRequest) string { return "constant" }
	finding, err := SearchWeakCacheKey(constant, AuditOptions{MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil || finding.LeakedToken == 0 {
		t.Fatalf("constant key finding = %+v", finding)
	}
}

func TestAuditTenantBindingAloneCannotCrossTenants(t *testing.T) {
	tenantOnly := func(r AuditRequest) string { return r.TenantID }
	finding, err := SearchWeakCacheKey(tenantOnly, AuditOptions{MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if finding != nil {
		t.Fatalf("tenant-only key produced a cross-tenant finding: %+v", finding)
	}
}

func TestAuditCallbackPanic(t *testing.T) {
	panicky := func(AuditRequest) string { panic("boom") }
	if _, err := SearchWeakCacheKey(panicky, AuditOptions{MaxTokens: 1}); !errors.Is(err, ErrAuditCallbackPanic) {
		t.Fatalf("panic error = %v, want ErrAuditCallbackPanic", err)
	}
}

func TestMarkedKVRoundTrip(t *testing.T) {
	r := AuditRequest{
		TenantID:               "tenant-a",
		ModelDigest:            "model",
		AdapterDigest:          "adapter",
		TokenizerDigest:        "tokenizer",
		DecodePosition:         7,
		Tokens:                 []Token{1, 2, 3},
		SystemPromptDigest:     promptHash("prompt"),
		SafetyPolicyGeneration: 9,
		KVLayoutVersion:        3,
		PublicScope:            "",
		SecretToken:            1234,
	}
	got, err := DecodeMarkedKV(EncodeMarkedKV(r))
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != r.TenantID || got.SecretToken != r.SecretToken ||
		got.DecodePosition != r.DecodePosition || got.KVLayoutVersion != r.KVLayoutVersion ||
		got.SystemPromptDigest != r.SystemPromptDigest || !tokensEqual(got.Tokens, r.Tokens) {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, r)
	}
}
