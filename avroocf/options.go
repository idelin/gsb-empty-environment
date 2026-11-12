package avroocf

// FingerprintFunc computes an allowlist key from a writer schema.
type FingerprintFunc func(schema *Schema) string

// Config bounds every resource an untrusted container can consume.
// Zero-valued numeric fields are replaced by safe defaults in Open.
type Config struct {
	// AllowedFingerprints contains schema fingerprints accepted by the
	// ingestion pipeline. A container whose writer schema is absent is
	// rejected before any block is decoded.
	AllowedFingerprints map[string]bool

	// Fingerprint computes the allowlist key. Defaults to
	// Schema.FingerprintSHA256 (lowercase hex).
	Fingerprint FingerprintFunc

	// MaxSchemaDepth bounds textual nesting of the writer schema and the
	// runtime nesting depth of decoded values.
	MaxSchemaDepth int

	// MaxBlockObjects bounds the absolute value of a per-block object count.
	MaxBlockObjects int64

	// MaxBlockBytes bounds the declared size of a compressed/raw block and
	// the bytes a single block may consume in the stream.
	MaxBlockBytes int64

	// MaxSchemaBytes bounds the raw header schema JSON.
	MaxSchemaBytes int64

	// AllowDeflate permits the "deflate" codec. The null codec is always
	// allowed; any other codec is rejected.
	AllowDeflate bool

	// MaxDecompressedBlockBytes bounds the output of a deflate block. It is
	// also the gate that enables deflate when positive together with
	// AllowDeflate.
	MaxDecompressedBlockBytes int64
}

func (c Config) withDefaults() Config {
	if c.MaxSchemaDepth <= 0 {
		c.MaxSchemaDepth = 64
	}
	if c.MaxBlockObjects <= 0 {
		c.MaxBlockObjects = 100_000
	}
	if c.MaxBlockBytes <= 0 {
		c.MaxBlockBytes = 16 << 20
	}
	if c.MaxSchemaBytes <= 0 {
		c.MaxSchemaBytes = 1 << 20
	}
	if c.Fingerprint == nil {
		c.Fingerprint = func(schema *Schema) string {
			return schema.FingerprintSHA256()
		}
	}
	if c.AllowDeflate && c.MaxDecompressedBlockBytes <= 0 {
		c.MaxDecompressedBlockBytes = c.MaxBlockBytes * 4
	}
	return c
}
