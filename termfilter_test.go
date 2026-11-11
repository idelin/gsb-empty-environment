package termfilter

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func TestFilterPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "c0 controls pass through",
			in:   "a\nb\tc\r\nd",
			want: "a\nb\tc\r\nd",
		},
		{
			name: "sgr color kept",
			in:   "\x1b[31mred\x1b[0m",
			want: "\x1b[31mred\x1b[0m",
		},
		{
			name: "sgr 256 color kept",
			in:   "\x1b[38;5;200mX\x1b[48;5;17m",
			want: "\x1b[38;5;200mX\x1b[48;5;17m",
		},
		{
			name: "sgr true color kept",
			in:   "\x1b[38;2;1;2;3mX\x1b[m",
			want: "\x1b[38;2;1;2;3mX\x1b[m",
		},
		{
			name: "cursor movement kept",
			in:   "\x1b[5A\x1b[3B\x1b[2C\x1b[D\x1b[2;3H\x1b[G\x1b[10d\x1b[4`\x1b[2;5f",
			want: "\x1b[5A\x1b[3B\x1b[2C\x1b[D\x1b[2;3H\x1b[G\x1b[10d\x1b[4`\x1b[2;5f",
		},
		{
			name: "erase kept",
			in:   "\x1b[2J\x1b[K",
			want: "\x1b[2J\x1b[K",
		},
		{
			name: "osc 52 clipboard removed",
			in:   "\x1b]52;c;Zm9v\x07after",
			want: "after",
		},
		{
			name: "osc 52 st terminated removed",
			in:   "\x1b]52;c;Zm9v\x1b\\after",
			want: "after",
		},
		{
			name: "osc title removed",
			in:   "pre\x1b]0;evil title\x07post",
			want: "prepost",
		},
		{
			name: "osc 8 hyperlink removed",
			in:   "\x1b]8;;http://evil.example/\x1b\\label\x1b]8;;\x1b\\done",
			want: "labeldone",
		},
		{
			name: "dcs removed",
			in:   "\x1bP1$u\x1b\\tail",
			want: "tail",
		},
		{
			name: "apc removed",
			in:   "\x1b_Gi=31;base64data\x1b\\tail",
			want: "tail",
		},
		{
			name: "pm removed",
			in:   "\x1b^note\x1b\\tail",
			want: "tail",
		},
		{
			name: "simple esc sequences removed",
			in:   "\x1bc\x1b7\x1b8\x1b[?25h\x1b[?1049l",
			want: "",
		},
		{
			name: "private mode csi removed",
			in:   "\x1b[?2004hX\x1b[>1;1;1m",
			want: "X",
		},
		{
			name: "non allowlisted csi removed",
			in:   "\x1b[2n\x1b[6n\x1b[c\x1b[?12l",
			want: "",
		},
		{
			name: "standalone c1 controls removed",
			in:   "a\x85b\x8ec\x90",
			want: "abc",
		},
		{
			name: "eight bit csi sgr kept",
			in:   "\x9b31mred\x9b0m",
			want: "\x9b31mred\x9b0m",
		},
		{
			name: "eight bit osc removed",
			in:   "\x9d52;c;x\x9ctail",
			want: "tail",
		},
		{
			name: "eight bit dcs removed",
			in:   "\x90payload\x9ctail",
			want: "tail",
		},
		{
			name: "can cancels csi",
			in:   "\x1b[31\x18tail",
			want: "tail",
		},
		{
			name: "sub cancels csi",
			in:   "\x1b[31\x1atail",
			want: "tail",
		},
		{
			name: "esc restarts sequence",
			in:   "\x1b[\x1b[31mX",
			want: "\x1b[31mX",
		},
		{
			name: "c1 csi introducer restarts sequence",
			in:   "\x1b[31\x9b31mX",
			want: "\x9b31mX",
		},
		{
			name: "utf8 preserved",
			in:   "中文\x1b[32m测试\x1b[0m🙂",
			want: "中文\x1b[32m测试\x1b[0m🙂",
		},
		{
			name: "invalid utf8 byte forwarded",
			in:   "a\xffb\xfec",
			want: "a\xffb\xfec",
		},
		{
			name: "lone c1 stripped continuation forwarded",
			in:   "\x80\xbfx",
			want: "\xbfx",
		},
		{
			name: "c1 range boundary",
			in:   "a\x7fb\x9fc\x9cd\xc0e\xc1",
			want: "a\x7fbd\xc0e\xc1",
		},
		{
			name: "osc with embedded esc not backslash",
			in:   "\x1b]52;x\x1bA\x07tail",
			want: "tail",
		},
		{
			name: "nested esc inside string",
			in:   "\x1b]x\x1b\x1b\\tail",
			want: "tail",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Filter([]byte(tc.in)); string(got) != tc.want {
				t.Errorf("Filter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// splitCorpus is a corpus exercising every parser state and boundary type.
// It feeds the split-equivalence tests below.
func splitCorpus() [][]byte {
	longCSI := append([]byte("\x1b["), make([]byte, 2000)...)
	for i := 1; i < len(longCSI); i++ {
		longCSI[i] = '5'
	}
	longCSI = append(longCSI, 'm')

	longOSC := append([]byte("\x1b]52;c;"), make([]byte, 2000)...)
	longOSC = append(longOSC, 0x07, 'Z')

	return [][]byte{
		[]byte("plain text only"),
		[]byte("\x1b[31mred\x1b[0m normal \x1b[38;2;1;2;3mX\x1b[m"),
		[]byte("\x1b[5A\x1b[2;3Hmove"),
		[]byte("\x1b]52;c;Zm9v\x07after \x1b]0;title\x1b\\end"),
		[]byte("\x1bP1$udcs\x1b\\ \x1b_Gapc\x1b\\ \x1b^pm\x1b\\!"),
		[]byte("\x1b[?25hprivate\x1b[?1049l"),
		[]byte("中文混合\x1b[32m颜色🙂\x1b[0m结尾"),
		[]byte("\x9b31m8bit\x9b0m \x9dosc\x9c \x90dcs\x9c"),
		[]byte("cancel\x1b[31\x18ok \x1b[2n\x1asub"),
		[]byte("\x1b[\x1b[31mrestart \x1b[31\x9b31mboth"),
		[]byte("a\xffb\x80\xbfc\xf0\x9f"),
		[]byte("\x1b]x\x1b\x1b\\escaped \x1b]y\x1bAbel"),
		longCSI,
		longOSC,
		[]byte("trailing incomplete \x1b[31"),
		[]byte("\x1b]unterminated osc payload never emitted"),
	}
}

// feedChunks drives one fresh Writer with p split into consecutive chunks.
func feedChunks(t *testing.T, p []byte, cuts []int, opts ...Option) []byte {
	t.Helper()
	var out bytes.Buffer
	w := NewWriter(&out, opts...)
	start := 0
	for _, cut := range cuts {
		if cut > start {
			if _, err := w.Write(p[start:cut]); err != nil {
				t.Fatalf("Write: %v", err)
			}
			start = cut
		}
	}
	if start < len(p) {
		if _, err := w.Write(p[start:]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return out.Bytes()
}

// TestEverySplitMatchesFilter enumerates every single cut position of every
// corpus entry: the stream is delivered in two chunks at that position, and
// the incremental result must equal the one-shot Filter result byte for byte.
func TestEverySplitMatchesFilter(t *testing.T) {
	t.Parallel()

	for _, p := range splitCorpus() {
		p := p
		want := Filter(p)
		for cut := 0; cut <= len(p); cut++ {
			cut := cut
			t.Run("", func(t *testing.T) {
				t.Parallel()
				got := feedChunks(t, p, []int{cut})
				if !bytes.Equal(got, want) {
					t.Fatalf("cut=%d in %q: streamed %q, want %q", cut, p, got, want)
				}
			})
		}
	}
}

// TestByteByByteMatchesFilter feeds one byte per Write, splitting every
// possible multi-byte rune and sequence.
func TestByteByByteMatchesFilter(t *testing.T) {
	t.Parallel()

	for _, p := range splitCorpus() {
		p := p
		want := Filter(p)
		cuts := make([]int, len(p))
		for i := range cuts {
			cuts[i] = i
		}
		if got := feedChunks(t, p, cuts); !bytes.Equal(got, want) {
			t.Fatalf("byte-by-byte %q: got %q, want %q", p, got, want)
		}
	}
}

// TestRandomChunkingMatchesFilter uses deterministic pseudo-random chunk
// boundaries, including empty-friendly cuts, and compares against Filter.
func TestRandomChunkingMatchesFilter(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(1))
	for _, p := range splitCorpus() {
		p := p
		want := Filter(p)
		for iter := 0; iter < 200; iter++ {
			var cuts []int
			pos := 0
			for pos < len(p) {
				pos += 1 + rng.Intn(4)
				if pos < len(p) {
					cuts = append(cuts, pos)
				}
			}
			if got := feedChunks(t, p, cuts); !bytes.Equal(got, want) {
				t.Fatalf("iter=%d cuts=%v on %q: got %q, want %q", iter, cuts, p, got, want)
			}
		}
	}
}

// TestUTF8NeverSplitAcrossWrites verifies a rune is never emitted piecemeal:
// partial writes produce nothing until the rune completes.
func TestUTF8NeverSplitAcrossWrites(t *testing.T) {
	t.Parallel()

	runeBytes := []byte("🙂") // 4-byte rune
	for cut := 1; cut < len(runeBytes); cut++ {
		var out bytes.Buffer
		w := NewWriter(&out)
		if _, err := w.Write(runeBytes[:cut]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("cut=%d emitted %d bytes before rune completed: %q", cut, out.Len(), out.Bytes())
		}
		if _, err := w.Write(runeBytes[cut:]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if !bytes.Equal(out.Bytes(), runeBytes) {
			t.Fatalf("cut=%d: got %q, want %q", cut, out.Bytes(), runeBytes)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
}

// TestFlushDropsUnterminatedSequence ensures an unterminated sequence held at
// Flush time never reaches the output, while later text still flows.
func TestFlushDropsUnterminatedSequence(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	w := NewWriter(&out)
	_, _ = w.Write([]byte("ok\x1b[31"))
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_, _ = w.Write([]byte("after"))
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := out.String(), "okafter"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestFlushEmitsIncompleteUTF8 matches the one-shot policy of forwarding a
// truncated rune verbatim rather than silently dropping it.
func TestFlushEmitsIncompleteUTF8(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	w := NewWriter(&out)
	partial := []byte{0xF0, 0x9F}
	_, _ = w.Write(partial)
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if !bytes.Equal(out.Bytes(), partial) {
		t.Fatalf("got %q, want %q", out.Bytes(), partial)
	}
}

// TestSequenceLimitTruncates verifies overlong sequences are truncated and
// that parsing recovers cleanly once the terminator arrives.
func TestSequenceLimitTruncates(t *testing.T) {
	t.Parallel()

	limit := 16

	overlongCSI := []byte("\x1b[")
	for len(overlongCSI) < limit+10 {
		overlongCSI = append(overlongCSI, '5')
	}
	overlongCSI = append(overlongCSI, 'm', 'X')
	if got := Filter(overlongCSI, MaxSequenceBytes(limit)); string(got) != "X" {
		t.Fatalf("overlong CSI leaked: %q", got)
	}

	overlongOSC := []byte("\x1b]52;c;")
	for len(overlongOSC) < limit+10 {
		overlongOSC = append(overlongOSC, 'A')
	}
	overlongOSC = append(overlongOSC, 0x07, 'Y')
	if got := Filter(overlongOSC, MaxSequenceBytes(limit)); string(got) != "Y" {
		t.Fatalf("overlong OSC leaked: %q", got)
	}

	// A short, allowlisted sequence after an overlong one is still forwarded.
	combined := append(append([]byte{}, overlongCSI...), []byte("\x1b[31mZ\x1b[0m")...)
	if got := Filter(combined, MaxSequenceBytes(limit)); string(got) != "X\x1b[31mZ\x1b[0m" {
		t.Fatalf("recovery failed: %q", got)
	}

	// An overlong string must set the marker while open and clear it after
	// its terminator, with no payload ever surfacing.
	var out bytes.Buffer
	w := NewWriter(&out, MaxSequenceBytes(8))
	_, _ = w.Write([]byte("\x1b]52;c;AAAAAAA"))
	if !w.stringOverlong {
		t.Fatal("expected stringOverlong to be set past the limit")
	}
	_, _ = w.Write([]byte("BBBB\x07"))
	if w.stringOverlong {
		t.Fatal("expected stringOverlong to clear after the terminator")
	}
	if out.Len() != 0 {
		t.Fatalf("overlong string payload leaked: %q", out.Bytes())
	}
}

// TestLimitRespectsSplitDelivery ensures truncation behaves identically when
// the overlong sequence is delivered one byte at a time.
func TestLimitRespectsSplitDelivery(t *testing.T) {
	t.Parallel()

	limit := 12
	in := []byte("\x1b[")
	for len(in) < 30 {
		in = append(in, '9')
	}
	in = append(in, 'm', 'o', 'k')

	oneShot := Filter(in, MaxSequenceBytes(limit))
	cuts := make([]int, len(in))
	for i := range cuts {
		cuts[i] = i
	}
	if got := feedChunks(t, in, cuts, MaxSequenceBytes(limit)); !bytes.Equal(got, oneShot) {
		t.Fatalf("split limit handling: %q vs %q", got, oneShot)
	}
	if string(oneShot) != "ok" {
		t.Fatalf("expected truncated sequence to vanish, got %q", oneShot)
	}
}

// TestResetClearsState verifies Reset drops buffered sequences and partial
// runes and makes the filter usable again.
func TestResetClearsState(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	w := NewWriter(&out)
	_, _ = w.Write([]byte("a\x1b[31"))
	_, _ = w.Write([]byte{0xF0, 0x9F})
	w.Reset()
	_, _ = w.Write([]byte("\x1b[32mok\x1b[0m"))
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := out.String(), "a\x1b[32mok\x1b[0m"; got != want {
		t.Fatalf("after Reset got %q, want %q", got, want)
	}
}

// TestWriterErrorSticky verifies a downstream error is surfaced by Write and
// Flush and stops further forwarding.
func TestWriterErrorSticky(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("downstream boom")
	w := NewWriter(writeFunc(func([]byte) (int, error) {
		return 0, sentinel
	}))
	n, err := w.Write([]byte("abc"))
	if n != 3 || !errors.Is(err, sentinel) {
		t.Fatalf("Write = (%d, %v), want (3, %v)", n, err, sentinel)
	}
	if err := w.Flush(); !errors.Is(err, sentinel) {
		t.Fatalf("Flush = %v, want %v", err, sentinel)
	}
}

// TestShortWriteReported verifies a partial write becomes io.ErrShortWrite.
func TestShortWriteReported(t *testing.T) {
	t.Parallel()

	w := NewWriter(writeFunc(func(b []byte) (int, error) {
		return len(b) - 1, nil
	}))
	if _, err := w.Write([]byte("ab")); err == nil {
		t.Fatal("expected io.ErrShortWrite, got nil")
	}
}

// TestEightBitPMAndAPC covers the remaining 8-bit C1 introducers.
func TestEightBitPMAndAPC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "\x9epm\x9ctail", want: "tail"},
		{in: "\x9fapc\x9ctail", want: "tail"},
		{in: "x\x9e\x9cy", want: "xy"},
		{in: "\x90d\x9c\x9da\x07z", want: "z"},
	}
	for _, tc := range tests {
		if got := Filter([]byte(tc.in)); string(got) != tc.want {
			t.Errorf("Filter(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestESCFinalByteVariants exercises simple ESC sequence final bytes and
// embedded control bytes that do not terminate the sequence.
func TestESCFinalByteVariants(t *testing.T) {
	t.Parallel()

	tests := []string{
		"\x1b(B",
		"\x1b)0",
		"\x1bM",
		"\x1bD",
		"\x1b H",
		"\x1b\x01c",
		"\x1b\x077",
	}
	for _, in := range tests {
		if got := Filter([]byte(in + "z")); string(got) != "z" {
			t.Errorf("Filter(%qz) = %q, want z", in, got)
		}
	}
}
