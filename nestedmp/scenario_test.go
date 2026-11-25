package nestedmp

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPrefixBoundarySafety(t *testing.T) {
	s := buildSample(t)
	if _, err := ReferenceParse(s.msg, rootB); err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{1, 2, 3, 5, 7, 13, 64, 128} {
		if _, _, _, err := runStream(t, splitEvery(s.msg, cut), Options{}); err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
	}
}

func TestEmptyAndCRLFBodies(t *testing.T) {
	s := buildSample(t)
	_, host, _, err := runStream(t, [][]byte{s.msg}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := host.txn.visible("empty"); !ok || len(got) != 0 {
		t.Fatalf("empty body representation broken: ok=%v len=%d", ok, len(got))
	}
	got, _ := host.txn.visible("crlf")
	if !bytes.HasSuffix(got, []byte("\r\n")) {
		t.Fatalf("CRLF-terminated body lost trailing CRLF: %q", got)
	}
}

func TestPartialDelimiterOnlyInBody(t *testing.T) {
	body := []byte("a--AB\r\nb--ABC--\r\nc--ABX\r\nd")
	var b wireBuilder
	b.open(rootB)
	b.header(TypeAttachment, "p", digestOf(body), "")
	b.body(body)
	b.close(rootB)
	msg := b.bytes()
	res, host, _, err := runStream(t, splitEvery(msg, 1), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Parts[0].Size != int64(len(body)) {
		t.Fatalf("size=%d want=%d", res.Parts[0].Size, len(body))
	}
	got, _ := host.txn.visible("p")
	if !bytes.Equal(got, body) {
		t.Fatalf("partial delimiter bytes altered: %q", got)
	}
}

func buildInner(t *testing.T) *wireBuilder {
	t.Helper()
	payload := []byte("inner-data")
	var b wireBuilder
	b.open(innerB)
	b.header(TypeAttachment, "inner", digestOf(payload), "")
	b.body(payload)
	b.close(innerB)
	return &b
}

func expectRollback(t *testing.T, host *fakeHost, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
	if !host.txn.rolled || host.txn.committed {
		t.Fatalf("rollback state wrong: rolled=%v committed=%v", host.txn.rolled, host.txn.committed)
	}
	for name := range host.txn.sinks {
		if _, ok := host.txn.visible(name); ok {
			t.Fatalf("staged part %q became readable after rollback", name)
		}
	}
}

func TestParentDigestFailureRollsBackEverything(t *testing.T) {
	inner := buildInner(t)
	var root wireBuilder
	root.open(rootB)
	root.header(TypeAttachment, "safe", digestOf([]byte("safe-data")), "")
	root.body([]byte("safe-data"))
	root.open(rootB)
	bad := digestOf(inner.bytes())
	bad[0] ^= 0xFF
	root.header(TypeContainer, "box", bad, innerB)
	root.body(inner.bytes())
	root.close(rootB)

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range splitEvery(root.bytes(), 7) {
		if _, err := p.WriteCtx(context.Background(), ch); err != nil {
			expectRollback(t, host, err, ErrDigest)
			return
		}
	}
	_, err = p.CloseCtx(context.Background())
	expectRollback(t, host, err, ErrDigest)
}

func TestLateCommit(t *testing.T) {
	s := buildSample(t)
	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(s.msg); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	host.txn.commitCh = release
	done := make(chan error, 1)
	go func() {
		_, e := p.CloseCtx(context.Background())
		done <- e
	}()
	for name := range host.txn.sinks {
		if _, ok := host.txn.visible(name); ok {
			t.Fatalf("part %q readable before commit returns", name)
		}
	}
	select {
	case <-done:
		t.Fatal("Close returned while commit callback still pending")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, ok := host.txn.visible("first")
	if !ok || !bytes.Equal(got, s.want["first"]) {
		t.Fatal("committed part not readable after late commit")
	}
}
