package depverify

import (
	"context"
	"io"
)

// Hop records one HTTP-level redirect followed while fetching module content.
// Every hop must use an allowed protocol; a single downgrade (for example
// https:// -> http://) fails verification.
type Hop struct {
	From string
	To   string
}

// Source describes where module content was actually fetched from, including
// the full redirect chain. Protocol checks cover the initial URL and every hop.
type Source struct {
	// URL is the location the fetcher ultimately read content from.
	URL string

	// Protocol is the effective scheme of the final content location, for
	// example "https". Local directory replacements use "file".
	Protocol string

	// Redirects lists all redirects, in order, starting from the requested
	// proxy URL. An empty slice means no redirect occurred.
	Redirects []Hop

	// LocalDir is set for an allowed local replacement and holds the
	// absolute, allow-listed directory that was hashed.
	LocalDir string
}

// Content is the result of fetching one module version. Zip is the module
// archive as served by a proxy; GoMod is the module's go.mod contents.
// Zip must remain readable for the lifetime of the call and is closed by the
// verifier after hashing.
type Content struct {
	Zip     io.ReadCloser
	ZipSize int64
	GoMod   []byte
	Source  Source
}

// Fetcher retrieves module content. A production build service implements this
// over its module proxy client; tests use an in-memory fake. The fetcher must
// report the complete redirect chain so downgrade redirects can be detected.
type Fetcher interface {
	Fetch(ctx context.Context, modulePath, version string) (*Content, error)
}

// LocalReplacement explicitly registers a permitted filesystem replacement,
// keyed by both the module path and the exact version named on the left hand
// side of the replace directive.
type LocalReplacement struct {
	// Path is the replaced module path (left hand side of replace).
	Path string

	// Version is the exact version on the left hand side of the replace.
	Version string

	// Dir is the absolute directory containing the replacement source.
	Dir string
}

// VerifiedModule is the binding produced for every effective (verified) module.
type VerifiedModule struct {
	// Path and Version are the module path and version as named by require.
	Path    string
	Version string

	// EffectivePath and EffectiveVersion are what is actually fetched after
	// applying replace. They equal Path/Version when no replace applies.
	EffectivePath    string
	EffectiveVersion string

	// Digest is the h1: hash computed over the actual fetched content.
	Digest string

	// GoModDigest is the verified h1: hash of the module go.mod when go.sum
	// contains an entry for it.
	GoModDigest string

	// Source records where the content came from, including redirects.
	Source Source

	// Replaced is true when a replace directive was applied.
	Replaced bool

	// Local is true when the content is an allow-listed local directory.
	Local bool
}
