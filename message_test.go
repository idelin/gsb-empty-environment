package netsec

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewlineAndControlInjectionRejected(t *testing.T) {
	names := []string{
		"Alice\r\nBcc: attacker@example.com",
		"Alice\nContent-Type: text/html",
		"Alice\rBcc: attacker@example.com",
		"Alice\x00Bcc: attacker@example.com",
		"Alice\tBcc: attacker@example.com",
		"Alice\x1fBcc: attacker@example.com",
		"Alice\x7fBcc: attacker@example.com",
	}
	for _, name := range names {
		if _, err := NewAddress(name, "alice@example.com"); !errors.Is(err, ErrHeaderInjection) {
			t.Fatalf("NewAddress(%q) error = %v, want ErrHeaderInjection", name, err)
		}
		if _, err := NewAddress("Alice", "alice@example.com"+name[len("Alice"):]); err == nil {
			t.Fatalf("mailbox with injected text was accepted")
		}
	}

	msg := NewMessage()
	if err := msg.SetFromNameAddress("Alice\r\nBcc: attacker@example.com", "alice@example.com"); err == nil {
		t.Fatal("SetFromNameAddress accepted a line break")
	}
	if err := msg.SetSubject("hello\r\nBcc: attacker@example.com"); !errors.Is(err, ErrHeaderInjection) {
		t.Fatalf("SetSubject error = %v, want ErrHeaderInjection", err)
	}
}

func TestDisplayNameEncodingDoesNotDecodeAgain(t *testing.T) {
	encodedPayload := "=?UTF-8?Q?=0D=0A?="
	if _, err := NewAddress(encodedPayload, "alice@example.com"); !errors.Is(err, ErrEncodedInput) {
		t.Fatalf("pre-encoded display name error = %v, want ErrEncodedInput", err)
	}

	msg := NewMessage()
	if err := msg.SetFromNameAddress("Alice%0D%0ABcc: attacker@example.com", "alice@example.com"); err != nil {
		t.Fatalf("literal percent text should be accepted and quoted: %v", err)
	}
	if err := msg.SetTo(Address{Name: "Alice=0D=0ABcc: attacker@example.com", Address: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	parsed := readMessage(t, data)
	if _, ok := parsed.Header["Bcc"]; ok {
		t.Fatal("percent/equal text became a Bcc header")
	}
	headerBlock, _ := splitHeaderBody(t, data)
	if bytes.Contains(headerBlock, []byte("\r\nBcc:")) || bytes.Contains(headerBlock, []byte("\nBcc:")) {
		t.Fatal("header block contains an injected Bcc line")
	}
	if !bytes.Contains(headerBlock, []byte("%0D%0A")) {
		t.Fatal("percent escapes were unexpectedly decoded")
	}
}

func TestLongDisplayNameIsEncodedAndFolded(t *testing.T) {
	name := strings.Repeat("A", 2000) + strings.Repeat("长", 600)
	address, err := NewAddress(name, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	msg := NewMessage()
	if err := msg.SetFrom(address); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	headerBlock, _ := splitHeaderBody(t, data)
	if !utf8.Valid(headerBlock) {
		t.Fatal("header block is not UTF-8")
	}
	for _, b := range headerBlock {
		if b >= utf8.RuneSelf {
			t.Fatal("header block contains raw non-ASCII bytes")
		}
	}
	assertHeaderLines(t, headerBlock)
	if bytes.Count(headerBlock, []byte("\r\nFrom:")) != 0 {
		t.Fatal("From header was split into another field")
	}
	if !bytes.HasPrefix(headerBlock, []byte("From: ")) {
		t.Fatal("From header is missing")
	}

	parsed := readMessage(t, data)
	from, err := mail.ParseAddress(parsed.Header.Get("From"))
	if err != nil {
		t.Fatalf("parse generated From: %v", err)
	}
	if from.Name != name {
		t.Fatalf("decoded display name length = %d, want %d", len(from.Name), len(name))
	}
}

func TestForgedBoundaryAndMIMEParametersRejected(t *testing.T) {
	msg := NewMessage()
	if err := msg.SetFromNameAddress("Sender", "sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.AddAttachment("report.txt", "text/plain; boundary=forged", []byte("x")); !errors.Is(err, ErrInvalidMIME) {
		t.Fatalf("content type with parameters error = %v, want ErrInvalidMIME", err)
	}
	if err := msg.AddAttachment("report\r\nX-Evil: 1.txt", "text/plain", []byte("x")); !errors.Is(err, ErrHeaderInjection) {
		t.Fatalf("filename line break error = %v, want ErrHeaderInjection", err)
	}
	if err := msg.Attach("report.txt", []byte("attachment")); err != nil {
		t.Fatal(err)
	}

	firstBoundary, err := msg.Boundary()
	if err != nil {
		t.Fatal(err)
	}
	if err := msg.SetTextBody("--" + firstBoundary + "\r\nContent-Type: text/html\r\n\r\nforged\r\n"); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	parsed := readMessage(t, data)
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("top-level content type = %q", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("server did not generate a boundary")
	}
	if boundary == firstBoundary {
		t.Fatal("boundary was not regenerated after a body collision")
	}
	headerBlock, _ := splitHeaderBody(t, data)
	if bytes.Count(headerBlock, []byte("boundary=")) != 1 {
		t.Fatal("expected exactly one authoritative boundary parameter")
	}

	reader := multipart.NewReader(parsed.Body, boundary)
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "--"+firstBoundary) {
		t.Fatal("forged body boundary was not preserved as body text")
	}
	part, err = reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	attached, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(attached), "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "attachment" {
		t.Fatalf("attachment = %q", decoded)
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatalf("unexpected extra MIME part: %v", err)
	}
}

func TestValidMultipleRecipientsAndEnvelopeSeparation(t *testing.T) {
	alice, err := NewAddress("Alice", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewAddress("鲍勃", "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	carol, err := NewAddress("", "carol@example.com")
	if err != nil {
		t.Fatal(err)
	}

	msg := NewMessage()
	if err := msg.SetFromNameAddress("Sender", "sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.SetTo(alice, bob, carol); err != nil {
		t.Fatal(err)
	}
	if err := msg.SetCc("Carol <carol@example.com>"); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	headerBlock, _ := splitHeaderBody(t, data)
	assertHeaderLines(t, headerBlock)
	if bytes.Count(headerBlock, []byte("\r\nTo:")) != 1 {
		t.Fatal("expected exactly one To header")
	}
	parsed := readMessage(t, data)
	to, err := mail.ParseAddressList(parsed.Header.Get("To"))
	if err != nil {
		t.Fatal(err)
	}
	if len(to) != 3 {
		t.Fatalf("To recipient count = %d, want 3", len(to))
	}
	if to[0].Name != "Alice" || to[1].Name != "鲍勃" || to[2].Address != "carol@example.com" {
		t.Fatalf("unexpected To list: %#v", to)
	}

	envelope, err := NewEnvelope("sender@example.com", []string{
		"alice@example.com",
		"bob@example.com",
		"carol@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if recipients, err := envelope.RcptTo(); err != nil || len(recipients) != 3 {
		t.Fatalf("RcptTo = %#v, %v", recipients, err)
	}
	if _, err := NewEnvelope("sender@example.com", []string{"Bob <bob@example.com>"}); err == nil {
		t.Fatal("envelope accepted a header-style display name")
	}
	if _, err := NewEnvelope("sender@example.com", []string{"bob@example.com\r\nRCPT TO:<eve@example.com>"}); err == nil {
		t.Fatal("envelope accepted a line break")
	}
	if err := ValidateEnvelopeRecipients([]string{"用户@example.com"}); err == nil {
		t.Fatal("envelope accepted a non-ASCII mailbox")
	}
}

func TestAttachmentFilenameUsesRFC2231(t *testing.T) {
	filename := "报告 2026-最终版本-" + strings.Repeat("长", 80) + ".pdf"
	msg := NewMessage()
	if err := msg.SetFromNameAddress("Sender", "sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.SetTextBody("see attachment\n"); err != nil {
		t.Fatal(err)
	}
	if err := msg.AddAttachment(filename, "application/pdf", []byte("%PDF-1.7\n")); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	headerBlock, _ := splitHeaderBody(t, data)
	for _, b := range headerBlock {
		if b >= utf8.RuneSelf {
			t.Fatal("attachment header contains raw non-ASCII bytes")
		}
	}
	parsed := readMessage(t, data)
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("content type = %q", mediaType)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	if _, err := reader.NextPart(); err != nil {
		t.Fatal(err)
	}
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	disposition := part.Header.Get("Content-Disposition")
	if !strings.Contains(disposition, "filename*0*=UTF-8''") {
		t.Fatalf("long RFC 2231 filename is missing a continuation:\n%s", disposition)
	}
	for _, b := range []byte(disposition) {
		if b >= utf8.RuneSelf {
			t.Fatal("attachment disposition contains raw non-ASCII bytes")
		}
	}
	got := part.FileName()
	if got != filename {
		t.Fatalf("decoded filename = %q, want %q", got, filename)
	}
}

func TestSubjectAndRecipientListAreSingleAuthoritativeFields(t *testing.T) {
	msg := NewMessage()
	if err := msg.SetFromNameAddress("Sender", "sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.SetTo("one@example.com", "two@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.SetSubject(strings.Repeat("主题", 300)); err != nil {
		t.Fatal(err)
	}
	data, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	headerBlock, _ := splitHeaderBody(t, data)
	assertHeaderLines(t, headerBlock)
	counts := countHeaderFields(t, headerBlock)
	for _, field := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"} {
		if counts[field] != 1 {
			t.Fatalf("%s count = %d, want 1", field, counts[field])
		}
	}
	if counts["Bcc"] != 0 {
		t.Fatal("unexpected Bcc field")
	}
}

func readMessage(t *testing.T, data []byte) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v\n%s", err, data)
	}
	return msg
}

func splitHeaderBody(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	index := bytes.Index(data, []byte("\r\n\r\n"))
	if index < 0 {
		t.Fatalf("message has no header/body separator:\n%s", data)
	}
	return data[:index+2], data[index+4:]
}

func assertHeaderLines(t *testing.T, headerBlock []byte) {
	t.Helper()
	lines := bytes.Split(headerBlock, []byte("\n"))
	for i, raw := range lines {
		if len(raw) == 0 {
			continue
		}
		if raw[len(raw)-1] != '\r' {
			t.Fatalf("header line %d does not end in CRLF: %q", i+1, raw)
		}
		line := raw[:len(raw)-1]
		if len(line) > 998 {
			t.Fatalf("header line %d is %d bytes", i+1, len(line))
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			name := line
			if colon := bytes.IndexByte(line, ':'); colon >= 0 {
				name = line[:colon]
			}
			if !isHeaderFieldName(string(name)) {
				t.Fatalf("header line %d has invalid field name %q", i+1, name)
			}
		}
	}
}

func countHeaderFields(t *testing.T, headerBlock []byte) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, raw := range bytes.Split(headerBlock, []byte("\n")) {
		line := bytes.TrimSuffix(raw, []byte("\r"))
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			t.Fatalf("header line has no colon: %q", line)
		}
		counts[string(line[:colon])]++
	}
	return counts
}
