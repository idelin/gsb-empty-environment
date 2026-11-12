package admission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Guard is the write-admission engine placed in front of a Bazel remote
// cache. It is transport-agnostic: the gRPC layer translates protobuf
// messages into the domain types below and calls the Admit* methods.
//
// Invariants enforced:
//
//   - ActionCache writes require a trusted identity; PR builds are read-only.
//   - CAS blobs must hash to the sha256 and match the size claimed by their
//     resource name.
//   - Every output digest referenced by an ActionResult must already exist
//     in CAS with a matching stored size.
//   - An action digest may not be overwritten with a different ActionResult;
//     identical re-uploads are idempotent.
type Guard struct {
	Store  Store
	Logger Logger
}

// NewGuard builds a Guard. A nil logger defaults to DiscardLogger.
func NewGuard(store Store, logger Logger) *Guard {
	if logger == nil {
		logger = DiscardLogger{}
	}
	return &Guard{Store: store, Logger: logger}
}

// AdmitCASWrite validates a one-shot CAS upload (FindMissingBlobs batch
// update / CasBlob upload path): parses the resource name, recomputes
// sha256(data), checks size, then persists the blob. CAS writes do not
// require trust -- any writer may upload content, but only under the digest
// the content actually has, so forged digests can never land.
func (g *Guard) AdmitCASWrite(ctx context.Context, resourceName string, data []byte) (Digest, error) {
	const op = "cas.write"
	resource, err := ParseResource(resourceName)
	if err != nil {
		g.reject(op, IdentityFromContext(ctx), Digest{}, resourceName, err)
		return Digest{}, err
	}
	if err := VerifyBlob(resource.Digest, data); err != nil {
		g.reject(op, IdentityFromContext(ctx), resource.Digest, resourceName, err)
		return resource.Digest, err
	}
	if err := g.Store.PutBlob(ctx, resource.Digest, data); err != nil {
		return resource.Digest, fail(op, ErrInternal, "store put failed: %v", err)
	}
	return resource.Digest, nil
}

// NewBlobWriter starts a ByteStream upload. The returned writer hashes
// streamed chunks as they arrive and only validates/persists on Commit,
// matching the ByteStream Write/WriteCompleted lifecycle.
func (g *Guard) NewBlobWriter(ctx context.Context, resourceName string) (*BlobWriter, error) {
	const op = "bytestream.write"
	resource, err := ParseResource(resourceName)
	if err != nil {
		g.reject(op, IdentityFromContext(ctx), Digest{}, resourceName, err)
		return nil, err
	}
	if !resource.Upload {
		err := fail(op, ErrInvalidDigest, "resource %q is not a ByteStream upload name", resourceName)
		g.reject(op, IdentityFromContext(ctx), resource.Digest, resourceName, err)
		return nil, err
	}
	return &BlobWriter{
		guard:        g,
		ctx:          ctx,
		resourceName: resourceName,
		digest:       resource.Digest,
		hasher:       sha256.New(),
	}, nil
}

// AdmitActionResultWrite enforces the ActionCache write policy. The caller
// identity must be trusted, every referenced output must be present in CAS
// with matching size, and a different result for the same action digest is
// rejected as an overwrite conflict. On success the result is persisted.
func (g *Guard) AdmitActionResultWrite(ctx context.Context, actionDigest Digest, result *ActionResult) error {
	const op = "actioncache.write"
	if result == nil {
		err := fail(op, ErrInvalidDigest, "nil ActionResult")
		g.reject(op, IdentityFromContext(ctx), actionDigest, "", err)
		return err
	}
	if err := actionDigest.Validate(); err != nil {
		wrapped := fail(op, ErrInvalidDigest, "action digest invalid: %v", err)
		g.reject(op, IdentityFromContext(ctx), actionDigest, "", wrapped)
		return wrapped
	}

	id := IdentityFromContext(ctx)
	if !id.Trusted {
		err := fail(op, ErrPermissionDenied, "writer %q (source %q) may not update the action cache", id.Name, id.Source)
		g.reject(op, id, actionDigest, "", err)
		return err
	}

	for _, ref := range result.ReferencedDigests() {
		if err := ref.Validate(); err != nil {
			wrapped := fail(op, ErrInvalidDigest, "referenced output digest invalid: %v", err)
			g.reject(op, id, actionDigest, "", wrapped)
			return wrapped
		}
		exists, sizeOK, err := g.Store.BlobStatus(ctx, ref)
		if err != nil {
			return fail(op, ErrInternal, "cas lookup for %s failed: %v", ref.Hash, err)
		}
		if !exists || !sizeOK {
			wrapped := fail(op, ErrMissingBlob, "output digest %s/%d exists=%v size_ok=%v", ref.Hash, ref.Size, exists, sizeOK)
			g.reject(op, id, actionDigest, "", wrapped)
			return wrapped
		}
	}

	existing, err := g.Store.GetActionResult(ctx, actionDigest)
	if err != nil {
		return fail(op, ErrInternal, "action cache lookup failed: %v", err)
	}
	if existing != nil && !existing.Equal(result) {
		wrapped := fail(op, ErrConflict, "action %s already stores a different result", actionDigest.Hash)
		g.reject(op, id, actionDigest, "", wrapped)
		return wrapped
	}

	if err := g.Store.PutActionResult(ctx, actionDigest, result); err != nil {
		return fail(op, ErrInternal, "action cache put failed: %v", err)
	}
	g.Logger.Log(Event{
		Op:         op,
		Decision:   "allow",
		Writer:     id.Name,
		Source:     id.Source,
		Trusted:    true,
		ActionHash: actionDigest.Hash,
	})
	return nil
}

// VerifyBlob recomputes the sha256 of data and compares both hash and size
// against the claimed digest.
func VerifyBlob(claimed Digest, data []byte) error {
	const op = "cas.verify"
	if int64(len(data)) != claimed.Size {
		return fail(op, ErrSizeMismatch, "declared size %d, got %d bytes", claimed.Size, len(data))
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != claimed.Hash {
		return fail(op, ErrDigestMismatch, "declared hash %s, computed %s", claimed.Hash, got)
	}
	return nil
}

func (g *Guard) reject(op string, id Identity, digest Digest, resourceName string, err error) {
	var actionHash string
	if op == "actioncache.write" {
		actionHash = digest.Hash
	}
	g.Logger.Log(Event{
		Op:           op,
		Decision:     "deny",
		Code:         string(CodeOf(err)),
		Reason:       reasonOf(err),
		Writer:       id.Name,
		Source:       id.Source,
		Trusted:      id.Trusted,
		DigestHash:   digest.Hash,
		DigestSize:   digest.Size,
		ActionHash:   actionHash,
		ResourceName: resourceName,
		Message:      err.Error(),
	})
}

func reasonOf(err error) string {
	if ae, ok := err.(*AdmissionError); ok {
		return ae.Reason
	}
	return ""
}
