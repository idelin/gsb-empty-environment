package mimeingest_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"example.org/offlinecli/mimeingest"
)

// fakeObject is a staged storage object that records its fate.
type fakeObject struct {
	name      string
	buf       bytes.Buffer
	published bool
	withdrawn bool
}

func (o *fakeObject) Write(p []byte) (int, error) { return o.buf.Write(p) }
func (o *fakeObject) Publish(context.Context) error {
	o.published = true
	return nil
}
func (o *fakeObject) Withdraw(context.Context) error {
	o.withdrawn = true
	return nil
}

type fakeStore struct {
	objects []*fakeObject
}

func (s *fakeStore) Stage(_ context.Context, meta mimeingest.Attachment) (mimeingest.Object, error) {
	o := &fakeObject{name: meta.Filename}
	s.objects = append(s.objects, o)
	return o, nil
}

// fakeScanner flags attachments as infected based on their filename.
type fakeScanner struct {
	infected map[string]bool
	scanned  []string
}

type fakeSession struct {
	bytes.Buffer
	infected bool
}

func (s *fakeSession) Verdict() error {
	if s.infected {
		return errors.New("virus found")
	}
	return nil
}

func (s *fakeScanner) Begin(meta mimeingest.Attachment) (mimeingest.Session, error) {
	s.scanned = append(s.scanned, meta.Filename)
	return &fakeSession{infected: s.infected[meta.Filename]}, nil
}

// chunkedReader feeds data in fixed-size chunks so that boundary markers
// and headers span multiple underlying reads.
type chunkedReader struct {
	data []byte
	n    int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := min(c.n, len(c.data), len(p))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func join(lines ...string) string { return strings.Join(lines, "\r\n") }

var pdfB64 = base64.StdEncoding.EncodeToString([]byte("fake-pdf-bytes"))

func happyMessage() string {
	return join(
		"From: sender@example.com",
		"To: rcpt@example.com",
		"Subject: report",
		`Content-Type: multipart/mixed; boundary="out"`,
		"",
		"preamble is ignored",
		"--out",
		"Content-Type: text/plain",
		"",
		"hello",
		"--out",
		`Content-Type: multipart/alternative; boundary="in"`,
		"",
		"--in",
		"Content-Type: text/plain",
		"",
		"plain",
		"--in",
		"Content-Type: text/html",
		"",
		"<b>html</b>",
		"--in--",
		"--out",
		`Content-Type: application/pdf; name="doc.pdf"`,
		`Content-Disposition: attachment; filename="doc.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		pdfB64,
		"--out",
		`Content-Type: text/plain; name="notes.txt"`,
		`Content-Disposition: attachment; filename="notes.txt"`,
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"hello=20world=21",
		"--out--",
		"",
	)
}

func ingest(t *testing.T, r io.Reader, cfg mimeingest.Config) (*mimeingest.Result, error) {
	t.Helper()
	return mimeingest.Ingest(context.Background(), r, cfg)
}

// requireAllWithdrawn fails the test unless every staged object was
// withdrawn and none was published.
func requireAllWithdrawn(t *testing.T, s *fakeStore) {
	t.Helper()
	if len(s.objects) == 0 {
		t.Fatal("expected at least one staged object")
	}
	for i, o := range s.objects {
		if o.published {
			t.Errorf("object %d (%q) was published", i, o.name)
		}
		if !o.withdrawn {
			t.Errorf("object %d (%q) was not withdrawn", i, o.name)
		}
	}
}

func TestIngestPublishesCleanAttachments(t *testing.T) {
	store := &fakeStore{}
	scanner := &fakeScanner{}
	res, err := ingest(t, strings.NewReader(happyMessage()), mimeingest.Config{
		Store:   store,
		Scanner: scanner,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Attachments) != 2 {
		t.Fatalf("got %d attachments, want 2", len(res.Attachments))
	}
	if res.Attachments[0].Filename != "doc.pdf" || res.Attachments[0].Size != 14 {
		t.Errorf("attachment 0: %+v", res.Attachments[0])
	}
	if res.Attachments[1].Filename != "notes.txt" || res.Attachments[1].Size != 12 {
		t.Errorf("attachment 1: %+v", res.Attachments[1])
	}
	if got := store.objects[0].buf.String(); got != "fake-pdf-bytes" {
		t.Errorf("doc.pdf payload = %q", got)
	}
	if got := store.objects[1].buf.String(); got != "hello world!" {
		t.Errorf("notes.txt payload = %q", got)
	}
	for i, o := range store.objects {
		if !o.published || o.withdrawn {
			t.Errorf("object %d: published=%v withdrawn=%v", i, o.published, o.withdrawn)
		}
	}
	if strings.Join(scanner.scanned, ",") != "doc.pdf,notes.txt" {
		t.Errorf("scanned = %v", scanner.scanned)
	}
	// 5 (hello) + 5 (plain) + 11 (<b>html</b>) + 14 (pdf) + 12 (qp)
	if res.DecodedBytes != 47 {
		t.Errorf("DecodedBytes = %d, want 47", res.DecodedBytes)
	}
}

func TestBoundaryMarkersSpanningBuffers(t *testing.T) {
	for _, chunk := range []int{1, 3, 7} {
		store := &fakeStore{}
		scanner := &fakeScanner{}
		r := &chunkedReader{data: []byte(happyMessage()), n: chunk}
		res, err := ingest(t, r, mimeingest.Config{Store: store, Scanner: scanner})
		if err != nil {
			t.Fatalf("chunk=%d: Ingest: %v", chunk, err)
		}
		if len(res.Attachments) != 2 {
			t.Fatalf("chunk=%d: got %d attachments, want 2", chunk, len(res.Attachments))
		}
		if got := store.objects[0].buf.String(); got != "fake-pdf-bytes" {
			t.Errorf("chunk=%d: doc.pdf payload = %q", chunk, got)
		}
		for i, o := range store.objects {
			if !o.published || o.withdrawn {
				t.Errorf("chunk=%d: object %d: published=%v withdrawn=%v", chunk, i, o.published, o.withdrawn)
			}
		}
	}
}

func TestForgedInnerTerminatorWithdrawsAll(t *testing.T) {
	msg := join(
		"From: attacker@example.com",
		`Content-Type: multipart/mixed; boundary="out"`,
		"",
		"--out",
		`Content-Type: application/octet-stream; name="first.bin"`,
		`Content-Disposition: attachment; filename="first.bin"`,
		"",
		"PAYLOAD-1",
		"--out",
		"Content-Type: text/plain",
		"",
		"some innocent text",
		"--out--",
		"--out",
		`Content-Type: application/octet-stream; name="evil.exe"`,
		`Content-Disposition: attachment; filename="evil.exe"`,
		"",
		"EVIL",
		"--out--",
		"",
	)
	store := &fakeStore{}
	scanner := &fakeScanner{}
	_, err := ingest(t, strings.NewReader(msg), mimeingest.Config{Store: store, Scanner: scanner})
	if !errors.Is(err, mimeingest.ErrTrailingData) {
		t.Fatalf("err = %v, want ErrTrailingData", err)
	}
	requireAllWithdrawn(t, store)
	if strings.Join(scanner.scanned, ",") != "first.bin" {
		t.Errorf("scanned = %v, want only first.bin", scanner.scanned)
	}
}

func TestTrailingCorruptionWithdrawsAll(t *testing.T) {
	msg := happyMessage() + "GARBAGE\r\n"
	store := &fakeStore{}
	scanner := &fakeScanner{}
	_, err := ingest(t, strings.NewReader(msg), mimeingest.Config{Store: store, Scanner: scanner})
	if !errors.Is(err, mimeingest.ErrTrailingData) {
		t.Fatalf("err = %v, want ErrTrailingData", err)
	}
	requireAllWithdrawn(t, store)
}

func TestTruncatedTailWithdrawsAll(t *testing.T) {
	msg := join(
		`Content-Type: multipart/mixed; boundary="out"`,
		"",
		"--out",
		`Content-Type: application/octet-stream; name="first.bin"`,
		`Content-Disposition: attachment; filename="first.bin"`,
		"",
		"partial-payload-without-terminator",
	)
	store := &fakeStore{}
	scanner := &fakeScanner{}
	_, err := ingest(t, strings.NewReader(msg), mimeingest.Config{Store: store, Scanner: scanner})
	if !errors.Is(err, mimeingest.ErrUnterminated) {
		t.Fatalf("err = %v, want ErrUnterminated", err)
	}
	requireAllWithdrawn(t, store)
}

func TestMissingInnerClosingBoundaryWithdrawsAll(t *testing.T) {
	msg := join(
		`Content-Type: multipart/mixed; boundary="out"`,
		"",
		"--out",
		`Content-Type: application/octet-stream; name="first.bin"`,
		`Content-Disposition: attachment; filename="first.bin"`,
		"",
		"PAYLOAD-1",
		"--out",
		`Content-Type: multipart/alternative; boundary="in"`,
		"",
		"--in",
		"Content-Type: text/plain",
		"",
		"plain",
		"--out--",
		"",
	)
	store := &fakeStore{}
	scanner := &fakeScanner{}
	_, err := ingest(t, strings.NewReader(msg), mimeingest.Config{Store: store, Scanner: scanner})
	if err == nil || !strings.Contains(err.Error(), "missing closing boundary") {
		t.Fatalf("err = %v, want missing closing boundary", err)
	}
	requireAllWithdrawn(t, store)
}

func TestBudgetExceededWithdrawsAll(t *testing.T) {
	store := &fakeStore{}
	scanner := &fakeScanner{}
	_, err := ingest(t, strings.NewReader(happyMessage()), mimeingest.Config{
		Store:   store,
		Scanner: scanner,
		Budget:  10,
	})
	if !errors.Is(err, mimeingest.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if len(store.objects) != 0 {
		// The budget is blown before any attachment starts, so nothing
		// may be staged at all.
		requireAllWithdrawn(t, store)
	}
}

func TestBudgetExceededInsideAttachmentWithdrawsAll(t *testing.T) {
	store := &fakeStore{}
	scanner := &fakeScanner{}
	// 5 (hello) + 5 (plain) + 11 (html) + 14 (pdf) = 35, so a budget of
	// 30 blows up while streaming doc.pdf.
	_, err := ingest(t, strings.NewReader(happyMessage()), mimeingest.Config{
		Store:   store,
		Scanner: scanner,
		Budget:  30,
	})
	if !errors.Is(err, mimeingest.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	requireAllWithdrawn(t, store)
}

func TestInfectedAttachmentWithdrawsAll(t *testing.T) {
	store := &fakeStore{}
	scanner := &fakeScanner{infected: map[string]bool{"notes.txt": true}}
	_, err := ingest(t, strings.NewReader(happyMessage()), mimeingest.Config{
		Store:   store,
		Scanner: scanner,
	})
	if !errors.Is(err, mimeingest.ErrInfected) {
		t.Fatalf("err = %v, want ErrInfected", err)
	}
	requireAllWithdrawn(t, store)
}
