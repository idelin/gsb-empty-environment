package netsec

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
)

func layerDiffID(descriptor Descriptor, compressed []byte, maxDecompressed int64) (string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(descriptor.MediaType))
	switch {
	case strings.Contains(mediaType, "zstd"):
		return "", fmt.Errorf("%w: zstd layer %q", ErrUnsupportedMediaType, descriptor.MediaType)
	case strings.Contains(mediaType, "gzip"):
		return gzipDiffID(compressed, maxDecompressed)
	case strings.HasSuffix(mediaType, ".tar"), strings.HasSuffix(mediaType, "+tar"):
		return SHA256Digest(compressed), nil
	case mediaType == "":
		if hasGzipHeader(compressed) {
			return gzipDiffID(compressed, maxDecompressed)
		}
		return SHA256Digest(compressed), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedMediaType, descriptor.MediaType)
	}
}

func hasGzipHeader(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func gzipDiffID(compressed []byte, maxDecompressed int64) (string, error) {
	if !hasGzipHeader(compressed) {
		return "", fmt.Errorf("%w: missing gzip header", ErrInvalidGzipLayer)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidGzipLayer, err)
	}

	hash := sha256.New()
	var source io.Reader = reader
	if maxDecompressed > 0 && maxDecompressed < math.MaxInt64 {
		source = io.LimitReader(reader, maxDecompressed+1)
	}
	written, copyErr := io.Copy(hash, source)
	closeErr := reader.Close()
	if copyErr != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidGzipLayer, copyErr)
	}
	if maxDecompressed > 0 && written > maxDecompressed {
		return "", ErrDecompressedTooLarge
	}
	if closeErr != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidGzipLayer, closeErr)
	}
	return sha256Prefix + hex.EncodeToString(hash.Sum(nil)), nil
}
