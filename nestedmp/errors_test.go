package nestedmp

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestUnexpectedEOF(t *testing.T) {
	body := []byte("data")
	var b wireBuilder
	b.open(rootB)
	b.header(TypeAttachment, "p", digestOf(body), "")
	b.body(body)
	// 缺少结束行。
	msg := b.bytes()

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(msg); err != nil {
		t.Fatal(err)
	}
	_, err = p.CloseCtx(context.Background())
	if !errors.Is(err, ErrEOF) {
		t.Fatalf("want ErrEOF, got %v", err)
	}
	if !host.txn.rolled {
		t.Fatal("truncated stream must roll back")
	}
}

func TestMalformedHeaderRejected(t *testing.T) {
	msg := []byte("--" + rootB + "\r\nType: attachment\r\nName: p\r\n\r\nbody\r\n--" + rootB + "--\r\n")
	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Write(msg)
	if !errors.Is(err, ErrHeader) {
		t.Fatalf("want ErrHeader for missing digest, got %v", err)
	}
	if !host.txn.rolled {
		t.Fatal("malformed header must roll back")
	}
}

func TestSinkWriteFailureRollsBack(t *testing.T) {
	body := bytes.Repeat([]byte{0xAB, 0x00, 0xFF}, 5000)
	var b wireBuilder
	b.open(rootB)
	b.header(TypeAttachment, "bin", digestOf(body), "")
	b.body(body)
	b.close(rootB)

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB, Window: 128})
	if err != nil {
		t.Fatal(err)
	}
	var failErr error
	chunks := splitEvery(b.bytes(), 64)
	for i, ch := range chunks {
		if i == 3 {
			if s, ok := host.txn.sinks["bin"]; ok {
				s.failN = 1
			}
		}
		if _, werr := p.Write(ch); werr != nil {
			failErr = werr
			break
		}
	}
	if failErr == nil {
		_, failErr = p.CloseCtx(context.Background())
	}
	if failErr == nil {
		t.Fatal("expected sink failure to surface")
	}
	if !host.txn.rolled {
		t.Fatal("sink failure must roll back entire txn")
	}
	if _, ok := host.txn.visible("bin"); ok {
		t.Fatal("staged bytes must not be readable after write failure")
	}
}

func TestArbitraryBinaryBodyRoundTrip(t *testing.T) {
	body := make([]byte, 4096)
	for i := range body {
		body[i] = byte(i * 37)
	}
	var b wireBuilder
	b.open(rootB)
	b.header(TypeAttachment, "bin", digestOf(body), "")
	b.body(body)
	b.close(rootB)

	for _, cut := range []int{1, 7, 256, 4096, 10000} {
		_, host, _, err := runStream(t, splitEvery(b.bytes(), cut), Options{Window: 512})
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		got, ok := host.txn.visible("bin")
		if !ok || !bytes.Equal(got, body) {
			t.Fatalf("cut=%d binary body mismatch", cut)
		}
	}
}
