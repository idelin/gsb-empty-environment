package metatx_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"example.org/netsec/q0326/metatx"
)

// memStore is an in-memory Store that keeps every published version so
// tests can verify that a failed transaction leaves the old version intact.
type memStore struct {
	versions map[string]memVersion
	staged   map[int][]byte
	next     int
}

type memVersion struct {
	content []byte
	attrs   []byte
}

func newMemStore() *memStore {
	return &memStore{versions: map[string]memVersion{}, staged: map[int][]byte{}}
}

func (m *memStore) Stage(r io.Reader) (metatx.Staged, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	m.next++
	m.staged[m.next] = b
	return m.next, nil
}

func (m *memStore) Commit(name string, staged metatx.Staged, attrs []byte) error {
	id := staged.(int)
	m.versions[name] = memVersion{content: m.staged[id], attrs: attrs}
	delete(m.staged, id)
	return nil
}

func (m *memStore) Abort(staged metatx.Staged) error {
	delete(m.staged, staged.(int))
	return nil
}

// staticContent returns the same bytes on every Open.
type staticContent []byte

func (c staticContent) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c)), nil
}

// mutatingContent returns different bytes on each Open, simulating a source
// object that is modified while the transaction is being prepared.
type mutatingContent struct {
	versions [][]byte
	calls    int
}

func (c *mutatingContent) Open() (io.ReadCloser, error) {
	i := c.calls
	if i >= len(c.versions) {
		i = len(c.versions) - 1
	}
	c.calls++
	return io.NopCloser(bytes.NewReader(c.versions[i])), nil
}

// failAfterSigner signs successfully n-1 times and then fails, simulating a
// failure on the last attribute of the transaction.
type failAfterSigner struct {
	inner metatx.Signer
	fail  error
	calls int
	max   int
}

func (s *failAfterSigner) Sign(digest []byte, ns, key string, value []byte) ([]byte, error) {
	s.calls++
	if s.calls > s.max {
		return nil, s.fail
	}
	return s.inner.Sign(digest, ns, key, value)
}

var testPolicy = metatx.Policy{
	"meta":  metatx.Preserve,
	"trust": metatx.Resign,
	// "user.exec" intentionally absent: defaults to Drop.
}

var testAttrs = []metatx.Attribute{
	{Namespace: "meta", Key: "author", Value: []byte("alice")},
	{Namespace: "trust", Key: "signature", Value: []byte("old-sig")},
	{Namespace: "user.exec", Key: "mode", Value: []byte("0755")},
}

func seedOldVersion(t *testing.T, p *metatx.Publisher, store *memStore, name string) memVersion {
	t.Helper()
	oldAttrs := []metatx.Attribute{{Namespace: "meta", Key: "author", Value: []byte("bob")}}
	if _, err := p.Publish(name, staticContent("old content"), oldAttrs, testPolicy); err != nil {
		t.Fatalf("seed old version: %v", err)
	}
	return store.versions[name]
}

func requireOldVersionIntact(t *testing.T, store *memStore, name string, old memVersion) {
	t.Helper()
	got, ok := store.versions[name]
	if !ok {
		t.Fatalf("old version of %q is gone", name)
	}
	if !bytes.Equal(got.content, old.content) {
		t.Errorf("old content changed: got %q want %q", got.content, old.content)
	}
	if !bytes.Equal(got.attrs, old.attrs) {
		t.Errorf("old attributes changed: got %x want %x", got.attrs, old.attrs)
	}
	if len(store.staged) != 0 {
		t.Errorf("staged content leaked: %d entries", len(store.staged))
	}
}

func TestPublishAppliesPolicy(t *testing.T) {
	store := newMemStore()
	signer := metatx.HMACSigner{Key: []byte("secret")}
	p := &metatx.Publisher{Store: store, Signer: signer}

	digest, err := p.Publish("pkg", staticContent("new content"), testAttrs, testPolicy)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	v := store.versions["pkg"]
	if string(v.content) != "new content" {
		t.Errorf("content = %q, want %q", v.content, "new content")
	}

	expectedSig, err := signer.Sign(digest, "trust", "signature", []byte("old-sig"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	want, err := metatx.EncodeAttributes([]metatx.Attribute{
		{Namespace: "meta", Key: "author", Value: []byte("alice")}, // preserved
		{Namespace: "trust", Key: "signature", Value: expectedSig}, // re-signed, bound to digest
		// user.exec/mode dropped: not inherited from the upload area.
	})
	if err != nil {
		t.Fatalf("EncodeAttributes: %v", err)
	}
	if !bytes.Equal(v.attrs, want) {
		t.Errorf("committed attributes mismatch:\n got %x\nwant %x", v.attrs, want)
	}
	if len(store.staged) != 0 {
		t.Errorf("staged content leaked: %d entries", len(store.staged))
	}
}

func TestResignBindsFinalDigest(t *testing.T) {
	store := newMemStore()
	signer := metatx.HMACSigner{Key: []byte("secret")}
	p := &metatx.Publisher{Store: store, Signer: signer}
	attrs := []metatx.Attribute{{Namespace: "trust", Key: "signature", Value: []byte("v1")}}

	d1, err := p.Publish("a", staticContent("content A"), attrs, testPolicy)
	if err != nil {
		t.Fatalf("Publish a: %v", err)
	}
	d2, err := p.Publish("b", staticContent("content B"), attrs, testPolicy)
	if err != nil {
		t.Fatalf("Publish b: %v", err)
	}
	if bytes.Equal(store.versions["a"].attrs, store.versions["b"].attrs) {
		t.Error("identical signatures for different content digests")
	}
	if bytes.Equal(d1, d2) {
		t.Fatal("test setup error: digests should differ")
	}
}

func TestLastAttributeFailurePreservesOldVersion(t *testing.T) {
	store := newMemStore()
	signer := metatx.HMACSigner{Key: []byte("secret")}
	p := &metatx.Publisher{Store: store, Signer: signer}
	old := seedOldVersion(t, p, store, "pkg")

	// Two attributes in the resigned namespace; the signer fails on the
	// last one.
	signErr := errors.New("kms unavailable")
	p.Signer = &failAfterSigner{inner: signer, fail: signErr, max: 1}
	attrs := []metatx.Attribute{
		{Namespace: "trust", Key: "first", Value: []byte("1")},
		{Namespace: "trust", Key: "last", Value: []byte("2")},
	}

	_, err := p.Publish("pkg", staticContent("new content"), attrs, testPolicy)
	if !errors.Is(err, signErr) {
		t.Fatalf("Publish error = %v, want wrapped %v", err, signErr)
	}
	requireOldVersionIntact(t, store, "pkg", old)
}

func TestDuplicateAttributeEncodingPreservesOldVersion(t *testing.T) {
	store := newMemStore()
	p := &metatx.Publisher{Store: store, Signer: metatx.HMACSigner{Key: []byte("secret")}}
	old := seedOldVersion(t, p, store, "pkg")

	attrs := []metatx.Attribute{
		{Namespace: "meta", Key: "author", Value: []byte("alice")},
		{Namespace: "meta", Key: "author", Value: []byte("mallory")},
	}
	_, err := p.Publish("pkg", staticContent("new content"), attrs, testPolicy)
	if !errors.Is(err, metatx.ErrDuplicateAttribute) {
		t.Fatalf("Publish error = %v, want %v", err, metatx.ErrDuplicateAttribute)
	}
	requireOldVersionIntact(t, store, "pkg", old)
}

func TestContentChangedMidwayPreservesOldVersion(t *testing.T) {
	store := newMemStore()
	p := &metatx.Publisher{Store: store, Signer: metatx.HMACSigner{Key: []byte("secret")}}
	old := seedOldVersion(t, p, store, "pkg")

	src := &mutatingContent{versions: [][]byte{[]byte("content v1"), []byte("content v2")}}
	_, err := p.Publish("pkg", src, testAttrs, testPolicy)
	if !errors.Is(err, metatx.ErrContentChanged) {
		t.Fatalf("Publish error = %v, want %v", err, metatx.ErrContentChanged)
	}
	requireOldVersionIntact(t, store, "pkg", old)
}

func TestEncodeAttributesDeterministic(t *testing.T) {
	a := []metatx.Attribute{
		{Namespace: "b", Key: "k2", Value: []byte("2")},
		{Namespace: "a", Key: "k1", Value: []byte("1")},
	}
	e1, err := metatx.EncodeAttributes(a)
	if err != nil {
		t.Fatalf("EncodeAttributes: %v", err)
	}
	e2, err := metatx.EncodeAttributes([]metatx.Attribute{a[1], a[0]})
	if err != nil {
		t.Fatalf("EncodeAttributes: %v", err)
	}
	if !bytes.Equal(e1, e2) {
		t.Error("encoding is not order-independent")
	}
	if !strings.HasPrefix(string(e1), "\x00\x00\x00\x02") {
		t.Error("encoding missing entry-count prefix")
	}
}
