package netsec

import (
	"errors"
	"io"
	"reflect"
	"testing"
)

// chunkReader yields its payload in chunks of at most n bytes per Read, so
// tests can split malicious sequences across read boundaries.
type chunkReader struct {
	data []byte
	n    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(r.data) {
		n = len(r.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func mergeAll(string) MergePolicy { return CommaMerge }

func TestValidateNameRejectsInjection(t *testing.T) {
	bad := []string{
		"",
		"X-Bad\rName",
		"X-Bad\nName",
		"X-Bad\r\nInjected: yes",
		"Bad Name",
		"Bad:Name",
		"Bad\x00Name",
		"Bad\x7fName",
	}
	for _, name := range bad {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", name)
		}
	}
	good := []string{"Set-Cookie", "x-custom-1", "A!#$%&'*+-.^_`|~z09"}
	for _, name := range good {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateValueRejectsInjection(t *testing.T) {
	bad := []string{
		"ok\r\nInjected: evil",
		"ok\r",
		"ok\n",
		"\r\nHTTP/1.1 200 OK",
		"nul\x00byte",
		"del\x7fbyte",
	}
	for _, value := range bad {
		if err := ValidateValue(value); err == nil {
			t.Errorf("ValidateValue(%q) = nil, want error", value)
		}
	}
	good := []string{"", "plain", "with\ttab", "obs-text\xc3\xa9", " spaced "}
	for _, value := range good {
		if err := ValidateValue(value); err != nil {
			t.Errorf("ValidateValue(%q) = %v, want nil", value, err)
		}
	}
}

func TestFailedAddStagesNothing(t *testing.T) {
	b := NewBuilder(nil)
	if err := b.Add("X-Good", "a"); err != nil {
		t.Fatalf("Add good: %v", err)
	}
	if err := b.Add("X-Good", "evil\r\nInjected: 1"); err == nil {
		t.Fatal("Add with CRLF = nil, want error")
	}
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got := snap.Values("X-Good"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("Values = %q, want only the pre-failure instance", got)
	}
}

func TestPreserveMultiInstanceByDefault(t *testing.T) {
	b := NewBuilder(nil)
	mustAdd(t, b, "Set-Cookie", "a=1; Path=/")
	mustAdd(t, b, "Set-Cookie", "b=2; Path=/")
	mustAdd(t, b, "X-Trace", "t1")
	mustAdd(t, b, "X-Trace", "t2")
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	want := []Entry{
		{Name: "Set-Cookie", Value: "a=1; Path=/"},
		{Name: "Set-Cookie", Value: "b=2; Path=/"},
		{Name: "X-Trace", Value: "t1"},
		{Name: "X-Trace", Value: "t2"},
	}
	if got := snap.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries = %+v, want %+v", got, want)
	}
}

func TestCommaMergePolicy(t *testing.T) {
	b := NewBuilder(func(canonical string) MergePolicy {
		if canonical == "Vary" {
			return CommaMerge
		}
		return PreserveInstances
	})
	mustAdd(t, b, "Vary", "Accept-Encoding")
	mustAdd(t, b, "Vary", "Accept-Language")
	mustAdd(t, b, "X-Trace", "t1")
	mustAdd(t, b, "X-Trace", "t2")
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	want := []Entry{
		{Name: "Vary", Value: "Accept-Encoding, Accept-Language"},
		{Name: "X-Trace", Value: "t1"},
		{Name: "X-Trace", Value: "t2"},
	}
	if got := snap.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries = %+v, want %+v", got, want)
	}
}

func TestSetCookieNeverCommaMerged(t *testing.T) {
	b := NewBuilder(mergeAll)
	mustAdd(t, b, "set-cookie", "a=1")
	mustAdd(t, b, "SET-COOKIE", "b=2")
	mustAdd(t, b, "Warning", "w1")
	mustAdd(t, b, "Warning", "w2")
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	want := []Entry{
		{Name: "Set-Cookie", Value: "a=1"},
		{Name: "Set-Cookie", Value: "b=2"},
		{Name: "Warning", Value: "w1, w2"},
	}
	if got := snap.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries = %+v, want %+v", got, want)
	}
}

func TestEmptyInstancePreserved(t *testing.T) {
	b := NewBuilder(nil)
	mustAdd(t, b, "X-Empty", "")
	mustAdd(t, b, "X-Empty", "filled")
	mustAdd(t, b, "X-Empty", "")
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got := snap.Values("X-Empty"); !reflect.DeepEqual(got, []string{"", "filled", ""}) {
		t.Fatalf("Values = %q, want empty instances preserved", got)
	}
}

func TestAddReaderAcrossChunks(t *testing.T) {
	payload := "chunked value with\ttab and obs \xc3\xa9 tail"
	for _, size := range []int{1, 2, 3, 4096} {
		b := NewBuilder(nil)
		r := &chunkReader{data: []byte(payload), n: size}
		if err := b.AddReader("X-Stream", r); err != nil {
			t.Fatalf("chunk %d: AddReader: %v", size, err)
		}
		snap, err := b.Seal()
		if err != nil {
			t.Fatalf("chunk %d: Seal: %v", size, err)
		}
		if got := snap.Values("X-Stream"); !reflect.DeepEqual(got, []string{payload}) {
			t.Fatalf("chunk %d: Values = %q, want %q", size, got, payload)
		}
	}
}

func TestAddReaderCatchesCRLFSplitAcrossChunks(t *testing.T) {
	payload := "front\r\nInjected: evil"
	for _, size := range []int{1, 2, 5, 6, 7, 4096} {
		b := NewBuilder(nil)
		mustAdd(t, b, "X-Safe", "kept")
		r := &chunkReader{data: []byte(payload), n: size}
		if err := b.AddReader("X-Stream", r); err == nil {
			t.Fatalf("chunk %d: AddReader = nil, want CRLF error", size)
		}
		snap, err := b.Seal()
		if err != nil {
			t.Fatalf("chunk %d: Seal: %v", size, err)
		}
		if got := snap.Values("X-Stream"); len(got) != 0 {
			t.Fatalf("chunk %d: failed stream staged %q", size, got)
		}
		if got := snap.Values("X-Safe"); !reflect.DeepEqual(got, []string{"kept"}) {
			t.Fatalf("chunk %d: prior staged value lost: %q", size, got)
		}
	}
}

func TestPublishFailureKeepsOldSnapshot(t *testing.T) {
	sealer := NewSealer()

	good := NewBuilder(nil)
	mustAdd(t, good, "Set-Cookie", "a=1")
	mustAdd(t, good, "X-Trace", "t1")
	first, err := sealer.Publish(good)
	if err != nil {
		t.Fatalf("Publish good: %v", err)
	}

	bad := NewBuilder(nil)
	mustAdd(t, bad, "X-Trace", "t2")
	if err := bad.Add("X-Inject", "x\r\nInjected: evil"); err == nil {
		t.Fatal("Add with CRLF = nil, want error")
	}
	if err := bad.Add("Bad\rName", "x"); err == nil {
		t.Fatal("Add with CR in name = nil, want error")
	}

	second, err := sealer.Publish(bad)
	if err != nil {
		t.Fatalf("Publish of still-valid builder: %v", err)
	}
	if got := second.Values("X-Trace"); !reflect.DeepEqual(got, []string{"t2"}) {
		t.Fatalf("X-Trace = %q, failed adds must not be staged", got)
	}

	if _, err := sealer.Publish(nil); !errors.Is(err, ErrNilBuilder) {
		t.Fatalf("Publish(nil) = %v, want ErrNilBuilder", err)
	}
	if got := sealer.Current(); got != second {
		t.Fatalf("Current changed after failed publish: got %v want %v", got, second)
	}
	if got := first.Values("Set-Cookie"); !reflect.DeepEqual(got, []string{"a=1"}) {
		t.Fatalf("Set-Cookie = %q, old snapshot must survive failures", got)
	}
}

func TestSnapshotImmutable(t *testing.T) {
	b := NewBuilder(nil)
	mustAdd(t, b, "X-Trace", "t1")
	snap, err := b.Seal()
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	entries := snap.Entries()
	entries[0].Value = "mutated"
	if got := snap.Values("X-Trace"); !reflect.DeepEqual(got, []string{"t1"}) {
		t.Fatalf("snapshot mutated through Entries copy: %q", got)
	}
	// Sealing again must not be affected by later builder mutation.
	mustAdd(t, b, "X-Trace", "t2")
	if got := snap.Values("X-Trace"); !reflect.DeepEqual(got, []string{"t1"}) {
		t.Fatalf("sealed snapshot changed with builder: %q", got)
	}
}

func mustAdd(t *testing.T, b *Builder, name, value string) {
	t.Helper()
	if err := b.Add(name, value); err != nil {
		t.Fatalf("Add(%q, %q): %v", name, value, err)
	}
}
