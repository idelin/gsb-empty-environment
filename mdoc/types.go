// Package mdoc verifies ISO/IEC 18013-5 mobile document (mdoc) responses on
// the verifier side.
//
// The package works on already-decoded CBOR structures: the caller parses the
// raw response and supplies IssuerSigned, DeviceSigned, its own
// SessionTranscript bytes and the set of requested data elements. Hashing and
// signature verification are delegated to callbacks so that the package has no
// dependency on a particular crypto or CBOR library.
package mdoc

import "time"

// Digest identifiers are assigned per namespace by the issuer when building
// the Mobile Security Object (MSO).
type DigestID = uint64

// Data element identifiers are text strings such as "family_name".
type ElementIdentifier = string

// RequestedItems is the set of data elements that the verifier asked for,
// keyed first by namespace and then by element identifier.
type RequestedItems map[string]map[ElementIdentifier]struct{}

// NewRequestedItems builds a RequestedItems value from namespace/element
// pairs.
func NewRequestedItems(items ...[2]string) RequestedItems {
	requested := RequestedItems{}
	requested.Add(items...)
	return requested
}

// Add registers one or more namespace/element pairs as requested.
func (r RequestedItems) Add(items ...[2]string) {
	for _, item := range items {
		elements, ok := r[item[0]]
		if !ok {
			elements = map[ElementIdentifier]struct{}{}
			r[item[0]] = elements
		}
		elements[item[1]] = struct{}{}
	}
}

// Contains reports whether the element was part of the verifier's request.
func (r RequestedItems) Contains(namespace string, element ElementIdentifier) bool {
	elements, ok := r[namespace]
	if !ok {
		return false
	}
	_, ok = elements[element]
	return ok
}

// ValidityInfo mirrors the MSO validityInfo structure.
type ValidityInfo struct {
	Signed     time.Time
	ValidFrom  time.Time
	ValidUntil time.Time
}

// MSO is the decoded Mobile Security Object. Only the fields needed for
// response verification are modelled.
type MSO struct {
	// DigestAlgorithm is the MSO digestAlgorithm identifier, for example
	// "SHA-256", "SHA-384" or "SHA-512".
	DigestAlgorithm string
	// ValueDigests maps namespace -> digestID -> issuer-computed digest of the
	// tagged IssuerSignedItemBytes encoding.
	ValueDigests map[string]map[DigestID][]byte
	// ValidityInfo carries the issuer-declared validity window.
	ValidityInfo ValidityInfo
	// DocType is the document type signed inside the MSO.
	DocType string
}

// IssuerSignedItem is one decoded IssuerSignedItem entry.
type IssuerSignedItem struct {
	DigestID          DigestID
	Random            []byte
	ElementIdentifier ElementIdentifier
	ElementValue      any
}

// IssuerSigned is the decoded issuer-signed portion of the response.
type IssuerSigned struct {
	// MSO is the decoded Mobile Security Object.
	MSO MSO
	// NameSpaces maps namespace -> ordered list of returned data elements.
	NameSpaces map[string][]IssuerSignedItem
}

// DeviceSignedItem is one entry inside a DeviceNameSpaces namespace.
type DeviceSignedItem struct {
	ElementIdentifier ElementIdentifier
	ElementValue      any
}

// DeviceSigned is the decoded device-signed portion of the response.
type DeviceSigned struct {
	// DeviceNameSpaces maps namespace -> ordered list of device-provided data
	// elements.
	DeviceNameSpaces map[string][]DeviceSignedItem
	// DeviceSignature is the decoded COSE_Sign1 structure authenticating the
	// DeviceAuthentication payload.
	DeviceSignature DeviceSignature
}

// DeviceSignature is a decoded COSE_Sign1. Protected is the original
// protected-header bstr (including its empty-bstr encoding when no
// parameters are present); Unprotected carries decoded header label/value
// pairs; Payload is normally nil for a detached signature; Signature is the
// raw signature byte string.
type DeviceSignature struct {
	Protected   []byte
	Unprotected map[int64]any
	Payload     []byte
	Signature   []byte
}

// Response is a fully decoded mdoc response handed to the verifier.
type Response struct {
	// DocType is the document type carried by the response structure.
	DocType string
	IssuerSigned
	DeviceSigned
}
