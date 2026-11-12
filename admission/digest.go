package admission

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// Digest mirrors build.bazel.remote.execution.v2.Digest: the content hash
// (sha256, 64 lowercase hex characters) and the blob size in bytes.
type Digest struct {
	Hash string
	Size int64
}

// Validate enforces the REAPI digest shape: exactly 64 lowercase hex
// characters and a non-negative size. Note that a valid hex decode alone is
// not enough -- uppercase hex must be rejected so resource names stay
// canonical and cache keys cannot be confused via casing.
func (d Digest) Validate() error {
	if len(d.Hash) != 64 {
		return fail("digest", ErrInvalidDigest, "hash %q is %d chars, want 64", d.Hash, len(d.Hash))
	}
	for i := 0; i < len(d.Hash); i++ {
		c := d.Hash[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return fail("digest", ErrInvalidDigest, "hash %q contains non-lowercase-hex character %q", d.Hash, c)
		}
	}
	if _, err := hex.DecodeString(d.Hash); err != nil {
		return fail("digest", ErrInvalidDigest, "hash %q is not hex: %v", d.Hash, err)
	}
	if d.Size < 0 {
		return fail("digest", ErrInvalidDigest, "size %d must not be negative", d.Size)
	}
	return nil
}

// Resource describes a parsed ByteStream/CAS resource name.
//
// ByteStream write:
//
//	{instance}/blobs/uploads/{uuid}?hash={hash}&size_bytes={n}
//
// CAS v2 blob name (used by ContentAddressableStorage batch operations):
//
//	{instance}/blobs/{hash}/{size}
type Resource struct {
	Instance string
	Digest   Digest
	Upload   bool
}

// ParseResource parses a ByteStream or CAS resource name. It validates both
// the path shape and the embedded digest (64 lowercase hex chars, size >= 0).
func ParseResource(name string) (*Resource, error) {
	const op = "ParseResource"
	if name == "" {
		return nil, fail(op, ErrInvalidDigest, "empty resource name")
	}

	// Strip a leading slash so an absolute gRPC name parses the same way.
	path := name
	for strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	var query string
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path, query = path[:i], path[i+1:]
	}

	parts := strings.Split(path, "/")
	blobs := -1
	for i, p := range parts {
		if p == "blobs" {
			blobs = i
			break
		}
	}
	if blobs < 0 || len(parts) < blobs+2 {
		return nil, fail(op, ErrInvalidDigest, "resource name %q has no blobs segment", name)
	}

	instance := strings.Join(parts[:blobs], "/")
	res := &Resource{Instance: instance}

	switch {
	case parts[blobs+1] == "uploads":
		// {instance}/blobs/uploads/{uuid}?hash=...&size_bytes=...
		res.Upload = true
		values, err := parseQuery(query)
		if err != nil {
			return nil, fail(op, ErrInvalidDigest, "resource name %q: %v", name, err)
		}
		hash := values.Get("hash")
		sizeStr := values.Get("size_bytes")
		if hash == "" || sizeStr == "" {
			return nil, fail(op, ErrInvalidDigest, "upload resource %q must carry hash and size_bytes query params", name)
		}
		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			return nil, fail(op, ErrInvalidDigest, "size_bytes %q is not an integer: %v", sizeStr, err)
		}
		res.Digest = Digest{Hash: hash, Size: size}

	case len(parts) >= blobs+3:
		// {instance}/blobs/{hash}/{size}[/{filename}]
		size, err := strconv.ParseInt(parts[blobs+2], 10, 64)
		if err != nil {
			return nil, fail(op, ErrInvalidDigest, "size segment %q is not an integer: %v", parts[blobs+2], err)
		}
		res.Digest = Digest{Hash: parts[blobs+1], Size: size}

	default:
		return nil, fail(op, ErrInvalidDigest, "resource name %q is missing hash/size segments", name)
	}

	if err := res.Digest.Validate(); err != nil {
		return nil, fail(op, ErrInvalidDigest, "resource name %q: %v", name, err)
	}
	return res, nil
}

// parseQuery is a tiny url.Values replacement that avoids pulling in net/url
// semantics we do not need; it keeps the last value for a repeated key.
type queryValues map[string]string

func (q queryValues) Get(key string) string { return q[key] }

func parseQuery(query string) (queryValues, error) {
	values := queryValues{}
	if query == "" {
		return values, nil
	}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, &simpleError{msg: "malformed query segment: " + pair}
		}
		values[key] = value
	}
	return values, nil
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
