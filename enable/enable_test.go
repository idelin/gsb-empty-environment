package enable

import (
	"errors"
	"reflect"
	"testing"
)

const (
	rawUTF7Cafe = "caf&AOk-" // 修改版 UTF-7 写法
	rawUTF8Cafe = "café"     // UTF-8 写法
)

func newAuthedSession() *Session {
	s := NewSession([]string{"IMAP4rev1", "ENABLE", "UTF8=ACCEPT", "CONDSTORE"})
	s.SetState(StateAuthenticated)
	return s
}

func TestEnableUTF8Accept(t *testing.T) {
	s := newAuthedSession()
	got, err := s.Enable([]string{"UTF8=ACCEPT"})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"UTF8=ACCEPT"}) {
		t.Fatalf("ENABLED response = %v", got)
	}
	if !s.UTF8Accept() {
		t.Fatal("UTF8Accept not set after ENABLE")
	}
}

func TestEnableUnknownCapabilityIgnored(t *testing.T) {
	s := newAuthedSession()
	got, err := s.Enable([]string{"X-ATTACKER-INVENTED", "CONDSTORE", "utf8=accept"})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	want := []string{"CONDSTORE", "UTF8=ACCEPT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ENABLED response = %v, want %v", got, want)
	}
}

func TestEnableRejectedOutsideAuthenticatedState(t *testing.T) {
	for _, st := range []State{StateNotAuthenticated, StateSelected} {
		s := newAuthedSession()
		s.SetState(st)
		if _, err := s.Enable([]string{"UTF8=ACCEPT"}); !errors.Is(err, ErrBadState) {
			t.Fatalf("state %v: err = %v, want ErrBadState", st, err)
		}
		if s.UTF8Accept() {
			t.Fatalf("state %v: UTF8Accept must stay off", st)
		}
	}
}

func TestEnableAfterSelectDoesNotFlipDecoding(t *testing.T) {
	s := newAuthedSession()
	s.SetState(StateSelected)
	if _, err := s.Enable([]string{"UTF8=ACCEPT"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("err = %v, want ErrBadState", err)
	}
	name, err := s.CanonicalMailboxName(rawUTF7Cafe)
	if err != nil || name != rawUTF8Cafe {
		t.Fatalf("canonical = %q, %v; want %q", name, err, rawUTF8Cafe)
	}
}

func TestAuthorizeBeforeEnable(t *testing.T) {
	s := newAuthedSession()
	acl := NewACL(rawUTF8Cafe)

	ok, err := s.Authorize(acl, rawUTF7Cafe)
	if err != nil || !ok {
		t.Fatalf("UTF-7 spelling %q: ok=%v err=%v, want allowed", rawUTF7Cafe, ok, err)
	}
	// 未启用 UTF8=ACCEPT 时线上出现非 ASCII 字节即非法。
	if _, err := s.Authorize(acl, rawUTF8Cafe); err == nil {
		t.Fatal("raw UTF-8 bytes in UTF-7 mode must be rejected")
	}
}

func TestAuthorizeAfterEnable(t *testing.T) {
	s := newAuthedSession()
	if _, err := s.Enable([]string{"UTF8=ACCEPT"}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	acl := NewACL(rawUTF8Cafe)

	ok, err := s.Authorize(acl, rawUTF8Cafe)
	if err != nil || !ok {
		t.Fatalf("UTF-8 spelling %q: ok=%v err=%v, want allowed", rawUTF8Cafe, ok, err)
	}
	// 启用后 '&' 不再是 UTF-7 起始符，caf&AOk- 是另一个邮箱。
	ok, err = s.Authorize(acl, rawUTF7Cafe)
	if err != nil {
		t.Fatalf("literal ampersand name: %v", err)
	}
	if ok {
		t.Fatalf("%q must not map to %q after UTF8=ACCEPT", rawUTF7Cafe, rawUTF8Cafe)
	}
}

func TestBothSpellingsSameCanonicalBeforeEnable(t *testing.T) {
	s := newAuthedSession()
	got, err := s.CanonicalMailboxName(rawUTF7Cafe)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != rawUTF8Cafe {
		t.Fatalf("canonical = %q, want %q", got, rawUTF8Cafe)
	}
}

func TestDecodeUTF7(t *testing.T) {
	cases := map[string]string{
		"INBOX":          "INBOX",
		"caf&AOk-":       "café",
		"a&-b":           "a&b",
		"&ZeVnLIqe-":     "日本語",
		"~peter/mail/&U,BTFw-/&ZeVnLIqe-": "~peter/mail/台北/日本語",
	}
	for in, want := range cases {
		got, err := DecodeUTF7(in)
		if err != nil || got != want {
			t.Errorf("DecodeUTF7(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"caf&AOk", "caf\xC3\xA9", "a&+-b"} {
		if _, err := DecodeUTF7(bad); !errors.Is(err, ErrInvalidUTF7) {
			t.Errorf("DecodeUTF7(%q) err = %v, want ErrInvalidUTF7", bad, err)
		}
	}
}
