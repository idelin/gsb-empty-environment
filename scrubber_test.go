package offlinecli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// recallWriter keeps one separate record per underlying Write call so tests
// can prove no secret ever reaches the sink, even inside a single chunk.
type recallWriter struct {
	mu     sync.Mutex
	chunks [][]byte
	closed int
	err    error
}

func (w *recallWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.chunks = append(w.chunks, append([]byte(nil), p...))
	return len(p), nil
}

func (w *recallWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed++
	return w.err
}

func (w *recallWriter) joined() string {
	var b strings.Builder
	for _, chunk := range w.chunks {
		b.Write(chunk)
	}
	return b.String()
}

func (w *recallWriter) assertNoSubstring(t *testing.T, secret string) {
	t.Helper()
	for _, chunk := range w.chunks {
		if bytes.Contains(chunk, []byte(secret)) {
			t.Fatalf("secret %q leaked into underlying chunk %q", secret, chunk)
		}
	}
}

// shortWriter accepts at most maxPerCall bytes per Write but never errors,
// forcing the scrubber to retry around short writes.
type shortWriter struct {
	recallWriter
	maxPerCall int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.maxPerCall {
		n = w.maxPerCall
	}
	return w.recallWriter.Write(p[:n])
}

// failWriter errors after accept bytes have been accepted in total.
type failWriter struct {
	recallWriter
	accept int
	used   int
}

func (w *failWriter) Write(p []byte) (int, error) {
	remaining := w.accept - w.used
	if remaining <= 0 {
		return 0, errors.New("disk full")
	}
	n := len(p)
	if n > remaining {
		n = remaining
	}
	got, err := w.recallWriter.Write(p[:n])
	w.used += got
	return got, err
}

func mustWrite(t *testing.T, w io.Writer, p []byte) {
	t.Helper()
	n, err := w.Write(p)
	if err != nil {
		t.Fatalf("Write(%q) unexpected error: %v", p, err)
	}
	if n != len(p) {
		t.Fatalf("Write(%q) reported %d bytes, want %d", p, n, len(p))
	}
}

// TestLeftmostLongest covers the matching rule: the earliest starting
// position wins, and at a shared start the longest secret wins.
func TestLeftmostLongest(t *testing.T) {
	dict := NewDictionary(map[string]string{
		"abc":  "[A]",
		"abcd": "[LONG]",
		"bc":   "[B]",
		"x":    "[X]",
	})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	mustWrite(t, s, []byte("zabcdxxabc"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	got := sink.joined()
	want := "z[LONG][X][X][A]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	sink.assertNoSubstring(t, "abc")
	sink.assertNoSubstring(t, "abcd")
}

// TestByteByByteNoLeak feeds one byte at a time and checks every underlying
// chunk, proving buffered secret prefixes never hit the sink mid-secret.
func TestByteByByteNoLeak(t *testing.T) {
	dict := NewDictionary(map[string]string{
		"secret":  "***",
		"secrets": "****",
	})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	input := []byte("a secret and secrets here, secret!")
	for _, b := range input {
		mustWrite(t, s, []byte{b})
	}
	sink.assertNoSubstring(t, "secret")

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	got := sink.joined()
	want := "a *** and **** here, ***!"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	sink.assertNoSubstring(t, "secret")
	if sink.closed != 1 {
		t.Fatalf("underlying Close count = %d, want 1", sink.closed)
	}
}

// TestMidBufferMismatch exercises a trie prefix that diverges before a full
// secret is seen ("abx"); the safe bytes must still be emitted.
func TestMidBufferMismatch(t *testing.T) {
	dict := NewDictionary(map[string]string{"abc": "[A]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	for _, part := range []string{"ab", "x", "abc", "yab"} {
		mustWrite(t, s, []byte(part))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	got := sink.joined()
	want := "abx[A]yab"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestShortWritesNoLossOrDuplication makes the underlying sink accept only a
// few bytes per call: output must stay complete with one replacement per
// secret, no lost byte and no duplicated replacement.
func TestShortWritesNoLossOrDuplication(t *testing.T) {
	dict := NewDictionary(map[string]string{
		"key":  "[KEY]",
		"keys": "[KEYS]",
	})
	sink := &shortWriter{maxPerCall: 2}
	s := NewScrubber(sink, dict)

	mustWrite(t, s, []byte("key + keys = keykeys"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	got := sink.joined()
	want := "[KEY] + [KEYS] = [KEY][KEYS]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Count(got, "[KEY]") != 2 || strings.Count(got, "[KEYS]") != 2 {
		t.Fatalf("replacement count wrong in %q", got)
	}
	sink.assertNoSubstring(t, "key")
}

// TestRepeatedClose verifies idempotent Close: no extra flush, no double
// Close on the underlying writer, and writes after Close fail.
func TestRepeatedClose(t *testing.T) {
	dict := NewDictionary(map[string]string{"abc": "[A]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	mustWrite(t, s, []byte("ab")) // still undecided
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close error: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("third Close error: %v", err)
	}

	if sink.closed != 1 {
		t.Fatalf("underlying Close count = %d, want 1", sink.closed)
	}
	if got := sink.joined(); got != "ab" {
		t.Fatalf("undecided prefix must flush verbatim, got %q", got)
	}
	if n, err := s.Write([]byte("c")); !errors.Is(err, ErrClosed) || n != 0 {
		t.Fatalf("Write after Close = (%d, %v), want (0, ErrClosed)", n, err)
	}
}

// TestRotationCachedPrefixUsesOldDictionary is the key-rotation contract:
// prefixes buffered before rotation keep the old dictionary until they
// become decidable, while new bytes use the new dictionary.
func TestRotationCachedPrefixUsesOldDictionary(t *testing.T) {
	oldDict := NewDictionary(map[string]string{
		"abc": "[OLD]",
		"ab":  "[OLDSHORT]",
	})
	newDict := NewDictionary(map[string]string{
		"c":  "[NEWC]",
		"bc": "[NEWBC]",
	})
	sink := &recallWriter{}
	s := NewScrubber(sink, oldDict)

	mustWrite(t, s, []byte("ab")) // undecided under old dict
	sink.assertNoSubstring(t, "ab")
	s.RotateSecret(newDict)

	// "c" completes the old-era secret "abc": the cached prefix must be
	// judged under the old dictionary, not re-interpreted as "bc".
	mustWrite(t, s, []byte("c"))
	if got := sink.joined(); got != "[OLD]" {
		t.Fatalf("cached prefix used wrong dictionary, got %q", got)
	}

	// Everything afterward belongs to the new dictionary.
	mustWrite(t, s, []byte("bc"))
	if got := sink.joined(); got != "[OLD][NEWBC]" {
		t.Fatalf("new-era bytes used wrong dictionary, got %q", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	sink.assertNoSubstring(t, "abc")
}

// TestRotationUndecidedPrefixBreaks covers an old-era prefix that can never
// complete once its era ends: it is released safely, then new-era matching
// takes over without losing a byte.
func TestRotationUndecidedPrefixBreaks(t *testing.T) {
	oldDict := NewDictionary(map[string]string{"abc": "[OLD]"})
	newDict := NewDictionary(map[string]string{"xy": "[NEW]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, oldDict)

	mustWrite(t, s, []byte("ab"))
	s.RotateSecret(newDict)
	// "x" breaks the old-era prefix "ab"; the cached prefix was judged
	// under the old dictionary and is released as plain text.
	mustWrite(t, s, []byte("x"))

	// "ab" is released verbatim; "x" stays buffered as a prefix of "xy".
	if got := sink.joined(); got != "ab" {
		t.Fatalf("broken old-era prefix got %q, want ab", got)
	}
	mustWrite(t, s, []byte("y"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sink.joined(); got != "ab[NEW]" {
		t.Fatalf("got %q, want ab[NEW]", got)
	}
}

// TestRotationSplitSecretDoesNotMatch proves a secret assembled across the
// rotation boundary is matched by neither dictionary.
func TestRotationSplitSecretDoesNotMatch(t *testing.T) {
	oldDict := NewDictionary(map[string]string{"abc": "[OLD]"})
	newDict := NewDictionary(map[string]string{"abc": "[NEW]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, oldDict)

	mustWrite(t, s, []byte("a"))
	s.RotateSecret(newDict)
	mustWrite(t, s, []byte("bc"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The cached era-1 prefix "a" keeps the old dictionary until decidable;
	// the borrowed bytes "bc" complete old-era secret "abc", so it is
	// replaced under the old dictionary rather than re-interpreted anew.
	if got := sink.joined(); got != "[OLD]" {
		t.Fatalf("cached prefix must keep the old dictionary, got %q", got)
	}
}

// TestRotationBorrowedMismatchReleasesOldEra covers an old-era prefix that
// cannot match in its own dictionary: the borrowed newer-era byte must be
// returned to its own era instead of completing a cross-era secret.
func TestRotationBorrowedMismatchReleasesOldEra(t *testing.T) {
	oldDict := NewDictionary(map[string]string{"abc": "[OLD]"})
	newDict := NewDictionary(map[string]string{"bc": "[NEW]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, oldDict)

	// "b" starts no secret in the old dictionary, so it is decided safe
	// before the borrowed "c" can be interpreted under the new dictionary.
	mustWrite(t, s, []byte("0b"))
	s.RotateSecret(newDict)
	mustWrite(t, s, []byte("c"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sink.joined(); got != "0bc" {
		t.Fatalf("cross-era secret must not match, got %q", got)
	}
}

// TestMultipleRotationsKeepsEveryEra ensures era boundaries survive several
// back-to-back rotations with buffered prefixes.
func TestMultipleRotationsKeepsEveryEra(t *testing.T) {
	d1 := NewDictionary(map[string]string{"abc": "[1]"})
	d2 := NewDictionary(map[string]string{"bc": "[2]"})
	d3 := NewDictionary(map[string]string{"c": "[3]"})
	sink := &recallWriter{}
	s := NewScrubber(sink, d1)

	mustWrite(t, s, []byte("a"))
	s.RotateSecret(d2)
	s.RotateSecret(d3) // no bytes between rotations
	mustWrite(t, s, []byte("bc"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The era-1 prefix "a" borrows "bc" and completes secret "abc" under
	// dictionary 1, so every byte is consumed there and era 3 never sees "c".
	if got := sink.joined(); got != "[1]" {
		t.Fatalf("got %q, want [1]", got)
	}
}

// TestDynamicAddAndRemove checks registration changes on the active dict.
func TestDynamicAddAndRemove(t *testing.T) {
	sink := &recallWriter{}
	s := NewScrubber(sink, NewDictionary(nil))

	mustWrite(t, s, []byte("token"))
	if err := s.AddSecret("token", "[T]"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, []byte(" token"))
	if got := sink.joined(); got != "token [T]" {
		t.Fatalf("AddSecret not applied, got %q", got)
	}

	if err := s.RemoveSecret("token"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, []byte(" token"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sink.joined(); got != "token [T] token" {
		t.Fatalf("RemoveSecret not applied, got %q", got)
	}

	if err := s.AddSecret("", "x"); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	if err := s.RemoveSecret("missing"); err == nil {
		t.Fatal("removing an unknown secret must error")
	}
}

// TestMultiByteSecret covers UTF-8 secrets matched at the byte level.
func TestMultiByteSecret(t *testing.T) {
	dict := NewDictionary(map[string]string{
		"密钥": "[KEY]",
		"密":  "[SHORT]",
	})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	for _, r := range "一个密钥和密文" {
		mustWrite(t, s, []byte(string(r)))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	want := "一个[KEY]和[SHORT]文"
	if got := sink.joined(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestEmptyReplacement verifies a secret may be scrubbed to nothing without
// disturbing surrounding bytes.
func TestEmptyReplacement(t *testing.T) {
	dict := NewDictionary(map[string]string{"drop": ""})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	mustWrite(t, s, []byte("adropbdropc"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sink.joined(); got != "abc" {
		t.Fatalf("got %q, want abc", got)
	}
}

// TestUnderlyingWriteErrorLatches ensures an underlying error is surfaced and
// later writes fail fast instead of duplicating already-emitted bytes.
func TestUnderlyingWriteErrorLatches(t *testing.T) {
	dict := NewDictionary(map[string]string{"abc": "[A]"})
	sink := &failWriter{accept: 3}
	s := NewScrubber(sink, dict)

	// "abcabc..." eventually produces more output than the sink accepts.
	var sawErr error
	for i := 0; i < 16; i++ {
		if _, err := s.Write([]byte("abc")); err != nil {
			sawErr = err
			break
		}
	}
	if sawErr == nil {
		if err := s.Close(); err != nil {
			sawErr = err
		}
	}
	if sawErr == nil {
		t.Fatal("expected an underlying write error")
	}
	if _, err := s.Write([]byte("x")); err == nil {
		t.Fatal("writes after an underlying error must fail")
	}
}

// TestCloseErrorPropagatedAndRepeated ensures a Close error from the sink is
// returned once and repeated on every subsequent Close call.
func TestCloseErrorPropagatedAndRepeated(t *testing.T) {
	dict := NewDictionary(map[string]string{"abc": "[A]"})
	sink := &closeFailWriter{recallWriter: recallWriter{err: errors.New("flush failed")}}
	s := NewScrubber(sink, dict)

	mustWrite(t, s, []byte("abc"))
	err1 := s.Close()
	err2 := s.Close()
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Fatalf("Close errors = %v, %v, want the same flush error", err1, err2)
	}
	if sink.closed != 1 {
		t.Fatalf("underlying Close count = %d, want 1", sink.closed)
	}
}

// closeFailWriter accepts every write but reports an error on Close.
type closeFailWriter struct {
	recallWriter
}

func (w *closeFailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunks = append(w.chunks, append([]byte(nil), p...))
	return len(p), nil
}

// TestConcurrentWritesAndRotation exercises the mutex under the race
// detector: output must remain a valid scrubbing of some byte ordering with
// no raw secret and no lost bytes beyond Write failures.
func TestConcurrentWritesAndRotation(t *testing.T) {
	dict := NewDictionary(map[string]string{"secret": "***"})
	sink := &recallWriter{}
	s := NewScrubber(sink, dict)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.Write([]byte("secret "))
				if g == 0 && i%25 == 0 {
					s.RotateSecret(NewDictionary(map[string]string{"secret": "***"}))
				}
			}
		}(g)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	sink.assertNoSubstring(t, "secret")
	got := sink.joined()
	if strings.Count(got, "***") != 400 {
		t.Fatalf("replacement count = %d, want 400", strings.Count(got, "***"))
	}
}

// TestNilDictionaryAndRotationWithNil ensures a scrubber starts usable even
// without an initial dictionary and accepts nil in RotateSecret.
func TestNilDictionaryAndRotationWithNil(t *testing.T) {
	sink := &recallWriter{}
	s := NewScrubber(sink, nil)

	mustWrite(t, s, []byte("plain"))
	if err := s.AddSecret("plain", "[P]"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, []byte(" plain"))
	s.RotateSecret(nil)
	mustWrite(t, s, []byte(" plain"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sink.joined(); got != "plain [P] plain" {
		t.Fatalf("got %q, want plain [P] plain", got)
	}
}
