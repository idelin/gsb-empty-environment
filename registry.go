package netsec

import (
	"compress/bzip2"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
)

func newHasher(algo HashAlgo) (hash.Hash, error) {
	switch algo {
	case HashSHA224:
		return sha512.New512_224(), nil
	case HashSHA256:
		return sha256.New(), nil
	case HashSHA384:
		return sha512.New384(), nil
	case HashSHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownHash, algo)
	}
}

func buildRegistry(override map[Compression]Decompressor) (map[Compression]Decompressor, error) {
	registry := map[Compression]Decompressor{
		CompressNone: decompressNone,
		CompressGzip: decompressGzip,
		CompressZlib: decompressZlib,
		CompressBzip: decompressBzip2,
	}
	for algo, fn := range override {
		if fn == nil {
			return nil, fmt.Errorf("%w: nil decompressor registered for %q", ErrUnknownCompression, algo)
		}
		registry[algo] = fn
	}
	return registry, nil
}

func decompressNone(reader io.Reader) (io.ReadCloser, error) {
	return io.NopCloser(reader), nil
}

func decompressGzip(reader io.Reader) (io.ReadCloser, error) {
	gr, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return gr, nil
}

func decompressZlib(reader io.Reader) (io.ReadCloser, error) {
	zr, err := zlib.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return zr, nil
}

// bzip2's standard-library reader has no Close, so wrap it.
func decompressBzip2(reader io.Reader) (io.ReadCloser, error) {
	return io.NopCloser(bzip2.NewReader(reader)), nil
}

// limitedWriter caps how many plaintext bytes one partition can stage. Write
// fails once the declared destination length is exceeded, which turns a
// decompression bomb or an over-long stream into a package rejection rather
// than unbounded disk use.
type limitedWriter struct {
	remaining int64
	dst       io.Writer
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("%w: stream exceeds declared length", ErrLengthMismatch)
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	return n, err
}
