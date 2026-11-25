package nestedmp

import (
	"bytes"
	"context"
	"testing"
)

// TestContainerWithMultipleChildParts 验证同一容器内连续多个子部件，
// 在逐字节喂入时容器摘要仍精确绑定完整子报文，且各附件独立暂存。
func TestContainerWithMultipleChildParts(t *testing.T) {
	a := []byte("aaa")
	b := []byte("bbb")
	var inner wireBuilder
	inner.open(innerB)
	inner.header(TypeAttachment, "a", digestOf(a), "")
	inner.body(a)
	inner.open(innerB)
	inner.header(TypeAttachment, "b", digestOf(b), "")
	inner.body(b)
	inner.close(innerB)

	var root wireBuilder
	root.open(rootB)
	root.header(TypeContainer, "box", digestOf(inner.bytes()), innerB)
	root.body(inner.bytes())
	root.close(rootB)

	host := &fakeHost{}
	p, err := New(context.Background(), Options{Host: host, RootBoundary: rootB})
	if err != nil {
		t.Fatal(err)
	}
	for i, ch := range root.bytes() {
		if _, err := p.Write([]byte{ch}); err != nil {
			t.Fatalf("byte %d (%q): %v", i, string(ch), err)
		}
	}
	res, err := p.CloseCtx(context.Background())
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(res.Parts) != 3 {
		t.Fatalf("want 3 parts (box,a,b), got %d", len(res.Parts))
	}
	if got, ok := host.txn.visible("a"); !ok || !bytes.Equal(got, a) {
		t.Fatalf("part a staged wrong: %q", got)
	}
	if got, ok := host.txn.visible("b"); !ok || !bytes.Equal(got, b) {
		t.Fatalf("part b staged wrong: %q", got)
	}
	ref, err := ReferenceParse(root.bytes(), rootB)
	if err != nil {
		t.Fatal(err)
	}
	for _, rp := range flattenRef(ref) {
		if rp.Name == "box" && rp.Digest != res.Parts[0].Digest {
			t.Fatal("container digest diverges from reference")
		}
	}
}
