package netsec

import (
	"errors"
	"net/mail"
	"strings"
	"testing"
)

// A CRLF plus a Bcc header smuggled after the display name must fail
// the To field instead of becoming a new header.
func TestAdjudicateRejectsInjectedBcc(t *testing.T) {
	h := mail.Header{}
	h["To"] = []string{"\"Alice\" <alice@example.com>\r\nBcc: spy@evil.example"}
	h["From"] = []string{"bob@example.com"}

	v := Adjudicate(h)

	if len(v.Failed) != 1 {
		t.Fatalf("expected 1 failed field, got %d", len(v.Failed))
	}
	fe := v.Failed[0]
	if fe.Field != "To" {
		t.Errorf("failed field = %q, want To", fe.Field)
	}
	if !errors.Is(fe.Err, ErrNewlineInAddressField) {
		t.Errorf("error = %v, want ErrNewlineInAddressField", fe.Err)
	}
	if _, ok := v.Kept["To"]; ok {
		t.Error("injected To field must not be kept")
	}
	if _, ok := v.Kept["Bcc"]; ok {
		t.Error("smuggled Bcc must not become a kept header")
	}
	if got := v.Kept["From"]; len(got) != 1 || got[0].Address != "bob@example.com" {
		t.Errorf("clean From not kept: %v", got)
	}
}

// An address list without newlines is preserved.
func TestAdjudicateKeepsCleanAddressList(t *testing.T) {
	h := mail.Header{}
	h["To"] = []string{"\"Bob\" <bob@example.com>, carol@example.com"}
	h["Cc"] = []string{"dave@example.com"}

	v := Adjudicate(h)

	if len(v.Failed) != 0 {
		t.Fatalf("unexpected failures: %v", v.Failed)
	}
	to := v.Kept["To"]
	if len(to) != 2 {
		t.Fatalf("kept To has %d addresses, want 2", len(to))
	}
	if to[0].Name != "Bob" || to[0].Address != "bob@example.com" {
		t.Errorf("to[0] = %v", to[0])
	}
	if to[1].Address != "carol@example.com" {
		t.Errorf("to[1] = %v", to[1])
	}
	if len(v.Kept["Cc"]) != 1 {
		t.Errorf("kept Cc = %v", v.Kept["Cc"])
	}
}

// A lone CR or LF anywhere in the value also fails the field.
func TestAdjudicateRejectsLoneCRAndLF(t *testing.T) {
	h := mail.Header{}
	h["To"] = []string{"alice@example.com\rbcc@evil.example"}
	h["Cc"] = []string{"alice@example.com\nbcc@evil.example"}

	v := Adjudicate(h)

	if len(v.Failed) != 2 {
		t.Fatalf("expected 2 failed fields, got %d", len(v.Failed))
	}
	for _, fe := range v.Failed {
		if !errors.Is(fe.Err, ErrNewlineInAddressField) {
			t.Errorf("%s: error = %v, want ErrNewlineInAddressField", fe.Field, fe.Err)
		}
	}
}

// Reading a full message adjudicates its address fields: the folded
// To continuation fails, the clean From is kept.
func TestReadMessage(t *testing.T) {
	raw := "To: \"Alice\" <alice@example.com>\r\n" +
		"\tBcc: spy@evil.example\r\n" +
		"From: bob@example.com\r\n" +
		"Subject: hello\r\n" +
		"\r\n" +
		"body\r\n"

	msg, v, err := ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "hello" {
		t.Errorf("Subject = %q", got)
	}
	if len(v.Failed) != 1 || v.Failed[0].Field != "To" {
		t.Fatalf("failed fields = %v, want only To", v.Failed)
	}
	if !errors.Is(v.Failed[0].Err, ErrNewlineInAddressField) {
		t.Errorf("error = %v, want ErrNewlineInAddressField", v.Failed[0].Err)
	}
	if got := v.Kept["From"]; len(got) != 1 || got[0].Address != "bob@example.com" {
		t.Errorf("clean From not kept: %v", got)
	}
}
