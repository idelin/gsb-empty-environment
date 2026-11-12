package netsec

import (
	"bytes"
	"errors"
	"testing"
)

const testRecipientType = "AGE-PLUGIN-TEST-1"

// fakeHost wires a session to an in-memory plugin transcript. pluginOut is
// what the hostile plugin "emits"; hostIn is what the host writes back to the
// plugin and is inspected to prove exactly which bytes were sent.
func fakeHost(t *testing.T, pluginOut string) (*Session, *bytes.Buffer) {
	t.Helper()
	in := bytes.NewBufferString(pluginOut)
	out := &bytes.Buffer{}
	session, err := NewSession(in, out, testRecipientType)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session, out
}

func greetStanza() string {
	s := &Stanza{Type: "greet", Args: []string{"v1"}}
	return mustWrite(s)
}

func mustWrite(stanza *Stanza) string {
	var buffer bytes.Buffer
	writer := &stanzaWriter{w: &buffer}
	if err := writer.WriteStanza(stanza); err != nil {
		panic(err)
	}
	return buffer.String()
}

func TestParsePluginName(t *testing.T) {
	cases := map[string]string{
		"AGE-PLUGIN-TEST-1":    "test",
		"AGE-PLUGIN-yubikey-1": "yubikey",
		"AGE-PLUGIN-a-1-b2-1":  "a-1-b2",
		"AGE-PLUGIN-123-1":     "123",
		"AGE-PLUGIN-a-b-c-1":   "a-b-c",
		"AGE-PLUGIN-EVIL-1":    "evil",
		"AGE-PLUGIN--evil-1":   "-evil",
	}
	for recipientType, want := range cases {
		got, err := ParsePluginName(recipientType)
		if err != nil {
			t.Errorf("ParsePluginName(%q) unexpected error: %v", recipientType, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePluginName(%q) = %q, want %q", recipientType, got, want)
		}
	}
}

func TestParsePluginNameRejectsInjection(t *testing.T) {
	injectionAttempts := []string{
		"AGE-PLUGIN-../bin/sh-1",
		"AGE-PLUGIN-./evil-1",
		"AGE-PLUGIN-/usr/bin/evil-1",
		"AGE-PLUGIN-evil;reboot-1",
		"AGE-PLUGIN-evil_name-1",
		"AGE-PLUGIN-evil name-1",
		"AGE-PLUGIN-evil\x00name-1",
		"AGE-PLUGIN--1",
		"AGE-PLUGIN-evil.name-1",
		"AGE-PLUGIN-evil~name-1",
		"BAD-PLUGIN-evil-1",
		"AGE-PLUGIN-evil-2",
		"AGE-PLUGIN-evil",
		"evIL-1",
		"",
	}
	for _, recipientType := range injectionAttempts {
		if name, err := ParsePluginName(recipientType); err == nil {
			t.Errorf("ParsePluginName(%q) = %q, want rejection", recipientType, name)
		} else if !errors.Is(err, ErrInvalidPluginName) {
			t.Errorf("ParsePluginName(%q) error = %v, want ErrInvalidPluginName", recipientType, err)
		}

		// A rejected recipient must never reach the handshake: no bytes may
		// be written to the plugin stream.
		in := bytes.NewBufferString(greetStanza())
		out := &bytes.Buffer{}
		if _, err := NewSession(in, out, recipientType); err == nil {
			t.Errorf("NewSession(%q) succeeded, want rejection", recipientType)
		}
		if out.Len() != 0 {
			t.Errorf("NewSession(%q) wrote %d bytes before rejecting the name", recipientType, out.Len())
		}
	}
}

func TestBinaryName(t *testing.T) {
	cases := map[string]string{
		"test":    "age-plugin-test",
		"yubikey": "age-plugin-yubikey",
		"a-b-2":   "age-plugin-a-b-2",
	}
	for name, want := range cases {
		got, err := BinaryName(name)
		if err != nil {
			t.Errorf("BinaryName(%q) unexpected error: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("BinaryName(%q) = %q, want %q", name, got, want)
		}
	}

	for _, name := range []string{"../bin/sh", "/usr/bin/evil", "EVIL", "bad_name", "", "a/b"} {
		if got, err := BinaryName(name); err == nil {
			t.Errorf("BinaryName(%q) = %q, want rejection", name, got)
		}
	}
}

func TestNewSessionValidatesAndMapsName(t *testing.T) {
	session, hostOut := fakeHost(t, greetStanza())
	if session.PluginName() != "test" {
		t.Errorf("PluginName = %q, want test", session.PluginName())
	}
	if session.BinaryName() != "age-plugin-test" {
		t.Errorf("BinaryName = %q, want age-plugin-test", session.BinaryName())
	}
	if session.RecipientType() != testRecipientType {
		t.Errorf("RecipientType = %q, want %q", session.RecipientType(), testRecipientType)
	}

	// The handshake answer must be exactly one greet stanza selecting v1.
	want := mustWrite(&Stanza{Type: "greet", Args: []string{"v1"}})
	if hostOut.String() != want {
		t.Errorf("handshake transcript = %q, want %q", hostOut.String(), want)
	}
}
