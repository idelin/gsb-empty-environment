package correlation

import (
	"errors"
	"testing"
	"time"
)

const testACS = "https://sp.example.com/acs"

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func newValidator(cfg Config) *Validator {
	if cfg.ACSURL == "" {
		cfg.ACSURL = testACS
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return testNow }
	}
	return New(cfg)
}

func validResponse() Response {
	return Response{
		InResponseTo: "req-1",
		AssertionID:  "assertion-1",
		Recipient:    testACS,
		NotOnOrAfter: testNow.Add(5 * time.Minute),
	}
}

func TestValidSPInitiatedResponse(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	if err := v.Validate(validResponse()); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestReplaySameResponseRejected(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	resp := validResponse()
	if err := v.Validate(resp); err != nil {
		t.Fatalf("first Validate() = %v, want nil", err)
	}
	// 同一响应在另一会话重放：登记项已被消费。
	if err := v.Validate(resp); !errors.Is(err, ErrUnknownRequestID) {
		t.Fatalf("replayed Validate() = %v, want ErrUnknownRequestID", err)
	}
}

func TestAssertionReplayWithFreshRequestRejected(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	if err := v.Validate(validResponse()); err != nil {
		t.Fatalf("first Validate() = %v, want nil", err)
	}
	// 攻击者持有新的合法请求 ID，但断言 ID 已见。
	v.RegisterRequest("req-2", testNow.Add(5*time.Minute))
	resp := validResponse()
	resp.InResponseTo = "req-2"
	if err := v.Validate(resp); !errors.Is(err, ErrAssertionReplayed) {
		t.Fatalf("Validate() = %v, want ErrAssertionReplayed", err)
	}
}

func TestWrongInResponseToRejected(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	resp := validResponse()
	resp.InResponseTo = "req-999"
	if err := v.Validate(resp); !errors.Is(err, ErrUnknownRequestID) {
		t.Fatalf("Validate() = %v, want ErrUnknownRequestID", err)
	}
}

func TestExpiredRegistrationRejected(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(-time.Minute))
	if err := v.Validate(validResponse()); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("Validate() = %v, want ErrRequestExpired", err)
	}
}

func TestIDPInitiatedRejectedByDefault(t *testing.T) {
	v := newValidator(Config{})
	resp := validResponse()
	resp.InResponseTo = ""
	if err := v.Validate(resp); !errors.Is(err, ErrIDPInitiatedNotAllowed) {
		t.Fatalf("Validate() = %v, want ErrIDPInitiatedNotAllowed", err)
	}
}

func TestIDPInitiatedAllowedWhenConfigured(t *testing.T) {
	v := newValidator(Config{AllowIDPInitiated: true})
	resp := validResponse()
	resp.InResponseTo = ""
	if err := v.Validate(resp); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	// IdP 发起的断言同样受重放保护。
	if err := v.Validate(resp); !errors.Is(err, ErrAssertionReplayed) {
		t.Fatalf("replayed Validate() = %v, want ErrAssertionReplayed", err)
	}
}

func TestRecipientMismatchRejected(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	resp := validResponse()
	resp.Recipient = "https://attacker.example.com/acs"
	if err := v.Validate(resp); !errors.Is(err, ErrRecipientMismatch) {
		t.Fatalf("Validate() = %v, want ErrRecipientMismatch", err)
	}
}

func TestNotOnOrAfterClockSkew(t *testing.T) {
	v := newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	// 过期 30 秒，在 60 秒偏差内，接受。
	resp := validResponse()
	resp.NotOnOrAfter = testNow.Add(-30 * time.Second)
	if err := v.Validate(resp); err != nil {
		t.Fatalf("Validate() = %v, want nil (within clock skew)", err)
	}

	v = newValidator(Config{})
	v.RegisterRequest("req-1", testNow.Add(5*time.Minute))
	// 过期 61 秒，超出偏差，拒绝。
	resp = validResponse()
	resp.NotOnOrAfter = testNow.Add(-61 * time.Second)
	if err := v.Validate(resp); !errors.Is(err, ErrAssertionExpired) {
		t.Fatalf("Validate() = %v, want ErrAssertionExpired", err)
	}
}
