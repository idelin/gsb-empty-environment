package netsec

import (
	"fmt"
	"strings"
)

const (
	platformUnknown = "unknown"
	attestationType = "attestation-manifest"
)

func normalizePlatform(platform Platform) Platform {
	normalized := Platform{
		OS:           strings.ToLower(strings.TrimSpace(platform.OS)),
		Architecture: strings.ToLower(strings.TrimSpace(platform.Architecture)),
		Variant:      strings.ToLower(strings.TrimSpace(platform.Variant)),
		OSVersion:    strings.TrimSpace(platform.OSVersion),
	}

	switch normalized.Architecture {
	case "x86_64", "x86-64":
		normalized.Architecture = "amd64"
	case "aarch64":
		normalized.Architecture = "arm64"
	}
	switch normalized.OS {
	case "macos":
		normalized.OS = "darwin"
	}

	if normalized.Architecture == "arm64" {
		switch normalized.Variant {
		case "", "8", "v8":
			normalized.Variant = ""
		}
	}
	if normalized.Architecture == "arm" {
		switch normalized.Variant {
		case "5", "6", "7":
			normalized.Variant = "v" + normalized.Variant
		}
	}

	return normalized
}

func validatePlatform(platform Platform) error {
	normalized := normalizePlatform(platform)
	if normalized.OS == "" || normalized.Architecture == "" {
		return fmt.Errorf("%w: os and architecture are required", ErrInvalidPlatform)
	}
	if normalized.OS == platformUnknown || normalized.Architecture == platformUnknown {
		return fmt.Errorf("%w: unknown is not a runnable platform", ErrInvalidPlatform)
	}
	return nil
}

func platformKey(platform Platform) string {
	normalized := normalizePlatform(platform)
	return strings.Join(
		[]string{normalized.OS, normalized.Architecture, normalized.Variant, normalized.OSVersion},
		"\x00",
	)
}

func platformMatches(candidate Platform, requested Platform) bool {
	candidate = normalizePlatform(candidate)
	requested = normalizePlatform(requested)

	if candidate.OS != requested.OS || candidate.Architecture != requested.Architecture {
		return false
	}
	if candidate.Variant != requested.Variant {
		return false
	}
	if requested.OSVersion != "" && candidate.OSVersion != requested.OSVersion {
		return false
	}
	return true
}

func isUnknownPlatform(platform *Platform) bool {
	if platform == nil {
		return true
	}
	normalized := normalizePlatform(*platform)
	return normalized.OS == "" ||
		normalized.Architecture == "" ||
		normalized.OS == platformUnknown ||
		normalized.Architecture == platformUnknown
}

func isAttestationDescriptor(descriptor Descriptor) bool {
	if descriptor.Annotations == nil {
		return false
	}
	return descriptor.Annotations["vnd.docker.reference.type"] == attestationType
}

func isImageMediaType(mediaType string) bool {
	switch mediaType {
	case "", MediaTypeOCIManifest, MediaTypeDockerManifest:
		return true
	default:
		return false
	}
}

func isIndexMediaType(mediaType string) bool {
	switch mediaType {
	case "", MediaTypeOCIIndex, MediaTypeDockerList:
		return true
	default:
		return false
	}
}

func isRunnableDescriptor(descriptor Descriptor) bool {
	if descriptor.ArtifactType != "" || isAttestationDescriptor(descriptor) || isUnknownPlatform(descriptor.Platform) {
		return false
	}
	if descriptor.MediaType == "" {
		return true
	}
	return descriptor.MediaType == MediaTypeOCIManifest ||
		descriptor.MediaType == MediaTypeDockerManifest ||
		descriptor.MediaType == MediaTypeOCIIndex ||
		descriptor.MediaType == MediaTypeDockerList
}
