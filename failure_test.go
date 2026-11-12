package netsec

import (
	"context"
	"errors"
	"testing"
)

// TestFailedUploadsGrantNothing covers mid-stream failures and digest
// mismatches: no tenant index entry, no physical write, no cross-tenant
// oracle, and a clean retry afterwards.
func TestFailedUploadsGrantNothing(t *testing.T) {
	store := NewMemoryStore()
	controller := NewAdmissionController(store)
	ctx := context.Background()

	payload := makePayload(2048, 3)
	digest := SHA256Hex(payload)

	t.Run("stream interrupted", func(t *testing.T) {
		failAt := len(payload) / 2
		uploader := newCountingReader(payload, 37, failAt)
		result, err := controller.Upload(ctx, "tenant-a", digest, uploader)
		if !errors.Is(err, errUploadBroken) {
			t.Fatalf("err = %v, want errUploadBroken", err)
		}
		if result.Received != int64(failAt) {
			t.Fatalf("Received = %d, want %d bytes before failure", result.Received, failAt)
		}
		if controller.Exists("tenant-a", digest) {
			t.Fatal("failed upload must not create a tenant index entry")
		}
		if store.PhysicalWrites() != 0 || store.PhysicalBlocks() != 0 {
			t.Fatalf("failed upload must not touch physical storage: writes=%d blocks=%d",
				store.PhysicalWrites(), store.PhysicalBlocks())
		}

		// Retrying with an intact stream succeeds and still uploads fully.
		retry := newCountingReader(payload, 37, 0)
		retryResult, err := controller.Upload(ctx, "tenant-a", digest, retry)
		if err != nil {
			t.Fatalf("retry upload failed: %v", err)
		}
		if retry.bytesRead != len(payload) || retryResult.Received != int64(len(payload)) {
			t.Fatalf("retry must upload all %d bytes: reader=%d result=%d",
				len(payload), retry.bytesRead, retryResult.Received)
		}
		if !controller.Exists("tenant-a", digest) {
			t.Fatal("tenant-a should hold a verified reference after retry")
		}
	})

	t.Run("digest mismatch", func(t *testing.T) {
		other := makePayload(2048, 9)
		wrongDigest := SHA256Hex(other)

		uploader := newCountingReader(payload, 53, 0)
		result, err := controller.Upload(ctx, "tenant-b", wrongDigest, uploader)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
		// The complete body was still consumed: verification needs all bytes.
		if uploader.bytesRead != len(payload) || result.Received != int64(len(payload)) {
			t.Fatalf("mismatched upload must still consume all %d bytes: reader=%d result=%d",
				len(payload), uploader.bytesRead, result.Received)
		}

		if controller.Exists("tenant-b", wrongDigest) || controller.Exists("tenant-b", digest) {
			t.Fatal("digest mismatch must not create any verified entry")
		}
		// One write comes from tenant-a's successful retry in the previous
		// subtest; this subtest itself must add nothing.
		if store.PhysicalWrites() != 1 || store.PhysicalBlocks() != 1 {
			t.Fatalf("unexpected store state: writes=%d blocks=%d",
				store.PhysicalWrites(), store.PhysicalBlocks())
		}

		// A third tenant cannot infer tenant-a's earlier successful upload.
		if controller.Exists("tenant-c", digest) {
			t.Fatal("tenant-c must not infer tenant-a's upload through Exists")
		}

		okUploader := newCountingReader(payload, 53, 0)
		if _, err := controller.Upload(ctx, "tenant-b", digest, okUploader); err != nil {
			t.Fatalf("correct upload failed: %v", err)
		}
		if okUploader.bytesRead != len(payload) {
			t.Fatalf("correct upload must still receive all %d bytes", len(payload))
		}
		if !controller.Exists("tenant-b", digest) {
			t.Fatal("tenant-b should now hold a verified reference")
		}
		// Shared block, second tenant reference, still only one physical write.
		if store.PhysicalBlocks() != 1 || store.PhysicalWrites() != 1 {
			t.Fatalf("deduplicated storage expected: blocks=%d writes=%d",
				store.PhysicalBlocks(), store.PhysicalWrites())
		}
	})

	t.Run("invalid inputs", func(t *testing.T) {
		uploader := newCountingReader(payload, 16, 0)
		if _, err := controller.Upload(ctx, "", digest, uploader); !errors.Is(err, ErrEmptyTenant) {
			t.Fatalf("empty tenant err = %v, want ErrEmptyTenant", err)
		}
		uploader = newCountingReader(payload, 16, 0)
		if _, err := controller.Upload(ctx, "tenant-d", "deadbeef", uploader); !errors.Is(err, ErrInvalidDigest) {
			t.Fatalf("bad digest err = %v, want ErrInvalidDigest", err)
		}
		if uploader.bytesRead != 0 {
			t.Fatalf("invalid digest must be rejected before reading body, read %d", uploader.bytesRead)
		}
		if _, err := controller.Open(ctx, "tenant-a", digest); err != nil {
			t.Fatalf("tenant-a verified read should still work: %v", err)
		}
		if err := controller.Delete(ctx, "tenant-z", digest); !errors.Is(err, ErrNotFound) {
			t.Fatalf("delete unknown tenant err = %v, want ErrNotFound", err)
		}
	})
}
