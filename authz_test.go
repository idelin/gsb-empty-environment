package netsec

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testRequest() AuthorizationRequest {
	return AuthorizationRequest{
		RequesterID:   "analyst-17",
		SubjectID:     "consumer-42",
		Purpose:       "loan-review",
		InstitutionID: "bank-9",
		MaxQueries:    2,
	}
}

func testQuery(token string) QueryRequest {
	req := testRequest()
	return QueryRequest{
		Token:         token,
		RequesterID:   req.RequesterID,
		SubjectID:     req.SubjectID,
		Purpose:       req.Purpose,
		InstitutionID: req.InstitutionID,
	}
}

func mustAuthorize(t *testing.T, m *Manager) (Authorization, string) {
	t.Helper()
	auth, token, err := m.AuthorizeWithGrant(testRequest())
	if err != nil {
		t.Fatalf("AuthorizeWithGrant() error = %v", err)
	}
	return auth, token
}

func TestLegalQueryOnlyReturnsBoundSubject(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	m := New(WithClock(func() time.Time { return now }))

	auth, token := mustAuthorize(t, m)
	decision, err := m.AuthorizeQuery(testQuery(token))
	if err != nil {
		t.Fatalf("AuthorizeQuery() error = %v", err)
	}
	if decision.SubjectID != "consumer-42" {
		t.Fatalf("decision subject = %q, want bound subject", decision.SubjectID)
	}
	if decision.RequesterID != "analyst-17" ||
		decision.Purpose != "loan-review" ||
		decision.InstitutionID != "bank-9" {
		t.Fatalf("decision contains unexpected scope: %+v", decision)
	}
	if decision.UsedQueries != 1 || decision.Remaining != 1 {
		t.Fatalf("usage = %d/%d, want 1 used and 1 remaining",
			decision.UsedQueries, decision.Remaining)
	}

	stored, ok := m.Authorization(auth.ID)
	if !ok || stored.UsedQueries != 1 || stored.MaxQueries != 2 {
		t.Fatalf("stored authorization = %+v, found=%v", stored, ok)
	}
	if err := m.VerifyAuditLog(); err != nil {
		t.Fatalf("VerifyAuditLog() error = %v", err)
	}
}

func TestZeroValueManagerAndEmptyAuditLog(t *testing.T) {
	var m Manager
	if err := m.VerifyAuditLog(); err != nil {
		t.Fatalf("empty zero-value audit log verification error = %v", err)
	}
	if _, err := m.AuthorizeQuery(testQuery("unknown")); !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("zero-value manager query error = %v, want ErrTokenMissing", err)
	}
}

func TestTokenReuseIsRejectedAtomically(t *testing.T) {
	m := New()
	auth, token := mustAuthorize(t, m)
	if _, err := m.AuthorizeQuery(testQuery(token)); err != nil {
		t.Fatalf("first query error = %v", err)
	}

	if _, err := m.AuthorizeQuery(testQuery(token)); !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("second query error = %v, want ErrTokenUsed", err)
	}
	stored, _ := m.Authorization(auth.ID)
	if stored.UsedQueries != 1 {
		t.Fatalf("used queries = %d after rejected reuse, want 1", stored.UsedQueries)
	}

	const goroutines = 32
	_, concurrentToken := mustAuthorizeWithMax(t, m, 1)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.AuthorizeQuery(testQuery(concurrentToken)); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrTokenUsed) {
				t.Errorf("concurrent query error = %v, want ErrTokenUsed", err)
			}
		}()
	}
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful uses of one token = %d, want 1", got)
	}
}

func mustAuthorizeWithMax(t *testing.T, m *Manager, max int) (Authorization, string) {
	t.Helper()
	req := testRequest()
	req.MaxQueries = max
	auth, token, err := m.AuthorizeWithGrant(req)
	if err != nil {
		t.Fatalf("AuthorizeWithGrant() error = %v", err)
	}
	return auth, token
}

func TestCrossSubjectAndPurposeMismatchAreDenied(t *testing.T) {
	m := New()
	auth, token := mustAuthorize(t, m)

	crossSubject := testQuery(token)
	crossSubject.SubjectID = "consumer-99"
	if _, err := m.AuthorizeQuery(crossSubject); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("cross-subject error = %v, want ErrSubjectMismatch", err)
	}

	wrongPurpose := testQuery(token)
	wrongPurpose.Purpose = "marketing"
	if _, err := m.AuthorizeQuery(wrongPurpose); !errors.Is(err, ErrPurposeMismatch) {
		t.Fatalf("wrong-purpose error = %v, want ErrPurposeMismatch", err)
	}

	wrongRequester := testQuery(token)
	wrongRequester.RequesterID = "analyst-other"
	if _, err := m.AuthorizeQuery(wrongRequester); !errors.Is(err, ErrRequesterMismatch) {
		t.Fatalf("wrong-requester error = %v, want ErrRequesterMismatch", err)
	}

	wrongInstitution := testQuery(token)
	wrongInstitution.InstitutionID = "bank-other"
	if _, err := m.AuthorizeQuery(wrongInstitution); !errors.Is(err, ErrInstitutionMismatch) {
		t.Fatalf("wrong-institution error = %v, want ErrInstitutionMismatch", err)
	}

	// Denials must not burn the one-time token.
	decision, err := m.AuthorizeQuery(testQuery(token))
	if err != nil {
		t.Fatalf("authorized query after denied attempts error = %v", err)
	}
	if decision.SubjectID != auth.SubjectID {
		t.Fatalf("decision subject = %q, want %q", decision.SubjectID, auth.SubjectID)
	}

	entries := m.AuditEntries()
	var sawRequestedSubject bool
	for _, entry := range entries {
		if entry.Action == AuditQueryDenied && entry.RequestedSubjectID == "consumer-99" {
			sawRequestedSubject = true
			if entry.SubjectID != auth.SubjectID {
				t.Fatalf("audit bound subject = %q, want %q", entry.SubjectID, auth.SubjectID)
			}
		}
	}
	if !sawRequestedSubject {
		t.Fatal("denied cross-subject attempt was not captured in the audit ledger")
	}
}

func TestDefaultQueryCountIsOne(t *testing.T) {
	m := New()
	req := testRequest()
	req.MaxQueries = 0
	auth, token := mustAuthorizeWithRequest(t, m, req)
	if auth.MaxQueries != 1 {
		t.Fatalf("default max queries = %d, want 1", auth.MaxQueries)
	}
	if _, err := m.AuthorizeQuery(testQuery(token)); err != nil {
		t.Fatalf("first query error = %v", err)
	}
	second, err := m.IssueToken(auth.ID)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	if _, err := m.AuthorizeQuery(testQuery(second)); !errors.Is(err, ErrQueryLimitExceeded) {
		t.Fatalf("second query error = %v, want ErrQueryLimitExceeded", err)
	}
}

func mustAuthorizeWithRequest(t *testing.T, m *Manager, req AuthorizationRequest) (Authorization, string) {
	t.Helper()
	auth, token, err := m.AuthorizeWithGrant(req)
	if err != nil {
		t.Fatalf("AuthorizeWithGrant() error = %v", err)
	}
	return auth, token
}

func TestQueryLimitCannotBeExpanded(t *testing.T) {
	m := New()
	req := testRequest()
	req.MaxQueries = 2
	auth, err := m.CreateAuthorization(req)
	if err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}

	tokens := make([]string, 3)
	for i := range tokens {
		tokens[i], err = m.IssueToken(auth.ID)
		if err != nil {
			t.Fatalf("IssueToken(%d) error = %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := m.AuthorizeQuery(testQuery(tokens[i])); err != nil {
			t.Fatalf("query %d error = %v", i, err)
		}
	}
	if _, err := m.AuthorizeQuery(testQuery(tokens[2])); !errors.Is(err, ErrQueryLimitExceeded) {
		t.Fatalf("over-limit error = %v, want ErrQueryLimitExceeded", err)
	}
	stored, _ := m.Authorization(auth.ID)
	if stored.UsedQueries != 2 || stored.MaxQueries != 2 {
		t.Fatalf("usage = %d/%d, want 2/2", stored.UsedQueries, stored.MaxQueries)
	}

	// The over-limit token was not consumed, but it still cannot get a report.
	if _, err := m.AuthorizeQuery(testQuery(tokens[2])); !errors.Is(err, ErrQueryLimitExceeded) {
		t.Fatalf("retry over-limit error = %v, want ErrQueryLimitExceeded", err)
	}
}

func TestConcurrentQueriesCannotExceedBudget(t *testing.T) {
	const budget = 5
	m := New()
	req := testRequest()
	req.MaxQueries = budget
	auth, err := m.CreateAuthorization(req)
	if err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}

	tokens := make([]string, 40)
	for i := range tokens {
		tokens[i], err = m.IssueToken(auth.ID)
		if err != nil {
			t.Fatalf("IssueToken(%d) error = %v", i, err)
		}
	}

	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			if _, err := m.AuthorizeQuery(testQuery(token)); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrQueryLimitExceeded) {
				t.Errorf("query error = %v, want ErrQueryLimitExceeded", err)
			}
		}(token)
	}
	wg.Wait()
	if got := successes.Load(); got != budget {
		t.Fatalf("successful queries = %d, want %d", got, budget)
	}
	stored, _ := m.Authorization(auth.ID)
	if stored.UsedQueries != budget {
		t.Fatalf("used queries = %d, want %d", stored.UsedQueries, budget)
	}
}

func TestRevocationInvalidatesGrantAndOutstandingTokens(t *testing.T) {
	m := New()
	auth, token := mustAuthorize(t, m)
	if err := m.RevokeAuthorization(auth.ID); err != nil {
		t.Fatalf("RevokeAuthorization() error = %v", err)
	}
	if _, err := m.AuthorizeQuery(testQuery(token)); !errors.Is(err, ErrAuthorizationRevoked) {
		t.Fatalf("query after revocation error = %v, want ErrAuthorizationRevoked", err)
	}
	if _, err := m.IssueToken(auth.ID); !errors.Is(err, ErrAuthorizationRevoked) {
		t.Fatalf("issue after revocation error = %v, want ErrAuthorizationRevoked", err)
	}
	if err := m.RevokeAuthorization(auth.ID); err != nil {
		t.Fatalf("idempotent revocation error = %v", err)
	}

	other, err := m.CreateAuthorization(testRequest())
	if err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}
	first, _ := m.IssueToken(other.ID)
	second, _ := m.IssueToken(other.ID)
	if err := m.RevokeToken(first); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}
	if _, err := m.AuthorizeQuery(testQuery(first)); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked token error = %v, want ErrTokenRevoked", err)
	}
	if _, err := m.AuthorizeQuery(testQuery(second)); err != nil {
		t.Fatalf("unrelated token was affected by token revocation: %v", err)
	}
}

func TestTokenAndGrantExpiry(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	m := New(
		WithClock(func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}),
		WithTokenTTL(time.Minute),
		WithAuthorizationTTL(3*time.Minute),
	)

	auth, token := mustAuthorize(t, m)
	clockMu.Lock()
	now = now.Add(time.Minute)
	clockMu.Unlock()
	if _, err := m.AuthorizeQuery(testQuery(token)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrTokenExpired", err)
	}

	newToken, err := m.IssueToken(auth.ID)
	if err != nil {
		t.Fatalf("IssueToken() before grant expiry error = %v", err)
	}
	clockMu.Lock()
	now = now.Add(2 * time.Minute)
	clockMu.Unlock()
	if _, err := m.AuthorizeQuery(testQuery(newToken)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("token at grant expiry error = %v, want ErrTokenExpired", err)
	}
	if _, err := m.IssueToken(auth.ID); !errors.Is(err, ErrAuthorizationExpired) {
		t.Fatalf("issue after grant expiry error = %v, want ErrAuthorizationExpired", err)
	}
}

func TestAuditLogIsAppendOnlyAndTamperEvident(t *testing.T) {
	m := New()
	_, token := mustAuthorize(t, m)
	if _, err := m.AuthorizeQuery(testQuery(token)); err != nil {
		t.Fatalf("AuthorizeQuery() error = %v", err)
	}
	if err := m.VerifyAuditLog(); err != nil {
		t.Fatalf("VerifyAuditLog() error = %v", err)
	}

	entries := m.AuditEntries()
	if len(entries) < 3 {
		t.Fatalf("audit entries = %d, want at least 3", len(entries))
	}
	entries[0].SubjectID = "tampered-copy"
	if err := m.VerifyAuditLog(); err != nil {
		t.Fatalf("mutating returned copy changed ledger: %v", err)
	}

	encoded, err := json.Marshal(m.AuditEntries())
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(encoded), token) {
		t.Fatal("raw authorization token was written to the audit log")
	}

	// White-box tampering confirms that modification of a retained record is
	// detected by the hash chain.
	m.audit[1].SubjectID = "consumer-other"
	if err := m.VerifyAuditLog(); !errors.Is(err, ErrAuditTampered) {
		t.Fatalf("VerifyAuditLog() after tampering error = %v, want ErrAuditTampered", err)
	}
}

func TestAuditLogDetectsTruncation(t *testing.T) {
	m := New()
	_, token := mustAuthorize(t, m)
	if _, err := m.AuthorizeQuery(testQuery(token)); err != nil {
		t.Fatalf("AuthorizeQuery() error = %v", err)
	}
	m.audit = m.audit[:len(m.audit)-1]
	if err := m.VerifyAuditLog(); !errors.Is(err, ErrAuditTampered) {
		t.Fatalf("VerifyAuditLog() after truncation error = %v, want ErrAuditTampered", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
