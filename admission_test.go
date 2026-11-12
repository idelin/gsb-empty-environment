package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

var errUploadBroken = errors.New("netsec-test: connection reset by peer")

// countingReader is the instrumented uploader: it serves a payload in small
// chunks and counts every byte the host actually pulls off the wire. When
// failAfter is positive, the stream aborts with errUploadBroken once that
// many bytes have been delivered and more data remains.
type countingReader struct {
	data      []byte
	pos       int
	chunkSize int
	bytesRead int
	failAfter int
	failed    bool
}

func newCountingReader(data []byte, chunkSize, failAfter int) *countingReader {
	if chunkSize <= 0 {
		chunkSize = len(data)
		if chunkSize == 0 {
			chunkSize = 1
		}
	}
	return &countingReader{data: data, chunkSize: chunkSize, failAfter: failAfter}
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	limit := r.chunkSize
	if remaining := len(r.data) - r.pos; limit > remaining {
		limit = remaining
	}
	if limit > len(p) {
		limit = len(p)
	}
	// Never overshoot the failure budget: the error surfaces on the next
	// read attempt, after exactly failAfter bytes have been delivered.
	if r.failAfter > 0 {
		if r.bytesRead >= r.failAfter {
			r.failed = true
			return 0, errUploadBroken
		}
		if budget := r.failAfter - r.bytesRead; limit > budget {
			limit = budget
		}
	}
	n := copy(p, r.data[r.pos:r.pos+limit])
	r.pos += n
	r.bytesRead += n
	return n, nil
}

func makePayload(size int, seed byte) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i%251) ^ seed
	}
	return payload
}

func mustReference(t *testing.T, c *AdmissionController, tenant string, d Digest) Reference {
	t.Helper()
	c.mu.RLock()
	defer c.mu.RUnlock()
	ref, ok := c.tenants[tenant][d]
	if !ok {
		t.Fatalf("tenant %s has no verified reference for %s", tenant, d)
	}
	return ref
}

// TestCrossTenantDeduplication verifies the core property: identical content
// from two tenants shares one physical block, each tenant still uploads every
// byte, and neither tenant's existence probe can see the other.
func TestCrossTenantDeduplication(t *testing.T) {
	store := NewMemoryStore()
	controller := NewAdmissionController(store)
	ctx := context.Background()

	payload := makePayload(4096, 7)
	digest := SHA256Hex(payload)

	if controller.Exists("tenant-a", digest) || controller.Exists("tenant-b", digest) {
		t.Fatal("no tenant should see the block before any verified upload")
	}

	uploaderA := newCountingReader(payload, 64, 0)
	resultA, err := controller.Upload(ctx, "tenant-a", digest, uploaderA)
	if err != nil {
		t.Fatalf("tenant-a upload failed: %v", err)
	}

	// After tenant A completes, tenant B must still get a negative answer:
	// the physical block exists but B has verified nothing itself.
	if !controller.Exists("tenant-a", digest) {
		t.Fatal("tenant-a should see its own verified block")
	}
	if controller.Exists("tenant-b", digest) {
		t.Fatal("tenant-b must not learn about tenant-a's block through Exists")
	}

	uploaderB := newCountingReader(payload, 64, 0)
	resultB, err := controller.Upload(ctx, "tenant-b", digest, uploaderB)
	if err != nil {
		t.Fatalf("tenant-b upload failed: %v", err)
	}

	if int64(uploaderA.bytesRead) != int64(len(payload)) ||
		int64(uploaderB.bytesRead) != int64(len(payload)) {
		t.Fatalf("each tenant must upload all %d bytes: a=%d b=%d",
			len(payload), uploaderA.bytesRead, uploaderB.bytesRead)
	}
	if resultA.Received != int64(len(payload)) || resultB.Received != int64(len(payload)) {
		t.Fatalf("Received counts must equal payload size: %d and %d", resultA.Received, resultB.Received)
	}

	// Physical sharing: one block, one write, two references.
	if store.PhysicalBlocks() != 1 {
		t.Fatalf("physical blocks = %d, want 1", store.PhysicalBlocks())
	}
	if store.PhysicalWrites() != 1 {
		t.Fatalf("physical writes = %d, want 1", store.PhysicalWrites())
	}
	if resultA.Reference != resultB.Reference {
		t.Fatalf("references should point at the shared block: %q vs %q",
			resultA.Reference, resultB.Reference)
	}
	if got := store.ReferenceCount(resultA.Reference); got != 2 {
		t.Fatalf("shared block refcount = %d, want 2", got)
	}

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		rc, err := controller.Open(ctx, tenant, digest)
		if err != nil {
			t.Fatalf("%s open failed: %v", tenant, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s read mismatch: %v", tenant, err)
		}
	}

	// Tenant A deleting its reference must not break tenant B.
	if err := controller.Delete(ctx, "tenant-a", digest); err != nil {
		t.Fatalf("tenant-a delete failed: %v", err)
	}
	if controller.Exists("tenant-a", digest) {
		t.Fatal("tenant-a entry should be gone after delete")
	}
	if !controller.Exists("tenant-b", digest) {
		t.Fatal("tenant-b must retain its verified reference")
	}
	if store.PhysicalBlocks() != 1 {
		t.Fatalf("shared block should survive tenant-a delete: %d", store.PhysicalBlocks())
	}
	if _, err := controller.Open(ctx, "tenant-a", digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant-a open after delete = %v, want ErrNotFound", err)
	}

	if err := controller.Delete(ctx, "tenant-b", digest); err != nil {
		t.Fatalf("tenant-b delete failed: %v", err)
	}
	if store.PhysicalBlocks() != 0 {
		t.Fatalf("block should be reclaimed after last release: %d", store.PhysicalBlocks())
	}

	// Probes with malformed digests or empty tenants are non-oracles.
	if controller.Exists("tenant-a", "not-a-digest") || controller.Exists("", digest) {
		t.Fatal("invalid probes must report false")
	}
}

// TestEmptyObjectUpload covers the zero-byte object edge case: each tenant
// still completes its own admission and only sees its own verified entry.
func TestEmptyObjectUpload(t *testing.T) {
	store := NewMemoryStore()
	controller := NewAdmissionController(store)
	ctx := context.Background()

	var empty [0]byte
	digest := SHA256Hex(empty[:])

	first, err := controller.Upload(ctx, "tenant-a", digest, newCountingReader(empty[:], 1, 0))
	if err != nil {
		t.Fatalf("empty upload tenant-a failed: %v", err)
	}
	if first.Received != 0 || first.Reference == "" {
		t.Fatalf("unexpected empty-object result: %+v", first)
	}
	if controller.Exists("tenant-b", digest) {
		t.Fatal("tenant-b must not infer tenant-a's empty object")
	}

	second, err := controller.Upload(ctx, "tenant-b", digest, newCountingReader(empty[:], 1, 0))
	if err != nil {
		t.Fatalf("empty upload tenant-b failed: %v", err)
	}
	if second.Reference != first.Reference {
		t.Fatal("empty objects with equal digests should share the physical block")
	}
	if !controller.Exists("tenant-a", digest) || !controller.Exists("tenant-b", digest) {
		t.Fatal("both tenants should hold verified entries for the empty object")
	}
	if store.PhysicalBlocks() != 1 || store.PhysicalWrites() != 1 {
		t.Fatalf("empty object stored once: blocks=%d writes=%d",
			store.PhysicalBlocks(), store.PhysicalWrites())
	}
}
