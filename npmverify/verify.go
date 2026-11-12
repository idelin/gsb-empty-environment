// Package npmverify implements the trust checks a Go package-management
// proxy performs around an npm registry mirror.
//
// A compromised mirror can point a dist-tag at an attacker-controlled
// version, or serve a tarball whose bytes do not match the published
// metadata. Verify closes those gaps by chaining four checks after a
// dist-tag has been resolved to a concrete version:
//
//   - the resolved version must exist in the packument;
//   - the tarball's hash must match the strongest digest in its SRI
//     "integrity" field;
//   - at least one dist.signatures entry over "name@version:integrity"
//     must be accepted by the caller-supplied SignatureVerifier callback;
//   - the name and version inside package.json must match the metadata.
//
// The package deliberately performs no network or key-management work:
// the embedding proxy fetches documents and owns the trusted registry
// keys, expressing that trust through the verifier callback.
package npmverify

import (
	"context"
	"errors"
	"fmt"
)

// ResolveTag maps a dist-tag (e.g. "latest") to its concrete version and
// metadata. It returns ErrTagNotFound when the tag is absent or points at a
// version not published in the packument's versions map.
func ResolveTag(doc *Packument, tag string) (string, *PkgVersion, error) {
	if doc == nil {
		return "", nil, ErrNoPackument
	}
	version, ok := doc.DistTags[tag]
	if !ok {
		return "", nil, fmt.Errorf("%w: tag %q", ErrTagNotFound, tag)
	}
	pkg, ok := doc.Versions[version]
	if !ok || pkg == nil {
		return "", nil, fmt.Errorf("%w: %s -> %s", ErrTagNotFound, tag, version)
	}
	return version, pkg, nil
}

// SignatureMessage returns the exact string npm registry signatures cover:
// "name@version:integrity", where integrity is the full SRI value from the
// version's dist object.
func SignatureMessage(name, version, integrity string) string {
	return name + "@" + version + ":" + integrity
}

// Verify runs the full trust chain for tarball fetched under tag. The
// returned Result is populated only when every check passes.
func Verify(ctx context.Context, doc *Packument, tag string, tarball []byte, verifySig SignatureVerifier, opts *Options) (*Result, error) {
	if doc == nil {
		return nil, ErrNoPackument
	}
	if verifySig == nil {
		return nil, ErrNoVerifier
	}

	resolved, pkg, err := ResolveTag(doc, tag)
	if err != nil {
		return nil, err
	}

	// 1. Bind the bytes to the metadata via SRI, preferring the strongest
	// declared digest when several algorithms are present.
	best, err := verifyIntegrity(pkg.Dist.Integrity, tarball)
	if err != nil {
		return nil, err
	}

	// 2. Bind name@version:integrity to a trusted registry key through the
	// caller's signature callback.
	acceptedKeyID, err := verifySignatures(ctx, pkg, verifySig, opts)
	if err != nil {
		return nil, err
	}

	// 3. Confirm the archive's own identity agrees with the resolved entry.
	manifest, err := TarballManifest(tarball)
	if err != nil {
		return nil, err
	}
	if manifest.Name != pkg.Name {
		return nil, fmt.Errorf("%w: tarball %q vs metadata %q", ErrNameMismatch, manifest.Name, pkg.Name)
	}
	if manifest.Version != pkg.Version || pkg.Version != resolved {
		return nil, fmt.Errorf("%w: tarball %q vs metadata %q", ErrVersionMismatch, manifest.Version, pkg.Version)
	}

	return &Result{
		ResolvedVersion: resolved,
		Algorithm:       best.Algorithm,
		Digest:          best.Digest,
		AcceptedKeyID:   acceptedKeyID,
	}, nil
}

func verifySignatures(ctx context.Context, pkg *PkgVersion, verifySig SignatureVerifier, opts *Options) (string, error) {
	require := true
	if opts != nil && opts.RequireSignature != nil {
		require = *opts.RequireSignature
	}

	if len(pkg.Dist.Signatures) == 0 {
		if require {
			return "", fmt.Errorf("%w: no signatures present", ErrSignatureFailed)
		}
		return "", nil
	}

	message := SignatureMessage(pkg.Name, pkg.Version, pkg.Dist.Integrity)
	var failures []error
	for _, sig := range pkg.Dist.Signatures {
		if err := verifySig(ctx, message, sig); err == nil {
			return sig.KeyID, nil
		} else {
			failures = append(failures, err)
		}
	}

	return "", fmt.Errorf("%w: %d signature(s) rejected (%w)", ErrSignatureFailed, len(failures), errors.Join(failures...))
}
