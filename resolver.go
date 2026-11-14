package netsec

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

// ResolverOptions controls non-default parsing behavior.
type ResolverOptions struct {
	// AllowMixedDigestAlgorithms permits different algorithms at different
	// levels of one chain. The secure default rejects such chains.
	AllowMixedDigestAlgorithms bool
}

// Resolver selects a platform from a verified OCI/Docker digest chain.
type Resolver struct {
	source                     BlobSource
	tags                       TagSource
	allowMixedDigestAlgorithms bool

	cacheMutex sync.RWMutex
	blobCache  map[string][]byte
}

// NewResolver creates a resolver using the secure default options. If tags is
// omitted and source also implements TagSource, source is used for tags.
func NewResolver(source BlobSource, tags ...TagSource) *Resolver {
	var tagSource TagSource
	if len(tags) > 0 {
		tagSource = tags[0]
	} else if inferred, implementsTags := source.(TagSource); implementsTags {
		tagSource = inferred
	}
	return NewResolverWithOptions(source, tagSource, ResolverOptions{})
}

// NewResolverWithOptions creates a resolver with explicit options.
func NewResolverWithOptions(source BlobSource, tags TagSource, options ResolverOptions) *Resolver {
	return &Resolver{
		source:                     source,
		tags:                       tags,
		allowMixedDigestAlgorithms: options.AllowMixedDigestAlgorithms,
		blobCache:                  make(map[string][]byte),
	}
}

// CachedBlobCount reports how many verified blobs are currently cached.
func (resolver *Resolver) CachedBlobCount() int {
	resolver.cacheMutex.RLock()
	defer resolver.cacheMutex.RUnlock()
	return len(resolver.blobCache)
}

// ClearCache drops all verified content. Tag state is never cached.
func (resolver *Resolver) ClearCache() {
	resolver.cacheMutex.Lock()
	defer resolver.cacheMutex.Unlock()
	resolver.blobCache = make(map[string][]byte)
}

// Resolve maps a tag or digest to an immutable digest and selects one verified
// platform from its manifest list. A tag is resolved afresh on every call.
func (resolver *Resolver) Resolve(
	ctx context.Context,
	reference string,
	requestedPlatform Platform,
) (*ResolvedImage, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if reference == "" {
		return nil, fmt.Errorf("%w: empty reference", ErrInvalidReference)
	}
	if platformErr := validatePlatform(requestedPlatform); platformErr != nil {
		return nil, platformErr
	}
	if resolver.source == nil {
		return nil, fmt.Errorf("%w: nil blob source", ErrFetchFailed)
	}

	rootDigest, rootErr := resolver.resolveRootDigest(ctx, reference)
	if rootErr != nil {
		return nil, rootErr
	}
	rootAlgorithm := rootDigest.Algorithm()

	rootDescriptor := Descriptor{Digest: rootDigest.String()}
	rootContent, rootContentErr := resolver.loadVerifiedBlob(ctx, rootDescriptor, rootAlgorithm)
	if rootContentErr != nil {
		return nil, fmt.Errorf("load root %s: %w", rootDigest, rootContentErr)
	}

	document, documentErr := inspectDocument(rootContent)
	if documentErr != nil {
		return nil, documentErr
	}

	switch document.kind {
	case documentIndex:
		return resolver.resolveIndex(
			ctx,
			rootDigest,
			document.index,
			reference,
			requestedPlatform,
			rootAlgorithm,
		)
	case documentManifest:
		return resolver.resolveSingleManifest(
			ctx,
			rootDigest,
			document.manifest,
			rootContent,
			reference,
			requestedPlatform,
			rootAlgorithm,
		)
	default:
		return nil, fmt.Errorf("%w: unrecognized root document", ErrInvalidManifest)
	}
}

// ResolveDigest resolves a digest without consulting tags.
func (resolver *Resolver) ResolveDigest(
	ctx context.Context,
	digest string,
	requestedPlatform Platform,
) (*ResolvedImage, error) {
	return resolver.Resolve(ctx, digest, requestedPlatform)
}

func (resolver *Resolver) resolveRootDigest(ctx context.Context, reference string) (Digest, error) {
	parsed, parseErr := ParseDigest(reference)
	if parseErr == nil {
		return parsed, nil
	}
	if looksLikeDigestReference(reference) {
		return "", fmt.Errorf("%w: %w", ErrInvalidReference, parseErr)
	}

	if resolver.tags == nil {
		return "", fmt.Errorf("%w: no tag source for %q", ErrTagResolutionFailed, reference)
	}
	digestString, resolveErr := resolver.tags.ResolveTag(ctx, reference)
	if resolveErr != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrTagResolutionFailed, reference, resolveErr)
	}
	digest, digestErr := ParseDigest(digestString)
	if digestErr != nil {
		return "", fmt.Errorf(
			"%w: %w: tag %q returned invalid digest %q",
			ErrInvalidReference,
			digestErr,
			reference,
			digestString,
		)
	}
	return digest, nil
}

func looksLikeDigestReference(reference string) bool {
	algorithm, encoded, found := strings.Cut(reference, ":")
	if !found {
		return false
	}
	if _, supported := digestEncodedSizes[strings.ToLower(algorithm)]; supported {
		return true
	}
	if strings.Contains(algorithm, "/") || strings.Contains(encoded, "/") {
		return false
	}
	_, decodeErr := hex.DecodeString(encoded)
	return decodeErr == nil
}

func (resolver *Resolver) resolveIndex(
	ctx context.Context,
	rootDigest Digest,
	index *Index,
	reference string,
	requestedPlatform Platform,
	chainAlgorithm string,
) (*ResolvedImage, error) {
	if index.ArtifactType != "" {
		return nil, fmt.Errorf("%w: index artifactType %q", ErrNotImageManifest, index.ArtifactType)
	}
	if index.Subject != nil {
		return nil, fmt.Errorf("%w: image index has a subject referrer", ErrNotImageManifest)
	}
	if index.Manifests == nil {
		return nil, fmt.Errorf("%w: index has no manifests field", ErrInvalidManifest)
	}
	if duplicateErr := validateIndexEntries(index, chainAlgorithm, resolver.allowMixedDigestAlgorithms); duplicateErr != nil {
		return nil, duplicateErr
	}

	matches := make([]Descriptor, 0, 1)
	for _, descriptor := range index.Manifests {
		if !isRunnableDescriptor(descriptor) {
			continue
		}
		if platformMatches(*descriptor.Platform, requestedPlatform) {
			matches = append(matches, descriptor)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf(
			"%w: no %s/%s variant=%q version=%q child",
			ErrPlatformNotFound,
			requestedPlatform.OS,
			requestedPlatform.Architecture,
			requestedPlatform.Variant,
			requestedPlatform.OSVersion,
		)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf(
			"%w: %d children match %s/%s variant=%q version=%q",
			ErrAmbiguousPlatform,
			len(matches),
			requestedPlatform.OS,
			requestedPlatform.Architecture,
			requestedPlatform.Variant,
			requestedPlatform.OSVersion,
		)
	}

	selected := matches[0]
	selectedDigest, digestErr := ParseDigest(selected.Digest)
	if digestErr != nil {
		return nil, digestErr
	}
	selectedContent, contentErr := resolver.loadVerifiedBlob(ctx, selected, chainAlgorithm)
	if contentErr != nil {
		return nil, fmt.Errorf("load selected child %s: %w", selectedDigest, contentErr)
	}

	childDocument, inspectErr := inspectDocument(selectedContent)
	if inspectErr != nil {
		return nil, inspectErr
	}
	if mediaTypeErr := checkDescriptorMediaType(selected, childDocument); mediaTypeErr != nil {
		return nil, mediaTypeErr
	}

	switch childDocument.kind {
	case documentIndex:
		nestedPlatform := requestedPlatform
		if nestedPlatform.OSVersion == "" && selected.Platform != nil {
			nestedPlatform.OSVersion = normalizePlatform(*selected.Platform).OSVersion
		}
		return resolver.resolveIndex(
			ctx,
			rootDigest,
			childDocument.index,
			reference,
			nestedPlatform,
			chainAlgorithm,
		)
	case documentManifest:
		return resolver.buildResolvedImage(
			ctx,
			rootDigest,
			selectedDigest,
			selected,
			*selected.Platform,
			childDocument.manifest,
			selectedContent,
			reference,
			chainAlgorithm,
		)
	default:
		return nil, fmt.Errorf("%w: selected child has unknown document type", ErrInvalidManifest)
	}
}

func (resolver *Resolver) resolveSingleManifest(
	ctx context.Context,
	rootDigest Digest,
	manifest *Manifest,
	manifestContent []byte,
	reference string,
	requestedPlatform Platform,
	chainAlgorithm string,
) (*ResolvedImage, error) {
	configContent, configErr := resolver.loadVerifiedBlob(ctx, manifest.Config, chainAlgorithm)
	if configErr != nil {
		return nil, fmt.Errorf("load image config %s: %w", manifest.Config.Digest, configErr)
	}
	config, configDecodeErr := decodeImageConfig(configContent)
	if configDecodeErr != nil {
		return nil, configDecodeErr
	}
	if config.OS == "" || config.Architecture == "" {
		return nil, fmt.Errorf("%w: single-manifest image config lacks os/architecture", ErrConfigPlatformMismatch)
	}

	configPlatform := Platform{
		OS:           config.OS,
		Architecture: config.Architecture,
		Variant:      config.Variant,
		OSVersion:    config.OSVersion,
	}
	if !platformMatches(configPlatform, requestedPlatform) {
		return nil, fmt.Errorf(
			"%w: image is %s/%s variant=%q version=%q",
			ErrPlatformNotFound,
			configPlatform.OS,
			configPlatform.Architecture,
			configPlatform.Variant,
			configPlatform.OSVersion,
		)
	}

	rootDescriptor := Descriptor{Digest: rootDigest.String()}
	return resolver.buildResolvedImage(
		ctx,
		rootDigest,
		rootDigest,
		rootDescriptor,
		configPlatform,
		manifest,
		manifestContent,
		reference,
		chainAlgorithm,
	)
}

func (resolver *Resolver) buildResolvedImage(
	ctx context.Context,
	rootDigest Digest,
	manifestDigest Digest,
	manifestDescriptor Descriptor,
	platform Platform,
	manifest *Manifest,
	manifestContent []byte,
	reference string,
	chainAlgorithm string,
) (*ResolvedImage, error) {
	if manifest.ArtifactType != "" {
		return nil, fmt.Errorf("%w: manifest artifactType %q", ErrNotImageManifest, manifest.ArtifactType)
	}
	if manifest.Subject != nil {
		return nil, fmt.Errorf("%w: manifest has a subject referrer", ErrNotImageManifest)
	}
	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("%w: image manifest has no config digest", ErrInvalidManifest)
	}
	if manifest.Config.Size < 0 {
		return nil, fmt.Errorf("%w: negative config size", ErrInvalidManifest)
	}
	if !isConfigMediaType(manifest.Config.MediaType) {
		return nil, fmt.Errorf("%w: config media type %q", ErrUnsupportedMediaType, manifest.Config.MediaType)
	}

	configContent, configErr := resolver.loadVerifiedBlob(ctx, manifest.Config, chainAlgorithm)
	if configErr != nil {
		return nil, fmt.Errorf("load image config %s: %w", manifest.Config.Digest, configErr)
	}
	config, imageConfigErr := decodeImageConfig(configContent)
	if imageConfigErr != nil {
		return nil, imageConfigErr
	}
	if platformErr := checkConfigPlatform(config, platform); platformErr != nil {
		return nil, platformErr
	}

	if config.RootFS != nil && (config.RootFS.Type != "" || config.RootFS.DiffIDs != nil) {
		if len(config.RootFS.DiffIDs) != len(manifest.Layers) {
			return nil, fmt.Errorf(
				"%w: %w: config has %d diff_ids, manifest has %d layers",
				ErrLayerCountMismatch,
				ErrMissingLayer,
				len(config.RootFS.DiffIDs),
				len(manifest.Layers),
			)
		}
		for _, diffID := range config.RootFS.DiffIDs {
			if _, diffErr := ParseDigest(diffID); diffErr != nil {
				return nil, fmt.Errorf("%w: invalid layer diff_id %q: %w", ErrInvalidManifest, diffID, diffErr)
			}
		}
	}

	layers := make([]VerifiedBlob, 0, len(manifest.Layers))
	for index, layerDescriptor := range manifest.Layers {
		if layerDescriptor.Size < 0 {
			return nil, fmt.Errorf("%w: layer %d has negative size", ErrInvalidManifest, index)
		}
		layerContent, layerErr := resolver.loadVerifiedBlob(ctx, layerDescriptor, chainAlgorithm)
		if layerErr != nil {
			return nil, fmt.Errorf(
				"%w: layer %d (%s): %w",
				ErrMissingLayer,
				index,
				layerDescriptor.Digest,
				layerErr,
			)
		}
		layers = append(layers, VerifiedBlob{
			Descriptor: layerDescriptor,
			Content:    layerContent,
		})
	}

	resolvedPlatform := normalizePlatform(platform)
	if resolvedPlatform.OSVersion == "" && config.OSVersion != "" {
		resolvedPlatform.OSVersion = config.OSVersion
	}

	return &ResolvedImage{
		Reference:          reference,
		RootDigest:         rootDigest,
		ManifestDigest:     manifestDigest,
		ManifestDescriptor: manifestDescriptor,
		Manifest:           *manifest,
		ManifestContent:    append([]byte(nil), manifestContent...),
		Platform:           resolvedPlatform,
		Config:             *config,
		ConfigBlob: VerifiedBlob{
			Descriptor: manifest.Config,
			Content:    configContent,
		},
		Layers: layers,
	}, nil
}

func validateIndexEntries(index *Index, chainAlgorithm string, allowMixedAlgorithms bool) error {
	seenPlatforms := make(map[string]Descriptor, len(index.Manifests))
	for _, descriptor := range index.Manifests {
		if !isRunnableDescriptor(descriptor) {
			continue
		}
		if _, digestErr := ParseDigest(descriptor.Digest); digestErr != nil {
			return fmt.Errorf("%w: index child digest %q: %w", ErrInvalidManifest, descriptor.Digest, digestErr)
		}
		if descriptor.Size < 0 {
			return fmt.Errorf("%w: index child %s has negative size", ErrInvalidManifest, descriptor.Digest)
		}
		if !allowMixedAlgorithms {
			if algorithmErr := verifyAlgorithm(Digest(descriptor.Digest), chainAlgorithm); algorithmErr != nil {
				return algorithmErr
			}
		}

		key := platformKey(*descriptor.Platform)
		if previous, duplicate := seenPlatforms[key]; duplicate {
			return fmt.Errorf(
				"%w: %s/%s variant=%q version=%q appears as both %s and %s",
				ErrDuplicatePlatform,
				normalizePlatform(*descriptor.Platform).OS,
				normalizePlatform(*descriptor.Platform).Architecture,
				normalizePlatform(*descriptor.Platform).Variant,
				normalizePlatform(*descriptor.Platform).OSVersion,
				previous.Digest,
				descriptor.Digest,
			)
		}
		seenPlatforms[key] = descriptor
	}
	return nil
}

func (resolver *Resolver) loadVerifiedBlob(
	ctx context.Context,
	descriptor Descriptor,
	chainAlgorithm string,
) ([]byte, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}

	digest, digestErr := ParseDigest(descriptor.Digest)
	if digestErr != nil {
		return nil, digestErr
	}
	if !resolver.allowMixedDigestAlgorithms {
		if algorithmErr := verifyAlgorithm(digest, chainAlgorithm); algorithmErr != nil {
			return nil, algorithmErr
		}
	}

	resolver.cacheMutex.RLock()
	cached, cachedFound := resolver.blobCache[digest.String()]
	resolver.cacheMutex.RUnlock()
	if cachedFound {
		if sizeErr := verifyDescriptorSize(descriptor, cached); sizeErr != nil {
			return nil, sizeErr
		}
		return append([]byte(nil), cached...), nil
	}

	if resolver.source == nil {
		return nil, fmt.Errorf("%w: nil blob source", ErrFetchFailed)
	}
	content, fetchErr := resolver.source.FetchBlob(ctx, digest.String())
	if fetchErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if errors.Is(fetchErr, ErrBlobNotFound) || errors.Is(fetchErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s: %w", ErrBlobNotFound, digest, fetchErr)
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrFetchFailed, digest, fetchErr)
	}
	if content == nil {
		return nil, fmt.Errorf("%w: %s returned no content", ErrBlobNotFound, digest)
	}
	if verifyErr := verifyContentDigest(digest, content); verifyErr != nil {
		return nil, fmt.Errorf("%w: blob %s", verifyErr, digest)
	}
	if sizeErr := verifyDescriptorSize(descriptor, content); sizeErr != nil {
		return nil, sizeErr
	}

	stored := append([]byte(nil), content...)
	resolver.cacheMutex.Lock()
	if _, alreadyCached := resolver.blobCache[digest.String()]; !alreadyCached {
		resolver.blobCache[digest.String()] = stored
	}
	resolver.cacheMutex.Unlock()

	return append([]byte(nil), content...), nil
}

func verifyDescriptorSize(descriptor Descriptor, content []byte) error {
	if descriptor.Size > 0 && int64(len(content)) != descriptor.Size {
		return fmt.Errorf(
			"%w: descriptor says %d bytes, content has %d",
			ErrSizeMismatch,
			descriptor.Size,
			len(content),
		)
	}
	return nil
}

type documentKind int

const (
	documentUnknown documentKind = iota
	documentIndex
	documentManifest
)

type document struct {
	kind      documentKind
	mediaType string
	index     *Index
	manifest  *Manifest
}

func inspectDocument(content []byte) (*document, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("%w: empty JSON document", ErrInvalidManifest)
	}

	var probe struct {
		SchemaVersion int             `json:"schemaVersion"`
		MediaType     string          `json:"mediaType"`
		Manifests     json.RawMessage `json:"manifests"`
		Config        json.RawMessage `json:"config"`
		Layers        json.RawMessage `json:"layers"`
		FSLayers      json.RawMessage `json:"fsLayers"`
	}
	if unmarshalErr := json.Unmarshal(content, &probe); unmarshalErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, unmarshalErr)
	}
	if probe.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: schemaVersion must be 2, got %d", ErrUnsupportedMediaType, probe.SchemaVersion)
	}
	if len(probe.FSLayers) > 0 {
		return nil, fmt.Errorf("%w: Docker schema 1 is not supported", ErrUnsupportedMediaType)
	}

	hasManifests := jsonFieldPresent(probe.Manifests)
	hasConfig := jsonFieldPresent(probe.Config)
	if hasManifests == hasConfig {
		return nil, fmt.Errorf("%w: document must contain exactly one of manifests or config", ErrInvalidManifest)
	}

	switch probe.MediaType {
	case MediaTypeOCIIndex, MediaTypeDockerList:
		if !hasManifests {
			return nil, fmt.Errorf("%w: index media type without manifests", ErrInvalidManifest)
		}
		var index Index
		if unmarshalErr := json.Unmarshal(content, &index); unmarshalErr != nil {
			return nil, fmt.Errorf("%w: decode index: %v", ErrInvalidManifest, unmarshalErr)
		}
		return &document{kind: documentIndex, mediaType: probe.MediaType, index: &index}, nil
	case MediaTypeOCIManifest, MediaTypeDockerManifest:
		if !hasConfig {
			return nil, fmt.Errorf("%w: image media type without config", ErrInvalidManifest)
		}
		var manifest Manifest
		if unmarshalErr := json.Unmarshal(content, &manifest); unmarshalErr != nil {
			return nil, fmt.Errorf("%w: decode manifest: %v", ErrInvalidManifest, unmarshalErr)
		}
		return &document{kind: documentManifest, mediaType: probe.MediaType, manifest: &manifest}, nil
	case "":
		if hasManifests {
			var index Index
			if unmarshalErr := json.Unmarshal(content, &index); unmarshalErr != nil {
				return nil, fmt.Errorf("%w: decode index: %v", ErrInvalidManifest, unmarshalErr)
			}
			return &document{kind: documentIndex, index: &index}, nil
		}
		var manifest Manifest
		if unmarshalErr := json.Unmarshal(content, &manifest); unmarshalErr != nil {
			return nil, fmt.Errorf("%w: decode manifest: %v", ErrInvalidManifest, unmarshalErr)
		}
		return &document{kind: documentManifest, manifest: &manifest}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedMediaType, probe.MediaType)
	}
}

func jsonFieldPresent(field json.RawMessage) bool {
	trimmed := bytes.TrimSpace(field)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func checkDescriptorMediaType(descriptor Descriptor, document *document) error {
	if descriptor.MediaType == "" {
		return nil
	}
	if document.kind == documentIndex && !isIndexMediaType(descriptor.MediaType) {
		return fmt.Errorf(
			"%w: descriptor says %q but content is an index",
			ErrMediaTypeMismatch,
			descriptor.MediaType,
		)
	}
	if document.kind == documentManifest && !isImageMediaType(descriptor.MediaType) {
		return fmt.Errorf(
			"%w: descriptor says %q but content is an image manifest",
			ErrMediaTypeMismatch,
			descriptor.MediaType,
		)
	}
	if document.mediaType != "" && descriptor.MediaType != document.mediaType {
		return fmt.Errorf(
			"%w: descriptor says %q, document says %q",
			ErrMediaTypeMismatch,
			descriptor.MediaType,
			document.mediaType,
		)
	}
	return nil
}

func isConfigMediaType(mediaType string) bool {
	switch mediaType {
	case "", MediaTypeOCIConfig, MediaTypeDockerConfig:
		return true
	default:
		return false
	}
}

func decodeImageConfig(content []byte) (*ImageConfig, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("%w: empty image config", ErrInvalidManifest)
	}
	var config ImageConfig
	if unmarshalErr := json.Unmarshal(content, &config); unmarshalErr != nil {
		return nil, fmt.Errorf("%w: decode config: %v", ErrInvalidManifest, unmarshalErr)
	}
	return &config, nil
}

func checkConfigPlatform(config *ImageConfig, platform Platform) error {
	expected := normalizePlatform(platform)

	if config.OS != "" && normalizePlatform(Platform{OS: config.OS}).OS != expected.OS {
		return fmt.Errorf("%w: config os %q, selected os %q", ErrConfigPlatformMismatch, config.OS, expected.OS)
	}
	if config.Architecture != "" {
		configArchitecture := normalizePlatform(Platform{Architecture: config.Architecture}).Architecture
		if configArchitecture != expected.Architecture {
			return fmt.Errorf(
				"%w: config architecture %q, selected architecture %q",
				ErrConfigPlatformMismatch,
				config.Architecture,
				expected.Architecture,
			)
		}
	}
	if config.Variant != "" {
		configArchitecture := config.Architecture
		if configArchitecture == "" {
			configArchitecture = expected.Architecture
		}
		configVariant := normalizePlatform(Platform{
			Architecture: configArchitecture,
			Variant:      config.Variant,
		}).Variant
		if configVariant != expected.Variant {
			return fmt.Errorf(
				"%w: config variant %q, selected variant %q",
				ErrConfigPlatformMismatch,
				config.Variant,
				expected.Variant,
			)
		}
	}
	if config.OSVersion != "" && expected.OSVersion != "" && config.OSVersion != expected.OSVersion {
		return fmt.Errorf(
			"%w: config os.version %q, selected os.version %q",
			ErrConfigPlatformMismatch,
			config.OSVersion,
			expected.OSVersion,
		)
	}
	return nil
}
