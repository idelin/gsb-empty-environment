package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteStanzaRejectsBadHeaders(t *testing.T) {
	bad := []*Stanza{
		{Type: ""},
		{Type: "has space"},
		{Type: "ok", Args: []string{""}},
		{Type: "ok", Args: []string{"bad arg"}},
	}
	for _, stanza := range bad {
		if err := (&stanzaWriter{w: io.Discard}).WriteStanza(stanza); !errors.Is(err, ErrProtocol) {
			t.Fatalf("WriteStanza(%+v) error = %v, want ErrProtocol", stanza, err)
		}
	}
}

func TestWriteAndReadRoundTrip(t *testing.T) {
	original := &Stanza{
		Type: "wrap-file-key",
		Args: []string{"x", "y"},
		Body: bytes.Repeat([]byte{0xAB}, 200), // forces multiple 64-col body lines
	}
	var buffer bytes.Buffer
	writer := &stanzaWriter{w: &buffer}
	if err := writer.WriteStanza(original); err != nil {
		t.Fatal(err)
	}

	reader := newStanzaReader(&buffer)
	reader.resetLimit(4096)
	decoded, err := reader.ReadStanza()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != original.Type || !bytes.Equal(decoded.Body, original.Body) ||
		len(decoded.Args) != 2 || decoded.Args[0] != "x" || decoded.Args[1] != "y" {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}

func TestReadStanzaRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"truncated body": "wrap-file-key\nAAAA\n",
		"bad base64":     "done\n####\n\n",
		"empty header":   "\n\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			reader := newStanzaReader(strings.NewReader(input))
			reader.resetLimit(4096)
			if _, err := reader.ReadStanza(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestReadLineHandlesCRLFAndLongLines(t *testing.T) {
	// CRLF input must be accepted with the CR stripped.
	reader := newStanzaReader(strings.NewReader("greet v1\r\n\r\n"))
	reader.resetLimit(4096)
	line, err := reader.readLine()
	if err != nil || string(line) != "greet v1" {
		t.Fatalf("line = %q, err = %v", line, err)
	}

	// A line longer than the bufio buffer must still be reassembled, and the
	// budget is enforced on consumed wire bytes rather than ReadSlice chunks.
	long := strings.Repeat("A", bufio.MaxScanTokenSize+100) + "\n\n"
	limited := newStanzaReader(strings.NewReader(long))
	limited.resetLimit(128)
	if _, err := limited.readLine(); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v, want ErrResponseTooLarge", err)
	}
}

func TestFlushSupportsBufferedWriters(t *testing.T) {
	session := &Session{rawWriter: bufio.NewWriter(io.Discard)}
	if err := session.flush(); err != nil {
		t.Fatalf("flush buffered: %v", err)
	}
	session.rawWriter = &bytes.Buffer{}
	if err := session.flush(); err != nil {
		t.Fatalf("flush unbuffered: %v", err)
	}
}

func TestPluginErrorFallbacks(t *testing.T) {
	if err := pluginError(&Stanza{}); !errors.Is(err, ErrProtocol) ||
		!strings.Contains(err.Error(), "unspecified plugin error") {
		t.Fatalf("unexpected empty error: %v", err)
	}
	if err := pluginError(&Stanza{Args: []string{"arg-message"}}); !strings.Contains(err.Error(), "arg-message") {
		t.Fatalf("unexpected arg error: %v", err)
	}
}

func TestHandshakeEOF(t *testing.T) {
	if _, err := NewSession(strings.NewReader(""), &bytes.Buffer{}, testRecipientType); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestWriteFailuresPropagate(t *testing.T) {
	// A writer that fails during the handshake answer surfaces immediately.
	_, err := NewSession(strings.NewReader(greetStanza()), failingWriter{}, testRecipientType)
	if err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("handshake error = %v, want broken pipe", err)
	}

	// A failure while sending add-recipient/wrap-file-key is propagated too.
	session, _ := fakeHost(t, greetStanza()+mustWrite(&Stanza{Type: "done"}))
	session.writer = &stanzaWriter{w: failingWriter{}}
	session.rawWriter = failingWriter{}
	session.AddRecipient([]string{testRecipientType})
	if _, err := session.WrapFileKey(testFileKey); err == nil {
		t.Fatal("WrapFileKey with failing writer succeeded, want error")
	}
}
