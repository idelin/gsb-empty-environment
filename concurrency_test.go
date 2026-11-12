package netsec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
)

// TestConcurrentConfirmations hammers the controller with parallel uploads of
// the same content from many goroutines and tenants. Every goroutine must
// stream the full payload before getting a reference, and the final state
// must be exactly one shared physical block with one verified entry per
// tenant.
func TestConcurrentConfirmations(t *testing.T) {
	store := NewMemoryStore()
	controller := NewAdmissionController(store)
	ctx := context.Background()

	payload := makePayload(8192, 11)
	digest := SHA256Hex(payload)

	const tenants = 8
	const perTenant = 4

	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	totalUploaded := int64(0)
	var byteMu sync.Mutex

	recordErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	for tIdx := 0; tIdx < tenants; tIdx++ {
		tenant := fmt.Sprintf("tenant-%d", tIdx)
		for uIdx := 0; uIdx < perTenant; uIdx++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				uploader := newCountingReader(payload, 31, 0)
				result, err := controller.Upload(ctx, tenant, digest, uploader)
				if err != nil {
					recordErr(err)
					return
				}
				if uploader.bytesRead != len(payload) {
					recordErr(fmt.Errorf("short upload: read=%d want=%d", uploader.bytesRead, len(payload)))
					return
				}
				if result.Received != int64(len(payload)) || result.Reference == "" {
					recordErr(fmt.Errorf("bad upload result: %+v", result))
					return
				}
				byteMu.Lock()
				totalUploaded += int64(uploader.bytesRead)
				byteMu.Unlock()
			}()
		}
	}
	wg.Wait()

	if firstErr != nil {
		t.Fatalf("concurrent upload error: %v", firstErr)
	}

	// Physical reuse never reduces tenant bytes: all concurrent callers
	// independently pushed the complete object over the wire.
	wantBytes := int64(tenants * perTenant * len(payload))
	if totalUploaded != wantBytes {
		t.Fatalf("total tenant bytes = %d, want %d", totalUploaded, wantBytes)
	}

	// Exactly one physical block, written once, one verified entry per tenant.
	if store.PhysicalBlocks() != 1 {
		t.Fatalf("physical blocks = %d, want 1", store.PhysicalBlocks())
	}
	if store.PhysicalWrites() != 1 {
		t.Fatalf("physical writes = %d, want 1", store.PhysicalWrites())
	}

	var sharedRef Reference
	for tIdx := 0; tIdx < tenants; tIdx++ {
		tenant := fmt.Sprintf("tenant-%d", tIdx)
		if !controller.Exists(tenant, digest) {
			t.Fatalf("%s missing verified entry after concurrent uploads", tenant)
		}
		rc, err := controller.Open(ctx, tenant, digest)
		if err != nil {
			t.Fatalf("%s open failed: %v", tenant, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s content mismatch: %v", tenant, err)
		}
		ref := mustReference(t, controller, tenant, digest)
		if sharedRef == "" {
			sharedRef = ref
		} else if ref != sharedRef {
			t.Fatalf("%s holds %q, expected shared ref %q", tenant, ref, sharedRef)
		}
	}

	// One reference per distinct tenant despite perTenant uploads each;
	// duplicate commits release the extra store reference.
	if got := store.ReferenceCount(sharedRef); got != tenants {
		t.Fatalf("shared block refcount = %d, want %d", got, tenants)
	}

	// An outsider tenant that never uploaded still sees nothing.
	if controller.Exists("outsider", digest) {
		t.Fatal("outsider must not observe the block through Exists")
	}
}

// TestConcurrentExistenceVisibility interleaves an in-flight upload from
// tenant A with existence probes from tenant B: B's answer must stay negative
// until B itself completes a verified upload.
func TestConcurrentExistenceVisibility(t *testing.T) {
	store := NewMemoryStore()
	controller := NewAdmissionController(store)
	ctx := context.Background()

	payload := makePayload(16384, 5)
	digest := SHA256Hex(payload)

	started := make(chan struct{})
	proceed := make(chan struct{})

	// Blocking reader releases the goroutine only after probes have run,
	// keeping tenant A's upload deterministically in flight.
	blocking := &gateReader{data: payload, started: started, proceed: proceed}

	done := make(chan error, 1)
	go func() {
		_, err := controller.Upload(ctx, "tenant-a", digest, blocking)
		done <- err
	}()

	<-started
	for i := 0; i < 16; i++ {
		if controller.Exists("tenant-b", digest) {
			close(proceed)
			t.Fatal("tenant-b observed tenant-a's in-flight upload")
		}
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("tenant-a upload failed: %v", err)
	}

	if !controller.Exists("tenant-a", digest) {
		t.Fatal("tenant-a should see its verified entry")
	}
	if controller.Exists("tenant-b", digest) {
		t.Fatal("tenant-b must still see nothing until its own upload")
	}

	uploaderB := newCountingReader(payload, 128, 0)
	if _, err := controller.Upload(ctx, "tenant-b", digest, uploaderB); err != nil {
		t.Fatalf("tenant-b upload failed: %v", err)
	}
	if uploaderB.bytesRead != len(payload) {
		t.Fatalf("tenant-b must receive full %d bytes despite physical sharing", len(payload))
	}
	if !controller.Exists("tenant-b", digest) {
		t.Fatal("tenant-b should see its own verified entry after uploading")
	}
	if store.PhysicalWrites() != 1 || store.PhysicalBlocks() != 1 {
		t.Fatalf("expected one shared physical block: writes=%d blocks=%d",
			store.PhysicalWrites(), store.PhysicalBlocks())
	}
}

type gateReader struct {
	data    []byte
	pos     int
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (g *gateReader) Read(p []byte) (int, error) {
	g.once.Do(func() { close(g.started) })
	if g.pos == 0 {
		<-g.proceed
	}
	if g.pos >= len(g.data) {
		return 0, io.EOF
	}
	n := copy(p, g.data[g.pos:])
	g.pos += n
	return n, nil
}
