package admission

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---- CAS / ByteStream admission -------------------------------------------

func TestCASWriteAcceptsContentWithMatchingDigest(t *testing.T) {
	guard, store, log := newTestGuard(t)
	data := []byte("trusted build artifact bytes")
	d := shaDigest(t, data)

	got, err := guard.AdmitCASWrite(prCtx(), blobResource(d), data)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if got != d || !store.HasBlob(d) {
		t.Fatalf("blob not stored under claimed digest %+v", got)
	}
	if len(log.events) != 0 {
		t.Fatalf("successful CAS write must not emit deny events, got %+v", log.events)
	}
}

func TestCASWriteRejectsForgedDigest(t *testing.T) {
	guard, store, log := newTestGuard(t)
	data := []byte("backdoor binary payload")

	// Attacker claims a digest that does not match the bytes' real sha256.
	forged := fakeDigest(shaDigest(t, []byte("totally different content")).Hash, int64(len(data)))
	_, err := guard.AdmitCASWrite(prCtx(), blobResource(forged), data)
	assertErrorIs(t, err, ErrDigestMismatch)
	if store.HasBlob(forged) {
		t.Fatal("forged blob must never reach the store")
	}
	if ev := log.last(); ev.Decision != "deny" || ev.Reason != "digest_mismatch" {
		t.Fatalf("expected logged digest_mismatch denial, got %+v", ev)
	}
}

func TestCASWriteRejectsSizeMismatch(t *testing.T) {
	guard, store, _ := newTestGuard(t)
	data := []byte("eleven bytes")
	real := shaDigest(t, data)
	wrongSize := Digest{Hash: real.Hash, Size: int64(len(data) + 99)}

	_, err := guard.AdmitCASWrite(prCtx(), blobResource(wrongSize), data)
	assertErrorIs(t, err, ErrSizeMismatch)
	if store.HasBlob(wrongSize) {
		t.Fatal("size-mismatched blob must never reach the store")
	}
}

func TestCASWriteRejectsMalformedResourceName(t *testing.T) {
	guard, _, log := newTestGuard(t)
	_, err := guard.AdmitCASWrite(prCtx(), "inst/blobs/ABCDEF/3", []byte("abc"))
	assertErrorCode(t, err, CodeInvalidArgument)
	if ev := log.last(); ev.Decision != "deny" {
		t.Fatalf("malformed resource must be logged as deny, got %+v", ev)
	}
}

func TestByteStreamWriterVerifiesOnCommit(t *testing.T) {
	guard, store, log := newTestGuard(t)
	data := []byte("streamed object content")
	real := shaDigest(t, data)

	t.Run("valid streaming upload commits", func(t *testing.T) {
		w, err := guard.NewBlobWriter(prCtx(), uploadResource(real))
		if err != nil {
			t.Fatalf("new writer: %v", err)
		}
		if _, err := w.Write(data[:5]); err != nil {
			t.Fatalf("write chunk 1: %v", err)
		}
		if _, err := w.Write(data[5:]); err != nil {
			t.Fatalf("write chunk 2: %v", err)
		}
		committed, err := w.Commit()
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		if committed != real || !store.HasBlob(real) {
			t.Fatal("streamed blob not stored under its digest")
		}
	})

	t.Run("forged hash rejected at commit", func(t *testing.T) {
		forged := Digest{Hash: strings.Repeat("a", 64), Size: int64(len(data))}
		w, err := guard.NewBlobWriter(prCtx(), uploadResource(forged))
		if err != nil {
			t.Fatalf("new writer: %v", err)
		}
		_, _ = w.Write(data)
		_, err = w.Commit()
		assertErrorIs(t, err, ErrDigestMismatch)
		if store.HasBlob(forged) {
			t.Fatal("forged streamed blob must not be stored")
		}
		if ev := log.last(); ev.Reason != "digest_mismatch" {
			t.Fatalf("expected digest_mismatch log, got %+v", ev)
		}
	})

	t.Run("oversized upload rejected mid-stream", func(t *testing.T) {
		small := shaDigest(t, []byte("ab"))
		w, err := guard.NewBlobWriter(prCtx(), uploadResource(small))
		if err != nil {
			t.Fatalf("new writer: %v", err)
		}
		if _, err := w.Write([]byte("abc")); err == nil {
			t.Fatal("expected size-mismatch error when stream exceeds declared size")
		}
	})

	t.Run("non-upload resource name rejected", func(t *testing.T) {
		d := shaDigest(t, []byte("ab"))
		if _, err := guard.NewBlobWriter(prCtx(), blobResource(d)); err == nil {
			t.Fatal("ByteStream writer must require an uploads/ resource name")
		}
	})
}

// ---- ActionCache admission ------------------------------------------------

func validResult(t *testing.T, store Store) (*ActionResult, Digest) {
	t.Helper()
	out := seedBlob(t, store, []byte("output file"))
	tree := seedBlob(t, store, []byte("directory tree"))
	stdout := seedBlob(t, store, []byte("stdout log"))
	return &ActionResult{
		OutputFiles:       []OutputFile{{Path: "bin/app", Digest: out}},
		OutputDirectories: []OutputDirectory{{Path: "gen", TreeDigest: tree}},
		StdoutDigest:      &stdout,
		ExitCode:          0,
	}, out
}

func TestActionResultDeniesPRWriter(t *testing.T) {
	guard, store, log := newTestGuard(t)
	result, _ := validResult(t, store)
	action := shaDigest(t, []byte("action spec"))

	err := guard.AdmitActionResultWrite(prCtx(), action, result)
	assertErrorIs(t, err, ErrPermissionDenied)

	got, lookupErr := store.GetActionResult(prCtx(), action)
	if lookupErr != nil || got != nil {
		t.Fatalf("PR write must not persist anything, got %+v err=%v", got, lookupErr)
	}
	ev := log.last()
	if ev.Decision != "deny" || ev.Code != string(CodePermissionDenied) ||
		ev.Writer != prIdentity.Name || ev.Source != prIdentity.Source {
		t.Fatalf("expected permission_denied audit event for PR, got %+v", ev)
	}
}

func TestActionResultDeniesAnonymousWriter(t *testing.T) {
	guard, store, _ := newTestGuard(t)
	result, _ := validResult(t, store)
	action := shaDigest(t, []byte("action spec"))

	// No identity in context: fail closed as untrusted.
	err := guard.AdmitActionResultWrite(context.Background(), action, result)
	assertErrorCode(t, err, CodePermissionDenied)
}

func TestActionResultRejectsMissingBlob(t *testing.T) {
	guard, store, log := newTestGuard(t)
	present := seedBlob(t, store, []byte("present output"))
	missing := shaDigest(t, []byte("never uploaded"))
	result := &ActionResult{
		OutputFiles: []OutputFile{
			{Path: "ok", Digest: present},
			{Path: "ghost", Digest: missing},
		},
	}
	action := shaDigest(t, []byte("action spec"))

	err := guard.AdmitActionResultWrite(trustedCtx(), action, result)
	assertErrorIs(t, err, ErrMissingBlob)
	if ev := log.last(); ev.Reason != "missing_blob" || !strings.Contains(ev.Message, missing.Hash) {
		t.Fatalf("expected missing_blob event naming %s, got %+v", missing.Hash, ev)
	}
	if got, _ := store.GetActionResult(trustedCtx(), action); got != nil {
		t.Fatal("result with missing output must not be persisted")
	}
}

func TestActionResultRejectsStoredSizeMismatch(t *testing.T) {
	guard, store, _ := newTestGuard(t)
	data := []byte("blob whose stored size disagrees")
	real := shaDigest(t, data)
	if err := store.PutBlob(context.Background(), real, data); err != nil {
		t.Fatal(err)
	}
	// Reference claims a different size than what CAS actually holds.
	lie := Digest{Hash: real.Hash, Size: real.Size + 10}
	result := &ActionResult{OutputFiles: []OutputFile{{Path: "x", Digest: lie}}}
	action := shaDigest(t, []byte("action spec"))

	err := guard.AdmitActionResultWrite(trustedCtx(), action, result)
	assertErrorIs(t, err, ErrMissingBlob)
}

func TestActionResultAcceptsTrustedWriterWhenOutputsExist(t *testing.T) {
	guard, store, log := newTestGuard(t)
	result, _ := validResult(t, store)
	action := shaDigest(t, []byte("action spec"))

	if err := guard.AdmitActionResultWrite(trustedCtx(), action, result); err != nil {
		t.Fatalf("trusted write: %v", err)
	}
	got, err := store.GetActionResult(trustedCtx(), action)
	if err != nil || got == nil || !got.Equal(result) {
		t.Fatalf("result not persisted correctly: %+v err=%v", got, err)
	}
	if ev := log.last(); ev.Decision != "allow" || ev.Trusted != true {
		t.Fatalf("expected allow audit event, got %+v", ev)
	}
}

func TestActionResultRejectsOverwriteConflict(t *testing.T) {
	guard, store, log := newTestGuard(t)
	first, firstOut := validResult(t, store)
	action := shaDigest(t, []byte("stable action digest"))

	if err := guard.AdmitActionResultWrite(trustedCtx(), action, first); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	// Same action key, different output digest: the poisoning overwrite.
	otherBlob := seedBlob(t, store, []byte("different output content"))
	poisoned := &ActionResult{
		OutputFiles: []OutputFile{{Path: "bin/app", Digest: otherBlob}},
		ExitCode:    0,
	}
	err := guard.AdmitActionResultWrite(trustedCtx(), action, poisoned)
	assertErrorIs(t, err, ErrConflict)

	got, _ := store.GetActionResult(trustedCtx(), action)
	if got == nil || !got.Equal(first) {
		t.Fatalf("original result must remain intact, got %+v", got)
	}
	ev := log.last()
	if ev.Decision != "deny" || ev.Reason != "action_result_conflict" || ev.ActionHash != action.Hash {
		t.Fatalf("expected conflict audit event, got %+v", ev)
	}

	// Identical re-upload is idempotent and must succeed.
	if err := guard.AdmitActionResultWrite(trustedCtx(), action, first); err != nil {
		t.Fatalf("idempotent re-upload should pass: %v", err)
	}
	stillThere, lookupErr := store.GetActionResult(trustedCtx(), action)
	if lookupErr != nil || stillThere == nil {
		t.Fatal("action result vanished after idempotent re-upload")
	}
	_ = firstOut
}

func TestActionResultRejectsInvalidActionDigest(t *testing.T) {
	guard, _, _ := newTestGuard(t)
	err := guard.AdmitActionResultWrite(trustedCtx(), Digest{Hash: "nope", Size: 1}, &ActionResult{})
	assertErrorCode(t, err, CodeInvalidArgument)
}

// ---- gRPC front dispatch --------------------------------------------------

func TestFrontDispatchesMethods(t *testing.T) {
	guard, store, _ := newTestGuard(t)
	front := NewFront(guard)
	result, _ := validResult(t, store)
	action := shaDigest(t, []byte("action spec"))

	t.Run("read methods pass through", func(t *testing.T) {
		for _, m := range []string{MethodCASFindMissing, MethodByteStreamRead, MethodByteStreamQuery} {
			if err := front.Intercept(prCtx(), m, nil); err != nil {
				t.Fatalf("%s must be allowed through: %v", m, err)
			}
		}
	})

	t.Run("PR UpdateActionResult blocked at the front", func(t *testing.T) {
		req := ActionCacheUpdateRequest{ActionDigest: action, Result: result}
		err := front.Intercept(prCtx(), MethodActionCacheUpdate, req)
		assertErrorIs(t, err, ErrPermissionDenied)
	})

	t.Run("trusted UpdateActionResult admitted", func(t *testing.T) {
		req := ActionCacheUpdateRequest{ActionDigest: action, Result: result}
		if err := front.Intercept(trustedCtx(), MethodActionCacheUpdate, req); err != nil {
			t.Fatalf("trusted update: %v", err)
		}
	})

	t.Run("batch upload with one forged entry fails atomically", func(t *testing.T) {
		good := shaDigest(t, []byte("good batch entry"))
		forged := Digest{Hash: strings.Repeat("b", 64), Size: 3}
		req := []CASBlobUpload{
			{ResourceName: blobResource(good), Data: []byte("good batch entry")},
			{ResourceName: blobResource(forged), Data: []byte("xyz")},
		}
		err := front.Intercept(prCtx(), MethodCASBatchUpdateBlobs, req)
		assertErrorIs(t, err, ErrDigestMismatch)
		if store.HasBlob(forged) {
			t.Fatal("forged batch entry must not be stored")
		}
	})

	t.Run("unknown method default pass-through, fail-closed when configured", func(t *testing.T) {
		if err := front.Intercept(prCtx(), "/some.Other/Rpc", nil); err != nil {
			t.Fatalf("default front should pass unknown methods: %v", err)
		}
		front.DenyUnknown = true
		err := front.Intercept(prCtx(), "/some.Other/Rpc", nil)
		assertErrorCode(t, err, CodePermissionDenied)
	})

	t.Run("wrong request type rejected", func(t *testing.T) {
		if err := front.Intercept(trustedCtx(), MethodActionCacheUpdate, "not-a-request"); err == nil {
			t.Fatal("expected invalid-argument error for malformed request")
		}
	})
}

func TestBackendFaultSurfacesAsInternal(t *testing.T) {
	guard, store, _ := newTestGuard(t)
	boom := errors.New("backend disk on fire")
	store.SetFault(boom)

	d := shaDigest(t, []byte("x"))
	_, err := guard.AdmitCASWrite(trustedCtx(), blobResource(d), []byte("x"))
	assertErrorCode(t, err, CodeInternal)
}
