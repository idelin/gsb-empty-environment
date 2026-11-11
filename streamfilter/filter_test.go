package streamfilter

import "testing"

func TestFilterEchoOnPassthrough(t *testing.T) {
	f := NewFilter(0)
	if got := f.FilterInput([]byte("ls -la\r")); string(got) != "ls -la\r" {
		t.Fatalf("input: got %q", got)
	}
	if got := f.FilterOutput([]byte("total 0\r\n")); string(got) != "total 0\r\n" {
		t.Fatalf("output: got %q", got)
	}
}

func TestFilterSuppressesInputAndLateEcho(t *testing.T) {
	f := NewFilter(0)
	f.SetEcho(false)

	if got := f.FilterInput([]byte("s3cr3t\r")); got != nil {
		t.Fatalf("masked input returned %q", got)
	}
	if n := f.PendingEcho(); n != len("s3cr3t\r") {
		t.Fatalf("pending = %d", n)
	}

	// The late echo arrives glued to the next prompt; only the echo
	// bytes may be removed.
	got := f.FilterOutput([]byte("s3cr3t\r\nPassword: "))
	if want := "\nPassword: "; string(got) != want {
		t.Fatalf("output: got %q, want %q", got, want)
	}
	if n := f.PendingEcho(); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
}

func TestFilterRestoreKeepsNormalOutput(t *testing.T) {
	f := NewFilter(0)
	f.SetEcho(false)
	f.FilterInput([]byte("ab\r")) // never echoed
	f.SetEcho(true)

	// Legitimate output that happens to contain the same bytes must
	// survive untouched once echo is restored.
	if got := f.FilterOutput([]byte("ab\r\n")); string(got) != "ab\r\n" {
		t.Fatalf("output: got %q", got)
	}
	if got := f.FilterInput([]byte("id\r")); string(got) != "id\r" {
		t.Fatalf("input: got %q", got)
	}
}

func TestFilterSplitUTF8AndCSI(t *testing.T) {
	f := NewFilter(0)
	f.SetEcho(false)

	zi := "密" // 3-byte UTF-8 sequence
	f.FilterInput([]byte(zi[:1]))
	f.FilterInput([]byte(zi[1:]))
	f.FilterInput([]byte("\r"))

	// The late echo is split across chunk boundaries and glued to a
	// prompt; byte-wise matching must still strip exactly the echo.
	if got := f.FilterOutput([]byte("password:" + zi[:2])); string(got) != "password:" {
		t.Fatalf("chunk 1: got %q", got)
	}
	if got := f.FilterOutput([]byte(zi[2:] + "\r\n")); string(got) != "\n" {
		t.Fatalf("chunk 2: got %q", got)
	}

	// Echo restored: a CSI sequence split across chunks passes through
	// untouched, and split multi-byte input is recorded intact.
	f.SetEcho(true)
	csi := "\x1b[1;31mred\x1b[0m"
	var rec []byte
	rec = append(rec, f.FilterOutput([]byte(csi[:5]))...)
	rec = append(rec, f.FilterOutput([]byte(csi[5:]))...)
	if string(rec) != csi {
		t.Fatalf("csi: got %q", rec)
	}
	var rin []byte
	rin = append(rin, f.FilterInput([]byte(zi[:2]))...)
	rin = append(rin, f.FilterInput([]byte(zi[2:]))...)
	if string(rin) != zi {
		t.Fatalf("input: got %q", rin)
	}
}

func TestFilterPendingBounded(t *testing.T) {
	f := NewFilter(8)
	f.SetEcho(false)
	f.FilterInput([]byte("0123456789"))
	if n := f.PendingEcho(); n != 8 {
		t.Fatalf("pending = %d, want 8", n)
	}
	// The oldest bytes were dropped, so only the tail echo matches.
	if got := f.FilterOutput([]byte("23456789")); len(got) != 0 {
		t.Fatalf("output: got %q", got)
	}
}
