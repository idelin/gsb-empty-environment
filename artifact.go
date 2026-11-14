package netsec

import (
	"context"
	"fmt"
	"strings"
)

// Artifact is the repository response for an exact module version.
//
// SourceID is repository provenance, not untrusted package metadata. Content
// must be the downloaded module zip. Metadata is untrusted and is never used
// to choose a source, version, checksum, or signer.
type Artifact struct {
	SourceID   string            `json:"source_id"`
	ModulePath string            `json:"module_path"`
	Version    string            `json:"version"`
	Checksum   string            `json:"checksum"`
	Content    []byte            `json:"content"`
	Signature  *Signature        `json:"signature,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Repository is one fetch endpoint bound to a logical source.
type Repository interface {
	Fetch(ctx context.Context, source Source, request Request) (Artifact, error)
}

// RepositoryBinding assigns a repository endpoint to a source. Multiple
// bindings for the same source form a fixed-order mirror chain; falling back
// between those endpoints is recorded, but falling back to another source is
// never permitted.
type RepositoryBinding struct {
	SourceID   string
	Repository Repository
}

func (r *Resolver) verifyArtifact(
	ctx context.Context,
	source Source,
	namespace string,
	request Request,
	artifact Artifact,
	events *[]AuditEvent,
) error {
	if artifact.SourceID == "" || artifact.SourceID != source.ID {
		r.record(ctx, events, AuditEvent{
			Kind: AuditConflict, ModulePath: request.ModulePath, Version: request.Version,
			SourceID: artifact.SourceID, Namespace: namespace,
			Detail: fmt.Sprintf("artifact claims source %q; policy pins %q", artifact.SourceID, source.ID),
		})
		return fmt.Errorf("%w: module %s@%s came from %q, pinned source is %q",
			ErrSourceConflict, request.ModulePath, request.Version, artifact.SourceID, source.ID)
	}
	if artifact.ModulePath != request.ModulePath {
		return fmt.Errorf("%w: expected module %q, repository returned %q",
			ErrInvalidArtifact, request.ModulePath, artifact.ModulePath)
	}
	if artifact.Version != request.Version {
		return fmt.Errorf("%w: requested %q, repository returned %q",
			ErrVersionMismatch, request.Version, artifact.Version)
	}

	r.recordMetadataIgnored(ctx, events, request, source, namespace, artifact.Metadata)

	if request.ExpectedChecksum != "" && artifact.Checksum != request.ExpectedChecksum {
		return fmt.Errorf("%w: trusted checksum %s, repository supplied %s",
			ErrChecksumMismatch, request.ExpectedChecksum, artifact.Checksum)
	}
	if artifact.Checksum == "" {
		return fmt.Errorf("%w: repository supplied no checksum", ErrInvalidArtifact)
	}
	if len(artifact.Content) == 0 {
		return fmt.Errorf("%w: repository supplied no module content", ErrInvalidArtifact)
	}
	if err := VerifyContentChecksum(artifact.Content, artifact.Checksum); err != nil {
		return err
	}

	requireSignature := r.signatureMode == SignaturesRequired || request.ExpectedSignerKeyID != ""
	if r.signatureMode == SignaturesIgnored && request.ExpectedSignerKeyID == "" {
		return nil
	}
	if artifact.Signature == nil {
		if requireSignature {
			return fmt.Errorf("%w: module %s@%s from %q",
				ErrSignatureMissing, request.ModulePath, request.Version, source.ID)
		}
		return nil
	}
	if artifact.Signature.KeyID == "" {
		return fmt.Errorf("%w: empty signer key ID", ErrSignatureInvalid)
	}
	if request.ExpectedSignerKeyID != "" && artifact.Signature.KeyID != request.ExpectedSignerKeyID {
		return fmt.Errorf("%w: expected key %q, artifact signed by %q",
			ErrUntrustedSigner, request.ExpectedSignerKeyID, artifact.Signature.KeyID)
	}
	if r.verifier == nil {
		return fmt.Errorf("%w", ErrSignatureVerifierMissing)
	}

	statement := SignedStatement{
		SourceID: source.ID, ModulePath: request.ModulePath,
		Version: request.Version, Checksum: artifact.Checksum,
	}
	if err := r.verifier.Verify(
		ctx,
		artifact.Signature.KeyID,
		artifact.Signature.Algorithm,
		CanonicalStatement(statement),
		artifact.Signature.Value,
	); err != nil {
		return err
	}
	return nil
}

func (r *Resolver) recordMetadataIgnored(
	ctx context.Context,
	events *[]AuditEvent,
	request Request,
	source Source,
	namespace string,
	metadata map[string]string,
) {
	for key, value := range metadata {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "source", "source_id", "sourceid", "repository", "repo", "origin", "proxy",
			"checksum", "sum", "signature", "signer", "version":
			r.record(ctx, events, AuditEvent{
				Kind: AuditMetadataIgnored, ModulePath: request.ModulePath,
				Version: request.Version, SourceID: source.ID, Namespace: namespace,
				Detail: fmt.Sprintf("ignored package metadata %q=%q", key, value),
			})
		}
	}
}
