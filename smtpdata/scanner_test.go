package smtpdata

import (
	"bytes"
	"errors"
	"strconv"
	"testing"
)

// feedFixed feeds data in blocks of exactly chunkSize bytes (the last block
// may be shorter). It stops at the first terminal error.
func feedFixed(t *testing.T, limit int64, data []byte, chunkSize int) *Scanner {
	t.Helper()
	s := NewScanner(limit)
	for off := 0; off < len(data); {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		n, err := s.Write(data[off:end])
		if n != end-off {
			t.Fatalf("Write consumed %d bytes, want %d", n, end-off)
		}
		if err != nil {
			return s
		}
		off = end
	}
	return s
}

// feedCut feeds data as two blocks split at cut.
func feedCut(t *testing.T, limit int64, data []byte, cut int) *Scanner {
	t.Helper()
	s := NewScanner(limit)
	n, err := s.Write(data[:cut])
	if n != cut {
		t.Fatalf("first Write consumed %d bytes, want %d", n, cut)
	}
	if err != nil {
		return s
	}
	n, err = s.Write(data[cut:])
	if n != len(data)-cut {
		t.Fatalf("second Write consumed %d bytes, want %d", n, len(data)-cut)
	}
	return s
}

func errKey(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrBareLF):
		return "bare-lf"
	case errors.Is(err, ErrBareCR):
		return "bare-cr"
	case errors.Is(err, ErrTooLarge):
		return "too-large"
	case errors.Is(err, ErrTrailingData):
		return "trailing"
	default:
		return "other"
	}
}

func assertOutcome(t *testing.T, label string, ref, got *Scanner) {
	t.Helper()
	if got.Done() != ref.Done() {
		t.Errorf("%s: Done = %v, want %v", label, got.Done(), ref.Done())
	}
	if errKey(got.Err()) != errKey(ref.Err()) {
		t.Errorf("%s: Err = %v, want %v", label, got.Err(), ref.Err())
	}
	if !bytes.Equal(got.Body(), ref.Body()) {
		t.Errorf("%s: Body = %q, want %q", label, got.Body(), ref.Body())
	}
	if got.Size() != ref.Size() {
		t.Errorf("%s: Size = %d, want %d", label, got.Size(), ref.Size())
	}
}

// The two smuggling primitives from the brief, embedded in different
// positions. Each must be rejected as a whole, without normalization.
var smugglingPayloads = map[string][]byte{
	"lf-dot-at-start":    []byte("\n.\r\n"),
	"cr-dot-at-start":    []byte("\r.\r\n"),
	"lf-dot-after-line":  []byte("Subject: test\r\n\n.\r\n"),
	"cr-dot-after-line":  []byte("Subject: test\r\n\r.\r\n"),
	"lf-dot-mid-message": []byte("From: a@x\r\nTo: b@y\r\n\n.\r\nRCPT TO:<victim>\r\n"),
	"cr-dot-mid-message": []byte("From: a@x\r\nTo: b@y\r\n\r.\r\nRCPT TO:<victim>\r\n"),
}

func TestRejectSmugglingSequences(t *testing.T) {
	for name, data := range smugglingPayloads {
		t.Run(name, func(t *testing.T) {
			want := ErrBareLF
			if data[0] == '\r' || bytes.Contains(data, []byte("\r.")) {
				want = ErrBareCR
			}

			ref := feedFixed(t, -1, data, len(data))
			if ref.Done() || !errors.Is(ref.Err(), want) {
				t.Fatalf("one-shot: Done=%v Err=%v, want error %v", ref.Done(), ref.Err(), want)
			}

			// Every possible block boundary must yield the same rejection.
			for cut := 0; cut <= len(data); cut++ {
				got := feedCut(t, -1, data, cut)
				assertOutcome(t, "cut@"+strconv.Itoa(cut), ref, got)
			}
			for chunk := 1; chunk <= len(data); chunk++ {
				got := feedFixed(t, -1, data, chunk)
				assertOutcome(t, "chunk="+strconv.Itoa(chunk), ref, got)
			}
		})
	}
}

// A valid terminator split at every possible position, including cuts in the
// middle of CRLF and between the dot and the following CR.
func TestTerminatorSplitAcrossBlocks(t *testing.T) {
	data := []byte("line-one\r\nline-two\r\n.\r\n")
	wantBody := []byte("line-one\r\nline-two\r\n")

	ref := NewScanner(-1)
	if n, err := ref.Write(data); err != nil || n != len(data) {
		t.Fatalf("one-shot Write: n=%d err=%v", n, err)
	}
	if !ref.Done() || ref.Err() != nil {
		t.Fatalf("one-shot: Done=%v Err=%v", ref.Done(), ref.Err())
	}
	if !bytes.Equal(ref.Body(), wantBody) {
		t.Fatalf("one-shot Body = %q, want %q", ref.Body(), wantBody)
	}

	for cut := 0; cut <= len(data); cut++ {
		got := feedCut(t, -1, data, cut)
		assertOutcome(t, "cut@"+strconv.Itoa(cut), ref, got)
	}
	for chunk := 1; chunk <= len(data); chunk++ {
		got := feedFixed(t, -1, data, chunk)
		assertOutcome(t, "chunk="+strconv.Itoa(chunk), ref, got)
	}
}

func TestDotStuffing(t *testing.T) {
	cases := []struct {
		name string
		wire string
		want string
	}{
		{"stuffed-first-line", "..hello\r\n.\r\n", ".hello\r\n"},
		{"stuffed-later-line", "a\r\n..\r\n.\r\n", "a\r\n.\r\n"},
		{"stuffed-multiple", "..\r\n...x\r\n..\r\n.\r\n", ".\r\n..x\r\n.\r\n"},
		{"interior-dots-untouched", "a..b\r\n.\r\n", "a..b\r\n"},
		// A lone line-start dot not forming the terminator is content, not
		// a terminator: only exact CRLF.CRLF ends DATA.
		{"lone-dot-line-content", ".x\r\n.\r\n", ".x\r\n"},
		{"empty-message", ".\r\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.wire)
			ref := feedFixed(t, -1, data, len(data))
			if !ref.Done() || ref.Err() != nil {
				t.Fatalf("one-shot: Done=%v Err=%v", ref.Done(), ref.Err())
			}
			if got := string(ref.Body()); got != tc.want {
				t.Fatalf("Body = %q, want %q", got, tc.want)
			}

			for cut := 0; cut <= len(data); cut++ {
				got := feedCut(t, -1, data, cut)
				assertOutcome(t, "cut@"+strconv.Itoa(cut), ref, got)
			}
			for chunk := 1; chunk <= len(data); chunk++ {
				got := feedFixed(t, -1, data, chunk)
				assertOutcome(t, "chunk="+strconv.Itoa(chunk), ref, got)
			}
		})
	}
}

// Exhaustive chunking invariance over a mixed corpus: no matter where block
// boundaries fall, the result must equal feeding the whole input at once.
func TestAllChunkingsMatchReference(t *testing.T) {
	valid := [][]byte{
		[]byte(".\r\n"),
		[]byte("hello\r\n.\r\n"),
		[]byte("a\r\nbc\r\n\r\n.\r\n"),
		[]byte("..x\r\na\r\n..\r\n.\r\n"),
		[]byte("CRLF only at end\r\n"), // incomplete: no terminator yet
		[]byte(".\r"),                  // terminator cut before final LF: not done, no error
		[]byte("x\r\n."),               // line-start dot held across the block boundary
	}
	invalid := [][]byte{
		[]byte("\n.\r\n"),
		[]byte("\r.\r\n"),
		[]byte("x\r\n\n.\r\ny"),
		[]byte("x\r\n\r.\r\ny"),
		[]byte("bare \r inside\r\n.\r\n"),
		[]byte("bare \n inside\r\n.\r\n"),
		[]byte(".\r\ntrailing"),
	}

	check := func(t *testing.T, data []byte) {
		ref := feedFixed(t, -1, data, len(data)+1) // always a single Write
		for cut := 0; cut <= len(data); cut++ {
			assertOutcome(t, "cut@"+strconv.Itoa(cut), ref, feedCut(t, -1, data, cut))
		}
		for chunk := 1; chunk <= len(data); chunk++ {
			assertOutcome(t, "chunk="+strconv.Itoa(chunk), ref, feedFixed(t, -1, data, chunk))
		}
	}

	for i, data := range valid {
		t.Run("valid/"+strconv.Itoa(i), func(t *testing.T) { check(t, data) })
	}
	for i, data := range invalid {
		t.Run("invalid/"+strconv.Itoa(i), func(t *testing.T) {
			ref := feedFixed(t, -1, data, len(data)+1)
			if ref.Err() == nil && ref.Done() {
				t.Fatalf("input %q unexpectedly accepted", data)
			}
			check(t, data)
		})
	}
}

func TestSizeLimit(t *testing.T) {
	data := []byte("abc\r\n.\r\n") // decoded body is "abc\r\n" = 5 bytes

	if s := feedFixed(t, 5, data, len(data)); s.Err() != nil || !s.Done() || s.Size() != 5 {
		t.Fatalf("limit=5: Done=%v Err=%v Size=%d", s.Done(), s.Err(), s.Size())
	}
	if s := feedFixed(t, 4, data, len(data)); !errors.Is(s.Err(), ErrTooLarge) || s.Done() {
		t.Fatalf("limit=4: Done=%v Err=%v", s.Done(), s.Err())
	}
	// The terminator itself never counts: an empty message fits limit 0.
	if s := feedFixed(t, 0, []byte(".\r\n"), 2); s.Err() != nil || !s.Done() || s.Size() != 0 {
		t.Fatalf("limit=0 empty: Done=%v Err=%v Size=%d", s.Done(), s.Err(), s.Size())
	}
	// A stuffed dot decodes to one byte, so the limit applies to output size.
	stuffed := []byte("..a\r\n.\r\n") // decoded ".a\r\n" = 4 bytes
	if s := feedFixed(t, 4, stuffed, 1); s.Err() != nil || !s.Done() {
		t.Fatalf("stuffed limit=4 byte-at-a-time: Done=%v Err=%v", s.Done(), s.Err())
	}
	if s := feedFixed(t, 3, stuffed, 1); !errors.Is(s.Err(), ErrTooLarge) {
		t.Fatalf("stuffed limit=3: Err=%v, want ErrTooLarge", s.Err())
	}
	// Unlimited cap.
	if s := feedFixed(t, -1, data, 2); s.Err() != nil || !s.Done() {
		t.Fatalf("unlimited: Done=%v Err=%v", s.Done(), s.Err())
	}
}

func TestTrailingBytes(t *testing.T) {
	s := NewScanner(-1)
	if _, err := s.Write([]byte(".\r\n")); err != nil {
		t.Fatalf("terminator: %v", err)
	}
	if _, err := s.Write([]byte("MAIL FROM:<attacker>")); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("second Write err = %v, want ErrTrailingData", err)
	}
	// Same block as the terminator, and across every cut position.
	data := []byte(".\r\nX")
	ref := NewScanner(-1)
	if _, err := ref.Write(data); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("one-shot err = %v, want ErrTrailingData", err)
	}
	for cut := 0; cut <= len(data); cut++ {
		assertOutcome(t, "cut@"+strconv.Itoa(cut), ref, feedCut(t, -1, data, cut))
	}
}

func TestResetReuse(t *testing.T) {
	s := NewScanner(8)
	if _, err := s.Write([]byte("0123456789\r\n.\r\n")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Err = %v, want ErrTooLarge", err)
	}
	s.Reset()
	if s.Done() || s.Err() != nil || s.Size() != 0 || len(s.Body()) != 0 {
		t.Fatalf("Reset did not clear state: Done=%v Err=%v Size=%d Body=%q",
			s.Done(), s.Err(), s.Size(), s.Body())
	}
	if _, err := s.Write([]byte("ok\r\n.\r\n")); err != nil || !s.Done() {
		t.Fatalf("reuse: Done=%v Err=%v", s.Done(), err)
	}
	if got := string(s.Body()); got != "ok\r\n" {
		t.Fatalf("Body after reuse = %q", got)
	}
}

func TestErrorBranchesAndLimitInEveryState(t *testing.T) {
	t.Run("write-after-terminal-error", func(t *testing.T) {
		s := NewScanner(-1)
		if _, err := s.Write([]byte("\n")); !errors.Is(err, ErrBareLF) {
			t.Fatalf("first Err = %v, want ErrBareLF", err)
		}
		// Subsequent blocks keep returning the same error and never panic.
		n, err := s.Write([]byte("ignored"))
		if !errors.Is(err, ErrBareLF) || n != len("ignored") {
			t.Fatalf("post-error Write: n=%d err=%v", n, err)
		}
	})

	t.Run("empty-write-is-noop", func(t *testing.T) {
		s := NewScanner(-1)
		if n, err := s.Write(nil); n != 0 || err != nil {
			t.Fatalf("empty Write: n=%d err=%v", n, err)
		}
	})

	// Limit enforcement must fire whichever state emits the byte.
	limitCases := []struct {
		name string
		lim  int64
		wire string
	}{
		{"data-byte", 0, "a"},          // limit 0 rejects the first content byte
		{"crlf", 1, "a\r\n"},           // 'a' fits; the CRLF '\r' then exceeds it
		{"stuffed-dot", 1, "..a"},      // first stuffed dot fits; next byte rejected
		{"held-dot-other", 1, ".x"},    // held dot fits; following byte rejected
		{"stuffed-dot-zero", 0, "..x"}, // limit 0 rejects inside dot stuffing
		{"held-dot-zero", 0, ".x"},     // limit 0 rejects the committed held dot
	}
	for _, tc := range limitCases {
		t.Run("limit/"+tc.name, func(t *testing.T) {
			s := NewScanner(tc.lim)
			_, err := s.Write([]byte(tc.wire))
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("Err = %v, want ErrTooLarge", err)
			}
		})
	}

	// Held dot followed by a bare LF and held ".\r" followed by a non-LF.
	t.Run("dot-then-bare-lf", func(t *testing.T) {
		s := NewScanner(-1)
		if _, err := s.Write([]byte(".\n")); !errors.Is(err, ErrBareLF) {
			t.Fatalf("Err = %v, want ErrBareLF", err)
		}
	})
	t.Run("dotcr-then-non-lf", func(t *testing.T) {
		s := NewScanner(-1)
		if _, err := s.Write([]byte(".\rX")); !errors.Is(err, ErrBareCR) {
			t.Fatalf("Err = %v, want ErrBareCR", err)
		}
	})
}
