package netsec

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// decompressedDiffID streams a layer blob through the right decompressor and
// returns the sha256 of its uncompressed tar content.
func decompressedDiffID(blob []byte, mediaType string) (Digest, error) {
	reader, err := openLayer(bytes.NewReader(blob), mediaType)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecompress, err)
	}
	if closer, ok := reader.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return "", fmt.Errorf("%w: %v", ErrDecompress, err)
		}
	}
	return Digest("sha256:" + hex.EncodeToString(hasher.Sum(nil))), nil
}

// openLayer returns a reader producing uncompressed tar bytes for one layer.
// Gzip and uncompressed tar are supported; unknown media types fall back to
// gzip magic-byte detection so descriptors with a missing or loose mediaType
// still get verified rather than silently trusted.
func openLayer(r io.Reader, mediaType string) (io.Reader, error) {
	switch {
	case isGzipMediaType(mediaType):
		gzipReader, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDecompress, err)
		}
		return gzipReader, nil
	case isTarMediaType(mediaType):
		return r, nil
	case isZstdMediaType(mediaType):
		return nil, fmt.Errorf("%w: zstd layer %q", ErrUnsupportedLayer, mediaType)
	default:
		// No trustworthy media type: decide from the gzip magic bytes.
		header := make([]byte, 2)
		n, _ := io.ReadFull(r, header)
		stream := io.MultiReader(bytes.NewReader(header[:n]), r)
		if n == 2 && header[0] == 0x1f && header[1] == 0x8b {
			gzipReader, err := gzip.NewReader(stream)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrDecompress, err)
			}
			return gzipReader, nil
		}
		return stream, nil
	}
}

func isGzipMediaType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.layer.v1.tar+gzip",
		"application/vnd.docker.image.rootfs.diff.tar.gzip":
		return true
	}
	return false
}

func isTarMediaType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.oci.image.layer.v1.tar",
		"application/vnd.docker.image.rootfs.diff.tar":
		return true
	}
	return false
}

func isZstdMediaType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.layer.v1.tar+zstd"
}

// Verify is the one-call admission-time check. It enforces, in order:
//
//  1. the in-toto subject set contains the sha256 digest of the raw
//     manifest being deployed;
//  2. the config blob matches its manifest descriptor and parses;
//  3. the number of layers equals the number of rootfs diff_ids;
//  4. every compressed layer blob matches its manifest descriptor digest and
//     declared size;
//  5. every decompressed layer's sha256 equals the diff_id at the same
//     position.
//
// The statement is checked before any blob content so a replayed attestation
// for an unrelated image is rejected without trusting the image at all.
func Verify(rawManifest []byte, stmt *Statement, configBlob []byte, layerBlobs [][]byte) error {
	manifest, err := ParseManifest(rawManifest)
	if err != nil {
		return err
	}
	if err := VerifyManifestSubject(rawManifest, stmt); err != nil {
		return err
	}
	imageConfig, err := VerifyConfigBlob(configBlob, manifest.Config)
	if err != nil {
		return err
	}
	return VerifyLayers(layerBlobs, manifest, imageConfig)
}

// VerifyWithBlobs is Verify for callers that store blobs behind a registry-
// like interface. The config and each layer are fetched by their manifest
// digests; a fetch failure is reported as ErrBlobFetch.
func VerifyWithBlobs(ctx context.Context, rawManifest []byte, stmt *Statement, getter BlobGetter) error {
	manifest, err := ParseManifest(rawManifest)
	if err != nil {
		return err
	}
	if err := VerifyManifestSubject(rawManifest, stmt); err != nil {
		return err
	}

	configBlob, err := getBlob(ctx, getter, manifest.Config.Digest)
	if err != nil {
		return err
	}
	imageConfig, err := VerifyConfigBlob(configBlob, manifest.Config)
	if err != nil {
		return err
	}

	layerBlobs := make([][]byte, len(manifest.Layers))
	for i, descriptor := range manifest.Layers {
		layerBlobs[i], err = getBlob(ctx, getter, descriptor.Digest)
		if err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
	}
	return VerifyLayers(layerBlobs, manifest, imageConfig)
}

func getBlob(ctx context.Context, getter BlobGetter, digest Digest) ([]byte, error) {
	blob, err := getter.GetBlob(ctx, digest)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBlobFetch, digest, err)
	}
	return blob, nil
}
