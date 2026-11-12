package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// RFC 8315 section 2.1 test vector. The hash input is the literal
// Base64-encoded key string.
const (
	rfcKey  = "yM0ep490Fzt83CLYYAytm3S2HasHhYG4LAeAlmuSEys="
	rfcLock = "NSBTz7BfcQFTCen+U4lQ0VS8VIlZao2b8mxD/xJaaeE="
)

func TestRFC8315Vector(t *testing.T) {
	headers, err := ParseHeader([]byte("Cancel-Lock: sha256:" + rfcLock + "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	locks, err := parseCancelElements(headers.Values("Cancel-Lock"))
	if err != nil {
		t.Fatal(err)
	}
	keys := []cancelElement{{scheme: "sha256", value: rfcKey}}
	if !verifyCancelKeys(keys, locks) {
		t.Fatal("RFC 8315 test vector did not verify")
	}
}

func TestFoldedControlInjectionRejected(t *testing.T) {
	article := strings.Join([]string{
		"From: attacker@example.com",
		"Subject: harmless",
		"X-Note: folded data",
		" Control: cancel <victim@example.com>",
		"Cancel-Key: sha256:" + rfcKey,
		"",
		"body",
	}, "\r\n")

	auth := newTestAuthorizer(t, map[string]Header{})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Reject || decision.Reason != ReasonFoldedControl {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestDuplicateControlRejected(t *testing.T) {
	article := strings.Join([]string{
		"From: attacker@example.com",
		"Control: cancel <victim@example.com>",
		"Control: cancel <other@example.com>",
		"Cancel-Key: sha256:" + rfcKey,
		"",
		"body",
	}, "\n")

	auth := newTestAuthorizer(t, map[string]Header{})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Reject || decision.Reason != ReasonDuplicateControl {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestWrongCancelKeyRejected(t *testing.T) {
	original := Header{
		"Cancel-Lock": {"sha256:" + rfcLock},
	}
	article := strings.Join([]string{
		"From: attacker@example.com",
		"Control: cancel <victim@example.com>",
		"Cancel-Key: sha256:" + lockForTest("wrong-secret"),
		"",
		"body",
	}, "\r\n")

	auth := newTestAuthorizer(t, map[string]Header{"<victim@example.com>": original})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Reject || decision.Reason != ReasonCancelKeyMismatch {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestNoLockRequiresAdministrator(t *testing.T) {
	original := Header{"Subject": {"ordinary article"}}

	t.Run("ordinary sender rejected", func(t *testing.T) {
		article := strings.Join([]string{
			"From: attacker@example.com",
			"Control: cancel <victim@example.com>",
			"Cancel-Key: sha256:" + rfcKey,
			"",
			"body",
		}, "\n")

		auth := newTestAuthorizer(t, map[string]Header{"<victim@example.com>": original})
		decision, err := auth.Authorize([]byte(article))
		if err != nil {
			t.Fatal(err)
		}
		if decision.Disposition != Reject || decision.Reason != ReasonNoLockAdminOnly {
			t.Fatalf("unexpected decision: %+v", decision)
		}
	})

	t.Run("administrator sender allowed", func(t *testing.T) {
		article := strings.Join([]string{
			"From: Moderator <moderator@example.com>",
			"Control: cancel <victim@example.com>",
			"Cancel-Key: sha256:" + rfcKey,
			"",
			"body",
		}, "\n")

		auth, err := NewAuthorizer(Config{
			SiteID:       "news.local.example",
			AdminSenders: []string{"moderator@example.com"},
		}, LookupFunc(func(messageID string) (Header, bool, error) {
			return original, messageID == "<victim@example.com>", nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		decision, err := auth.Authorize([]byte(article))
		if err != nil {
			t.Fatal(err)
		}
		if decision.Disposition != ApplyControl || decision.Reason != ReasonAdminSender {
			t.Fatalf("unexpected decision: %+v", decision)
		}
		if decision.Control != ControlCancel || decision.Target != "<victim@example.com>" {
			t.Fatalf("unexpected control details: %+v", decision)
		}
	})

	t.Run("authenticated identity prevents From spoofing", func(t *testing.T) {
		article := strings.Join([]string{
			"From: Moderator <moderator@example.com>",
			"Control: cancel <victim@example.com>",
			"Cancel-Key: sha256:" + rfcKey,
			"",
			"body",
		}, "\n")

		auth, err := NewAuthorizer(Config{
			SiteID:       "news.local.example",
			AdminSenders: []string{"moderator@example.com"},
		}, LookupFunc(func(messageID string) (Header, bool, error) {
			return original, messageID == "<victim@example.com>", nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		decision, err := auth.AuthorizeAs([]byte(article), "attacker@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Disposition != Reject || decision.Reason != ReasonNoLockAdminOnly {
			t.Fatalf("unexpected decision: %+v", decision)
		}
	})
}

func TestLegalCancelAllowed(t *testing.T) {
	original := Header{
		"Cancel-Lock": {"sha256:" + rfcLock},
	}
	// The Control value is folded across two physical lines to exercise
	// unfolding and re-normalization.
	article := strings.Join([]string{
		"From: author@example.com",
		"Control: cancel",
		" <victim@example.com>",
		"Cancel-Key: sha256:" + rfcKey,
		"",
		"body",
	}, "\r\n")

	auth := newTestAuthorizer(t, map[string]Header{"<victim@example.com>": original})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != ApplyControl || decision.Reason != ReasonMatchedCancelKey {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if decision.Control != ControlCancel || decision.Target != "<victim@example.com>" {
		t.Fatalf("unexpected control details: %+v", decision)
	}
}

func TestCancelWithoutKeyRejected(t *testing.T) {
	article := strings.Join([]string{
		"From: author@example.com",
		"Control: cancel <victim@example.com>",
		"",
		"body",
	}, "\n")

	auth := newTestAuthorizer(t, map[string]Header{
		"<victim@example.com>": {"Cancel-Lock": {"sha256:" + rfcLock}},
	})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Reject || decision.Reason != ReasonMissingCancelKey {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestLegalSupersedesAllowed(t *testing.T) {
	original := Header{"Cancel-Lock": {"sha256:" + rfcLock}}
	article := strings.Join([]string{
		"From: author@example.com",
		"Supersedes: <victim@example.com>",
		"Cancel-Key: sha256:" + rfcKey,
		"",
		"replacement body",
	}, "\n")

	auth := newTestAuthorizer(t, map[string]Header{"<victim@example.com>": original})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != ApplyControl || decision.Control != ControlSupersedes {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestLoopbackPathDropped(t *testing.T) {
	article := strings.Join([]string{
		"Path: upstream.example!{news.local.example.}!not-for-mail",
		"From: user@example.com",
		"Subject: ordinary",
		"",
		"body",
	}, "\r\n")

	auth := newTestAuthorizer(t, map[string]Header{})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Drop || decision.Reason != ReasonLoopback {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestOrdinaryArticleDelivered(t *testing.T) {
	article := "From: user@example.com\r\nSubject: hello\r\n\r\nbody"
	auth := newTestAuthorizer(t, map[string]Header{})
	decision, err := auth.Authorize([]byte(article))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != Deliver {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func newTestAuthorizer(t *testing.T, originals map[string]Header) *Authorizer {
	t.Helper()
	auth, err := NewAuthorizer(Config{SiteID: "news.local.example"}, LookupFunc(
		func(messageID string) (Header, bool, error) {
			header, ok := originals[messageID]
			return header, ok, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func lockForTest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.StdEncoding.EncodeToString(sum[:])
}
