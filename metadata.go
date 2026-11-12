package netsec

import (
	"encoding/base64"
	"errors"
	"strings"

	"google.golang.org/grpc/metadata"
)

// Metadata is the gRPC metadata representation used by the sanitizer.
type Metadata = metadata.MD

const (
	// UserIDKey, UserRolesKey and TenantIDKey are the identity headers owned by
	// the gateway. Any client-supplied value for these keys is discarded.
	UserIDKey    = "x-user-id"
	UserRolesKey = "x-user-roles"
	TenantIDKey  = "x-tenant-id"

	// InternalPrefix covers gateway-only internal headers.
	InternalPrefix = "x-internal-"

	// MaxMetadataBytes is the maximum accepted size of the forwarded metadata.
	MaxMetadataBytes = 8192

	// MaxBinaryValueBytes is the maximum decoded size of a "-bin" value.
	MaxBinaryValueBytes = 4096
)

// ErrMetadataTooLarge is returned when metadata exceeds MaxMetadataBytes.
var ErrMetadataTooLarge = errors.New("netsec: gRPC metadata exceeds 8192 bytes")

// Identity contains the values established by successful authentication.
//
// Roles is encoded as one comma-separated x-user-roles value so that the
// identity key never carries a client-supplied or duplicate value.
type Identity struct {
	UserID   string
	Roles    []string
	TenantID string
}

// SanitizeMetadata removes client-controlled identity, internal and reserved
// metadata. Keys are compared case-insensitively and emitted in gRPC's
// canonical lower-case form.
//
// Invalid or oversized "-bin" values are discarded. Metadata over
// MaxMetadataBytes is rejected.
func SanitizeMetadata(md Metadata) (Metadata, error) {
	if MetadataSize(md) > MaxMetadataBytes {
		return nil, ErrMetadataTooLarge
	}

	out := make(Metadata, len(md))
	for key, values := range md {
		lowerKey := strings.ToLower(key)
		if discardKey(lowerKey) {
			continue
		}

		cleanValues := values
		if strings.HasSuffix(lowerKey, "-bin") {
			cleanValues = validBinaryValues(values)
		}
		if len(cleanValues) == 0 {
			continue
		}

		out[lowerKey] = append(out[lowerKey], cleanValues...)
	}

	if MetadataSize(out) > MaxMetadataBytes {
		return nil, ErrMetadataTooLarge
	}
	return out, nil
}

// SanitizeIncomingMetadata is an alias for SanitizeMetadata.
func SanitizeIncomingMetadata(md Metadata) (Metadata, error) {
	return SanitizeMetadata(md)
}

// Sanitize is an alias for SanitizeMetadata.
func Sanitize(md Metadata) (Metadata, error) {
	return SanitizeMetadata(md)
}

// ApplyIdentity sanitizes md, removes every existing identity-key variant and
// writes the authenticated identity. Each identity key receives exactly one
// value. Applying it to already-sanitized metadata is safe and idempotent.
func ApplyIdentity(md Metadata, identity Identity) (Metadata, error) {
	clean, err := SanitizeMetadata(md)
	if err != nil {
		return nil, err
	}
	out := make(Metadata, len(clean)+3)
	for key, values := range clean {
		out[key] = append([]string(nil), values...)
	}

	if identity.UserID != "" {
		out[UserIDKey] = []string{identity.UserID}
	}
	if len(identity.Roles) > 0 {
		out[UserRolesKey] = []string{strings.Join(identity.Roles, ",")}
	}
	if identity.TenantID != "" {
		out[TenantIDKey] = []string{identity.TenantID}
	}

	if MetadataSize(out) > MaxMetadataBytes {
		return nil, ErrMetadataTooLarge
	}
	return out, nil
}

// InjectIdentity is an alias for ApplyIdentity.
func InjectIdentity(md Metadata, identity Identity) (Metadata, error) {
	return ApplyIdentity(md, identity)
}

// SanitizeAndApplyIdentity is an alias for ApplyIdentity.
func SanitizeAndApplyIdentity(md Metadata, identity Identity) (Metadata, error) {
	return ApplyIdentity(md, identity)
}

// MetadataSize returns the forwarded byte size, counting each key once and
// every value's bytes.
func MetadataSize(md Metadata) int {
	size := 0
	for key, values := range md {
		size += len(key)
		for _, value := range values {
			size += len(value)
		}
	}
	return size
}

func discardKey(key string) bool {
	return strings.HasPrefix(key, ":") ||
		key == "te" ||
		key == "content-type" ||
		strings.HasPrefix(key, "grpc-") ||
		strings.HasPrefix(key, InternalPrefix) ||
		isIdentityKey(key)
}

func isIdentityKey(key string) bool {
	return key == UserIDKey || key == UserRolesKey || key == TenantIDKey
}

func validBinaryValues(values []string) []string {
	valid := make([]string, 0, len(values))
	for _, value := range values {
		decoded, ok := decodeBinaryValue(value)
		if !ok || len(decoded) > MaxBinaryValueBytes {
			continue
		}
		valid = append(valid, value)
	}
	return valid
}

func decodeBinaryValue(value string) ([]byte, bool) {
	if value == "" {
		return []byte{}, true
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return nil, false
	}

	if strings.HasSuffix(value, "=") {
		if len(value)%4 != 0 {
			return nil, false
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		return decoded, err == nil
	}
	if len(value)%4 == 1 {
		return nil, false
	}

	decoded, err := base64.RawStdEncoding.DecodeString(value)
	return decoded, err == nil
}
