package netsec

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type constantReader byte

func (r constantReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func mustIssue(t *testing.T, s *Service, opts IssueOptions) *IssuedCode {
	t.Helper()

	issued, err := s.Issue(opts)
	if err != nil {
		t.Fatalf("Issue(%+v) failed: %v", opts, err)
	}

	return issued
}

func TestRedeemWithoutTxCode(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{})

	if issued.TxCodeRequired {
		t.Fatal("TxCodeRequired = true, want false")
	}
	if issued.TxCode != "" {
		t.Fatalf("TxCode = %q, want empty", issued.TxCode)
	}
	if err := s.Redeem(issued.Code, ""); err != nil {
		t.Fatalf("Redeem() failed: %v", err)
	}

	err := s.Redeem(issued.Code, "")
	if !errors.Is(err, ErrCodeAlreadyRedeemed) {
		t.Fatalf("second Redeem() error = %v, want ErrCodeAlreadyRedeemed", err)
	}
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("second Redeem() error = %v, want ErrInvalidGrant", err)
	}
}

func TestRedeemRequiresExactBoundTxCode(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{
		RequireTxCode: true,
		TxCode:        "123456",
	})

	if !issued.TxCodeRequired {
		t.Fatal("TxCodeRequired = false, want true")
	}
	if issued.TxCode != "123456" {
		t.Fatalf("TxCode = %q, want 123456", issued.TxCode)
	}

	err := s.Redeem(issued.Code, "")
	if !errors.Is(err, ErrTxCodeRequired) {
		t.Fatalf("missing tx_code error = %v, want ErrTxCodeRequired", err)
	}

	err = s.Redeem(issued.Code, "654321")
	if !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("wrong tx_code error = %v, want ErrTxCodeMismatch", err)
	}

	if err := s.Redeem(issued.Code, "123456"); err != nil {
		t.Fatalf("correct tx_code failed: %v", err)
	}
}

func TestTxCodeCannotBeReusedOnAnotherPreAuthorizedCode(t *testing.T) {
	s := NewService()
	first := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "111111"})
	second := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "222222"})

	if err := s.Redeem(first.Code, "222222"); !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("first code with second tx_code error = %v, want ErrTxCodeMismatch", err)
	}
	if err := s.Redeem(second.Code, "111111"); !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("second code with first tx_code error = %v, want ErrTxCodeMismatch", err)
	}

	if err := s.Redeem(first.Code, "111111"); err != nil {
		t.Fatalf("first code with its own tx_code failed: %v", err)
	}
	if err := s.Redeem(second.Code, "222222"); err != nil {
		t.Fatalf("second code with its own tx_code failed: %v", err)
	}
}

func TestSwapTxCodesMovesBindingWithTheCode(t *testing.T) {
	s := NewService()
	first := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "111111"})
	second := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "222222"})

	if err := s.SwapTxCodes(first.Code, second.Code); err != nil {
		t.Fatalf("SwapTxCodes() failed: %v", err)
	}

	if err := s.Redeem(first.Code, "111111"); !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("first code with old tx_code error = %v, want ErrTxCodeMismatch", err)
	}
	if err := s.Redeem(second.Code, "222222"); !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("second code with old tx_code error = %v, want ErrTxCodeMismatch", err)
	}

	if err := s.Redeem(first.Code, "222222"); err != nil {
		t.Fatalf("first code with swapped tx_code failed: %v", err)
	}
	if err := s.Redeem(second.Code, "111111"); err != nil {
		t.Fatalf("second code with swapped tx_code failed: %v", err)
	}
}

func TestSwapTxCodesIsAtomic(t *testing.T) {
	s := NewService()
	bound := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "111111"})
	unbound := mustIssue(t, s, IssueOptions{})

	if err := s.SwapTxCodes(bound.Code, unbound.Code); !errors.Is(err, ErrNoTxCodeBound) {
		t.Fatalf("SwapTxCodes() error = %v, want ErrNoTxCodeBound", err)
	}
	if err := s.SwapTxCodes(bound.Code, "missing"); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("SwapTxCodes() error = %v, want ErrCodeNotFound", err)
	}

	if err := s.Redeem(bound.Code, "111111"); err != nil {
		t.Fatalf("binding changed after failed swap: %v", err)
	}
	if err := s.Redeem(unbound.Code, ""); err != nil {
		t.Fatalf("unbound code changed after failed swap: %v", err)
	}
}

func TestSwapTxCodesRejectsRedeemedCode(t *testing.T) {
	s := NewService()
	first := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "111111"})
	second := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "222222"})

	if err := s.Redeem(first.Code, "111111"); err != nil {
		t.Fatalf("Redeem() failed: %v", err)
	}
	if err := s.SwapTxCodes(first.Code, second.Code); !errors.Is(err, ErrCodeAlreadyRedeemed) {
		t.Fatalf("SwapTxCodes() error = %v, want ErrCodeAlreadyRedeemed", err)
	}
	if err := s.Redeem(second.Code, "222222"); err != nil {
		t.Fatalf("second binding changed after failed swap: %v", err)
	}
}

func TestIssueGeneratesTxCodeWhenRequired(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{RequireTxCode: true})

	if len(issued.TxCode) != DefaultTxCodeDigits {
		t.Fatalf("generated TxCode length = %d, want %d", len(issued.TxCode), DefaultTxCodeDigits)
	}
	for _, r := range issued.TxCode {
		if r < '0' || r > '9' {
			t.Fatalf("generated TxCode = %q, want digits only", issued.TxCode)
		}
	}
	if err := s.Redeem(issued.Code, issued.TxCode); err != nil {
		t.Fatalf("Redeem() with generated TxCode failed: %v", err)
	}
}

func TestNonEmptyTxCodeImpliesRequirement(t *testing.T) {
	s := NewService()

	issued := mustIssue(t, s, IssueOptions{TxCode: "123456"})
	if !issued.TxCodeRequired {
		t.Fatal("TxCodeRequired = false, want true")
	}
	if err := s.Redeem(issued.Code, "123456"); err != nil {
		t.Fatalf("Redeem() failed: %v", err)
	}
}

func TestIssuePreAuthorizedCodeSemantics(t *testing.T) {
	s := NewService()

	direct, err := s.IssuePreAuthorizedCode(false, "")
	if err != nil {
		t.Fatalf("IssuePreAuthorizedCode(false, \"\") failed: %v", err)
	}
	if err := s.Redeem(direct, ""); err != nil {
		t.Fatalf("direct Redeem() failed: %v", err)
	}

	if _, err := s.IssuePreAuthorizedCode(true, ""); !errors.Is(err, ErrTxCodeRequired) {
		t.Fatalf("IssuePreAuthorizedCode(true, \"\") error = %v, want ErrTxCodeRequired", err)
	}
	if _, err := s.IssuePreAuthorizedCode(false, "123456"); !errors.Is(err, ErrTxCodeNotExpected) {
		t.Fatalf("IssuePreAuthorizedCode(false, code) error = %v, want ErrTxCodeNotExpected", err)
	}

	protected, err := s.IssuePreAuthorizedCode(true, "123456")
	if err != nil {
		t.Fatalf("IssuePreAuthorizedCode(true, code) failed: %v", err)
	}
	if err := s.Redeem(protected, "123456"); err != nil {
		t.Fatalf("protected Redeem() failed: %v", err)
	}
}

func TestUnknownCodeFails(t *testing.T) {
	s := NewService()

	err := s.Redeem("does-not-exist", "")
	if !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("Redeem() error = %v, want ErrCodeNotFound", err)
	}
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("Redeem() error = %v, want ErrInvalidGrant", err)
	}
}

func TestExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewService(Config{
		TTL: time.Minute,
		Now: func() time.Time { return now },
	})
	issued := mustIssue(t, s, IssueOptions{})

	now = now.Add(59 * time.Second)
	if err := s.Redeem(issued.Code, ""); err != nil {
		t.Fatalf("Redeem() before expiry failed: %v", err)
	}

	expired := mustIssue(t, s, IssueOptions{})
	now = now.Add(2 * time.Minute)
	if err := s.Redeem(expired.Code, ""); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("Redeem() after expiry error = %v, want ErrCodeExpired", err)
	}
}

func TestNegativeTTLDisablesExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := NewService(Config{
		TTL: -1,
		Now: func() time.Time { return now },
	})
	issued := mustIssue(t, s, IssueOptions{})
	if !issued.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v, want zero", issued.ExpiresAt)
	}

	now = now.Add(100 * 365 * 24 * time.Hour)
	if err := s.Redeem(issued.Code, ""); err != nil {
		t.Fatalf("non-expiring code failed: %v", err)
	}
}

func TestConcurrentRedeemConsumesCodeOnce(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "123456"})

	const goroutines = 64

	var successes atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := s.Redeem(issued.Code, "123456"); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful redemptions = %d, want 1", got)
	}
}

func TestVerifyTxCode(t *testing.T) {
	tests := []struct {
		name      string
		required  bool
		bound     string
		presented string
		wantErr   error
	}{
		{name: "not required", required: false, bound: "", presented: "", wantErr: nil},
		{name: "not required with value", required: false, bound: "", presented: "x", wantErr: nil},
		{name: "missing", required: true, bound: "123456", presented: "", wantErr: ErrTxCodeRequired},
		{name: "wrong", required: true, bound: "123456", presented: "654321", wantErr: ErrTxCodeMismatch},
		{name: "no binding", required: true, bound: "", presented: "123456", wantErr: ErrNoTxCodeBound},
		{name: "correct", required: true, bound: "123456", presented: "123456", wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyTxCode(tt.required, tt.bound, tt.presented)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyTxCode() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPackageLevelAPI(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	code, err := Issue("123456")
	if err != nil {
		t.Fatalf("Issue() failed: %v", err)
	}
	if err := Redeem(code, "000000"); !errors.Is(err, ErrTxCodeMismatch) {
		t.Fatalf("Redeem() error = %v, want ErrTxCodeMismatch", err)
	}
	if err := Redeem(code, "123456"); err != nil {
		t.Fatalf("Redeem() failed: %v", err)
	}

	direct, err := IssuePreAuthorizedCode(false, "")
	if err != nil {
		t.Fatalf("IssuePreAuthorizedCode() failed: %v", err)
	}
	if err := RedeemPreAuthorizedCode(direct, ""); err != nil {
		t.Fatalf("RedeemPreAuthorizedCode() failed: %v", err)
	}
}

func TestGeneratedCodesUseURLSafeAlphabet(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{})

	if strings.ContainsAny(issued.Code, "+/=") {
		t.Fatalf("generated code %q is not raw base64url", issued.Code)
	}
	if len(issued.Code) < 40 {
		t.Fatalf("generated code length = %d, want at least 40", len(issued.Code))
	}
}

func TestSuppliedCodeMustBeUnique(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{Code: "fixed-code"})
	if issued.Code != "fixed-code" {
		t.Fatalf("Code = %q, want fixed-code", issued.Code)
	}

	if _, err := s.Issue(IssueOptions{Code: "fixed-code"}); !errors.Is(err, ErrCodeExists) {
		t.Fatalf("duplicate Issue() error = %v, want ErrCodeExists", err)
	}
}

func TestDirectCodeIgnoresPresentedTxCode(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{})

	if err := s.Redeem(issued.Code, "not-bound"); err != nil {
		t.Fatalf("direct Redeem() with extra tx_code failed: %v", err)
	}
}

func TestGenerateTxCode(t *testing.T) {
	code, err := GenerateTxCode(8)
	if err != nil {
		t.Fatalf("GenerateTxCode() failed: %v", err)
	}
	if len(code) != 8 {
		t.Fatalf("GenerateTxCode() length = %d, want 8", len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("GenerateTxCode() = %q, want digits only", code)
		}
	}
}

func TestCustomLengths(t *testing.T) {
	s := NewService(Config{
		CodeLength:   16,
		TxCodeLength: 4,
	})

	issued := mustIssue(t, s, IssueOptions{RequireTxCode: true})
	if len(issued.Code) != 22 {
		t.Fatalf("generated code length = %d, want 22", len(issued.Code))
	}
	if len(issued.TxCode) != 4 {
		t.Fatalf("generated tx_code length = %d, want 4", len(issued.TxCode))
	}
}

func TestRandomSourceIsBounded(t *testing.T) {
	s := NewService(Config{Random: constantReader(255)})
	if _, err := s.Issue(IssueOptions{RequireTxCode: true}); !errors.Is(err, ErrRandomSource) {
		t.Fatalf("Issue() with unusable entropy error = %v, want ErrRandomSource", err)
	}
	if _, err := GenerateTxCode(6); err != nil {
		t.Fatalf("GenerateTxCode() with crypto/rand failed: %v", err)
	}
}

func TestCodeCollisionExhaustionFails(t *testing.T) {
	s := NewService(Config{Random: constantReader(0)})

	first := mustIssue(t, s, IssueOptions{})
	if first.Code == "" {
		t.Fatal("first generated code is empty")
	}
	if _, err := s.Issue(IssueOptions{}); !errors.Is(err, ErrRandomSource) {
		t.Fatalf("Issue() on exhausted entropy error = %v, want ErrRandomSource", err)
	}
}

func TestSwapSameCodeIsNoOp(t *testing.T) {
	s := NewService()
	issued := mustIssue(t, s, IssueOptions{RequireTxCode: true, TxCode: "123456"})

	if err := s.SwapTxCodes(issued.Code, issued.Code); err != nil {
		t.Fatalf("SwapTxCodes(same) failed: %v", err)
	}
	if err := s.Redeem(issued.Code, "123456"); err != nil {
		t.Fatalf("Redeem() after same-code swap failed: %v", err)
	}
}
