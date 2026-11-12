package zdebug

import (
	"compress/zlib"
	"io"
)

// defaultFactories returns the decoders that need no external dependency.
// MethodZstd is intentionally absent: callers with a zstd dependency inject
// its factory through Config.Factories.
func defaultFactories() map[Method]DecoderFactory {
	return map[Method]DecoderFactory{
		MethodNone: identityFactory,
		MethodZlib: zlibFactory,
	}
}

func identityFactory(r io.Reader) (Decoder, error) {
	return &identityDecoder{src: r}, nil
}

type identityDecoder struct {
	src io.Reader
}

func (d *identityDecoder) Read(p []byte) (int, error) { return d.src.Read(p) }

func (d *identityDecoder) Close() error { return nil }

func zlibFactory(r io.Reader) (Decoder, error) {
	// zlib.NewReader reads and validates the header immediately and
	// surfaces checksum failures through Read/Close on the full stream.
	return zlib.NewReader(r)
}
