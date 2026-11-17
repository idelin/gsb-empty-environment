package netsec

import (
	"errors"
	"strings"
)

// defaultRegistry mirrors the container runtime convention.
const defaultRegistry = "docker.io"

// ParseImage parses an OCI image reference. A digest is mandatory: an
// approval must never bind a floating tag. When both tag and digest are
// present the tag is retained for readability but the digest remains the
// identity used for authorization.
func ParseImage(ref string) (ImageRef, error) {
	original := ref
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ImageRef{}, errors.Join(errImage, errors.New("empty image reference"))
	}

	var digest string
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		digest = ref[at+1:]
		ref = ref[:at]
		if digest == "" || !strings.Contains(digest, ":") {
			return ImageRef{}, errors.Join(errImage, errors.New("malformed digest in "+original))
		}
	}
	if digest == "" {
		return ImageRef{}, errors.Join(errImage, errors.New("image "+original+" is not pinned by a digest"))
	}

	var tag string
	// A tag can only appear before a digest and after the final slash, so the
	// colon cannot belong to a host:port pair.
	lastSlash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
		tag = ref[colon+1:]
		ref = ref[:colon]
		if tag == "" {
			return ImageRef{}, errors.Join(errImage, errors.New("empty tag in "+original))
		}
	}

	var registry, repository string
	if firstSlash := strings.Index(ref, "/"); firstSlash >= 0 &&
		(strings.Contains(ref[:firstSlash], ".") ||
			strings.Contains(ref[:firstSlash], ":") ||
			ref[:firstSlash] == "localhost") {
		registry = ref[:firstSlash]
		repository = ref[firstSlash+1:]
	} else {
		registry = defaultRegistry
		repository = ref
	}
	if registry == "" || repository == "" {
		return ImageRef{}, errors.Join(errImage, errors.New("malformed reference "+original))
	}

	canonical := registry + "/" + repository + "@" + digest
	return ImageRef{
		Original:   original,
		Registry:   registry,
		Repository: repository,
		Tag:        tag,
		Digest:     digest,
		Canonical:  canonical,
	}, nil
}

// SameDigest reports whether two references resolve to the same pinned
// digest. Registry/repository differences are rejected as well: a digest
// alone must not silently authorize a different repository.
func (r ImageRef) SameDigest(other ImageRef) bool {
	return r.Digest != "" && r.Digest == other.Digest &&
		r.Registry == other.Registry && r.Repository == other.Repository
}
