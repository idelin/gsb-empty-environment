package netsec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRedactKernelPointers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "prefixed lower",
			in:   "ptr=0xffff888012345678 end",
			want: "ptr=" + Placeholder + " end",
		},
		{
			name: "prefixed upper",
			in:   "PTR=0XFFFF888012345678 END",
			want: "PTR=" + Placeholder + " END",
		},
		{
			name: "short prefixed",
			in:   "ptr=0xffff8880 end",
			want: "ptr=" + Placeholder + " end",
		},
		{
			name: "unprefixed pK output",
			in:   "kptr=ffff888012345678 tail",
			want: "kptr=" + Placeholder + " tail",
		},
		{
			name: "unprefixed uppercase",
			in:   "kptr=FFFF888012345678 tail",
			want: "kptr=" + Placeholder + " tail",
		},
		{
			name: "little endian bytes",
			in:   "dump=78 56 34 12 80 88 ff ff done",
			want: "dump=" + Placeholder + " done",
		},
		{
			name: "little endian zero prefixed bytes",
			in:   "dump=0x78, 0x56, 0x34, 0x12, 0x80, 0x88, 0xff, 0xff done",
			want: "dump=" + Placeholder + " done",
		},
		{
			name: "little endian colon separated bytes",
			in:   "dump=78:56:34:12:80:88:ff:ff done",
			want: "dump=" + Placeholder + " done",
		},
		{
			name: "little endian escaped bytes",
			in:   `dump=\x78\x56\x34\x12\x80\x88\xff\xff done`,
			want: "dump=" + Placeholder + ` done`,
		},
		{
			name: "little endian contiguous",
			in:   "dump=785634128088ffff done",
			want: "dump=" + Placeholder + " done",
		},
		{
			name: "multiple pointers",
			in:   "a=0xffff888012345678 b=ffff8880abcdef01 c=0xffffffff81000000",
			want: "a=" + Placeholder + " b=" + Placeholder + " c=" + Placeholder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Redact(tt.in); got != tt.want {
				t.Fatalf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactPreservesHashesAndUserAddresses(t *testing.T) {
	tests := []string{
		"hash=deadbeefcafebabe",
		"hash=0123456789abcdef0123456789abcdef",
		"sha1=da39a3ee5e6b4b0d3255bfef95601890afd80709",
		"sha256=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"user=0x00007ffff7a0d000",
		"user=0x0000555555555160",
		"user=00007ffff7a0d000",
		"user=0x00007fffffffffff",
		"user=00007fffffffffff",
		"null=0000000000000000",
		"user bytes=78 56 34 12 78 56 00 00",
		"timestamp=1723456789012345",
		"uuid=123e4567-e89b-12d3-a456-426614174000",
		"long=ffff8880123456789",
		"hashed pK=00000000deadbeef",
	}
	for _, in := range tests {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestRedactByteDumpWindows(t *testing.T) {
	in := "data 11 22 33 44 55 66 77 88 78 56 34 12 80 88 ff ff tail"
	want := "data 11 22 33 44 55 66 77 88 " + Placeholder + " tail"
	if got := Redact(in); got != want {
		t.Fatalf("Redact(%q) = %q, want %q", in, got, want)
	}
}

func TestRedactHexdumpLinePreservesOffset(t *testing.T) {
	in := "00000000: 78 56 34 12 80 88 ff ff  ................"
	want := "00000000: " + Placeholder + "  ................"
	if got := Redact(in); got != want {
		t.Fatalf("Redact(%q) = %q, want %q", in, got, want)
	}
}

func TestRedactMixedText(t *testing.T) {
	in := strings.Join([]string{
		"boot: kernel ptr 0xffff888012345678",
		"hash e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"regs: 78 56 34 12 80 88 ff ff",
		"user: 0x00007ffff7a0d000",
		"raw: ffff8880abcdef01",
	}, "\n")
	want := strings.Join([]string{
		"boot: kernel ptr " + Placeholder,
		"hash e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"regs: " + Placeholder,
		"user: 0x00007ffff7a0d000",
		"raw: " + Placeholder,
	}, "\n")
	if got := Redact(in); got != want {
		t.Fatalf("Redact mixed text:\n got: %q\nwant: %q", got, want)
	}
}

func TestRedactAddressesAcrossLines(t *testing.T) {
	in := "stack:\n" +
		"  [<ffffffff81000000>]\n" +
		"  regs=0xffff888012345678\n" +
		"  dump=78 56 34 12 80 88 ff ff\n" +
		"  user=0x00007ffff7a0d000\n"
	want := "stack:\n" +
		"  [<" + Placeholder + ">]\n" +
		"  regs=" + Placeholder + "\n" +
		"  dump=" + Placeholder + "\n" +
		"  user=0x00007ffff7a0d000\n"
	if got := Redact(in); got != want {
		t.Fatalf("Redact across lines:\n got: %q\nwant: %q", got, want)
	}
}

func TestWriterMatchesBatchAcrossChunkSizes(t *testing.T) {
	in := "start 0xffff888012345678\r\n" +
		"hash 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n" +
		"user 0x00007ffff7a0d000\n" +
		"dump 78 56 34 12 80 88 ff ff\n" +
		"pK FFFF8880ABCDEF01 and 0xffffffff81000000\n" +
		"tail"
	want := Redact(in)

	for size := 1; size <= len(in); size++ {
		var dst bytes.Buffer
		r := NewRedactor(&dst)
		for pos := 0; pos < len(in); pos += size {
			end := pos + size
			if end > len(in) {
				end = len(in)
			}
			if _, err := r.Write([]byte(in[pos:end])); err != nil {
				t.Fatalf("chunk size %d: Write: %v", size, err)
			}
		}
		if err := r.Close(); err != nil {
			t.Fatalf("chunk size %d: Close: %v", size, err)
		}
		if got := dst.String(); got != want {
			t.Fatalf("chunk size %d:\n got: %q\nwant: %q", size, got, want)
		}
	}
}

func TestWriterMatchesBatchAcrossEverySplit(t *testing.T) {
	in := "a=0xffff888012345678\nb=ffff8880abcdef01\nc=785634128088ffff\nd=hash0123456789abcdef\n"
	want := Redact(in)

	for split := 0; split <= len(in); split++ {
		var dst bytes.Buffer
		r := NewRedactor(&dst)
		if _, err := r.Write([]byte(in[:split])); err != nil {
			t.Fatalf("split %d: first Write: %v", split, err)
		}
		if _, err := r.Write([]byte(in[split:])); err != nil {
			t.Fatalf("split %d: second Write: %v", split, err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("split %d: Close: %v", split, err)
		}
		if got := dst.String(); got != want {
			t.Fatalf("split %d:\n got: %q\nwant: %q", split, got, want)
		}
	}
}

func TestWriterFlushesWithoutTrailingNewline(t *testing.T) {
	var dst bytes.Buffer
	r := NewRedactor(&dst)
	if _, err := r.Write([]byte("ptr=0xffff888012345678")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, want := dst.String(), "ptr="+Placeholder; got != want {
		t.Fatalf("Flush output = %q, want %q", got, want)
	}
}

func TestWriterEmitsCompletedLineEarly(t *testing.T) {
	var dst bytes.Buffer
	r := NewRedactor(&dst)
	if _, err := r.Write([]byte("safe line\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := dst.String(), "safe line\n"; got != want {
		t.Fatalf("completed line output = %q, want %q", got, want)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRedactBytesAndAliases(t *testing.T) {
	in := []byte("0xffff888012345678")
	if got, want := string(RedactBytes(in)), Placeholder; got != want {
		t.Fatalf("RedactBytes = %q, want %q", got, want)
	}
	if got, want := RedactString("ffff888012345678"), Placeholder; got != want {
		t.Fatalf("RedactString = %q, want %q", got, want)
	}
}

func TestClosedWriter(t *testing.T) {
	var dst bytes.Buffer
	r := NewRedactor(&dst)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("x")); err != ErrClosed {
		t.Fatalf("Write after Close error = %v, want ErrClosed", err)
	}
}

func ExampleRedact() {
	fmt.Println(Redact("kptr=0xffff888012345678"))
	// Output: kptr=[REDACTED]
}
