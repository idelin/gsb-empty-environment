package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

var testFileKey = []byte("0123456789abcdef") // 16 bytes

func wrapTranscript(s *Stanza) string {
	return greetStanza() + mustWrite(s) + mustWrite(&Stanza{Type: "done"})
}

func TestWrapFileKeySuccess(t *testing.T) {
	result := &Stanza{
		Type: "recipient-stanza",
		Args: []string{testRecipientType, "0"},
		Body: []byte("wrapped-key-material"),
	}
	session, hostOut := fakeHost(t, wrapTranscript(result))
	session.AddRecipient([]string{testRecipientType, "age1recipientdata"})

	results, err := session.WrapFileKey(testFileKey)
	if err != nil {
		t.Fatalf("WrapFileKey: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d result stanzas, want 1", len(results))
	}
	if results[0].Type != "recipient-stanza" || results[0].Args[0] != testRecipientType {
		t.Fatalf("unexpected result stanza: %+v", results[0])
	}
	if string(results[0].Body) != "wrapped-key-material" {
		t.Fatalf("body = %q, want wrapped-key-material", results[0].Body)
	}

	// The host must have sent add-recipient, wrap-file-key with the exact key,
	// and done, in that order.
	want := mustWrite(&Stanza{Type: "add-recipient", Args: []string{testRecipientType, "age1recipientdata"}}) +
		mustWrite(&Stanza{Type: "wrap-file-key", Body: testFileKey}) +
		mustWrite(&Stanza{Type: "done"})
	// Skip the handshake greet already written by NewSession.
	got := hostOut.String()[len(mustWrite(&Stanza{Type: "greet", Args: []string{"v1"}})):]
	if got != want {
		t.Errorf("wrap transcript = %q, want %q", got, want)
	}
}

func TestWrapFileKeyRejectsWrongSize(t *testing.T) {
	session, _ := fakeHost(t, wrapTranscript(&Stanza{
		Type: "recipient-stanza",
		Args: []string{testRecipientType},
	}))

	for _, size := range []int{0, 15, 17, 32} {
		key := make([]byte, size)
		if _, err := session.WrapFileKey(key); !errors.Is(err, ErrFileKeySize) {
			t.Errorf("WrapFileKey(%d bytes) error = %v, want ErrFileKeySize", size, err)
		}
	}
}

func TestWrapFileKeyRejectsTypeMismatch(t *testing.T) {
	mismatches := []*Stanza{
		{Type: "recipient-stanza", Args: []string{"AGE-PLUGIN-EVIL-1"}},
		{Type: "recipient-stanza", Args: []string{"AGE-PLUGIN-other-1", "0"}},
		{Type: "recipient-stanza"}, // missing type argument entirely
	}
	for index, stanza := range mismatches {
		t.Run(fmt.Sprintf("mismatch-%d", index), func(t *testing.T) {
			session, _ := fakeHost(t, wrapTranscript(stanza))
			session.AddRecipient([]string{testRecipientType})
			_, err := session.WrapFileKey(testFileKey)
			if !errors.Is(err, ErrRecipientType) {
				t.Fatalf("error = %v, want ErrRecipientType", err)
			}
		})
	}
}

func TestWrapFileKeyIgnoresUnknownStanzas(t *testing.T) {
	transcript := greetStanza() +
		mustWrite(&Stanza{Type: "extension-command", Args: []string{"ignored"}, Body: []byte("data")}) +
		mustWrite(&Stanza{Type: "recipient-stanza", Args: []string{testRecipientType, "0"}, Body: []byte("ok")}) +
		mustWrite(&Stanza{Type: "another-future-command"}) +
		mustWrite(&Stanza{Type: "done"})

	session, _ := fakeHost(t, transcript)
	results, err := session.WrapFileKey(testFileKey)
	if err != nil {
		t.Fatalf("WrapFileKey: %v", err)
	}
	if len(results) != 1 || string(results[0].Body) != "ok" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestWrapFileKeyPropagatesPluginError(t *testing.T) {
	transcript := greetStanza() +
		mustWrite(&Stanza{Type: "error", Body: []byte("internal failure")}) +
		mustWrite(&Stanza{Type: "done"})
	session, _ := fakeHost(t, transcript)
	_, err := session.WrapFileKey(testFileKey)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v, want ErrProtocol", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("internal failure")) {
		t.Fatalf("error message %q does not include plugin detail", err)
	}
}

func TestWrapFileKeyRejectsEmptyResponse(t *testing.T) {
	session, _ := fakeHost(t, greetStanza()+mustWrite(&Stanza{Type: "done"}))
	if _, err := session.WrapFileKey(testFileKey); !errors.Is(err, ErrProtocol) {
		t.Fatalf("error = %v, want ErrProtocol for empty response", err)
	}
}
