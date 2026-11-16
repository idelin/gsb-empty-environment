package mdoc

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors returned by Verify. Wrapped errors are matched with
// errors.Is, so additional context may be attached without losing the
// classification.
var (
	// ErrEmptyResponse is returned when no document data was supplied.
	ErrEmptyResponse = errors.New("mdoc: empty response")
	// ErrDocTypeMismatch is returned when the response DocType differs from
	// the DocType signed inside the MSO.
	ErrDocTypeMismatch = errors.New("mdoc: response DocType does not match MSO DocType")
	// ErrExpired is returned when the current instant is outside the MSO
	// validity window.
	ErrExpired = errors.New("mdoc: MSO validity window does not contain current time")
	// ErrUnsupportedDigestAlgorithm is returned for an MSO digestAlgorithm
	// value the verifier cannot service.
	ErrUnsupportedDigestAlgorithm = errors.New("mdoc: unsupported MSO digest algorithm")
	// ErrHashCallback is returned when the hash callback fails.
	ErrHashCallback = errors.New("mdoc: hash callback failed")
	// ErrInvalidSessionTranscript is returned for missing or malformed
	// SessionTranscript bytes.
	ErrInvalidSessionTranscript = errors.New("mdoc: invalid SessionTranscript")
	// ErrShortRandom is returned when an issuer random value is shorter than
	// 16 bytes.
	ErrShortRandom = errors.New("mdoc: issuer random must be at least 16 bytes")
	// ErrDuplicateDigestID is returned when one namespace contains two items
	// with the same digestID.
	ErrDuplicateDigestID = errors.New("mdoc: duplicate digestID within namespace")
	// ErrNotRequested is returned when a returned element was not in the
	// verifier's request.
	ErrNotRequested = errors.New("mdoc: returned element is not part of the request")
	// ErrDigestNotFound is returned when the MSO contains no digest for a
	// returned item's digestID.
	ErrDigestNotFound = errors.New("mdoc: digestID not found in MSO valueDigests")
	// ErrDigestMismatch is returned when the recomputed item digest differs
	// from the digest signed inside the MSO.
	ErrDigestMismatch = errors.New("mdoc: recomputed digest does not match MSO digest")
	// ErrDeviceAuthentication is returned when the device signature callback
	// rejects the DeviceAuthentication payload.
	ErrDeviceAuthentication = errors.New("mdoc: device authentication failed")
	// ErrEncoding is returned when canonical re-encoding fails.
	ErrEncoding = errors.New("mdoc: canonical CBOR encoding failed")
)

// HashFunc hashes data using the algorithm named by the MSO. The algorithm
// string is the MSO digestAlgorithm identifier (for example "SHA-256").
type HashFunc func(algorithm string, data []byte) ([]byte, error)

// DeviceSignatureVerifier checks the COSE_Sign1 over the DeviceAuthentication
// bytes that the package reconstructs. Implementations verify signature and
// COSE headers (for example against the MSO deviceKey) and return nil only
// when the signature is valid.
type DeviceSignatureVerifier func(authBytes []byte, sig DeviceSignature) error

// Verifier carries the local, verifier-specific inputs and callbacks.
type Verifier struct {
	// Hash is invoked for every returned issuer item and must implement the
	// MSO digest algorithm.
	Hash HashFunc
	// VerifyDeviceSignature authenticates the reconstructed
	// DeviceAuthentication payload.
	VerifyDeviceSignature DeviceSignatureVerifier
	// Now returns the instant to compare against the MSO validity window.
	// Defaults to time.Now in UTC when nil.
	Now func() time.Time
}

// Verify checks a decoded mdoc response against the verifier's own session
// transcript and request set.
//
// It enforces, in order:
//   - response and MSO DocType agreement;
//   - current-time containment within [validFrom, validUntil];
//   - well-formed, locally-owned SessionTranscript;
//   - per-item 16-byte minimum random values and unique digestIDs;
//   - every returned element being part of the request;
//   - every returned item matching an MSO valueDigest by digestID;
//   - the device signature over the canonical DeviceAuthentication array
//     embedding the verifier's own SessionTranscript.
func (v Verifier) Verify(response *Response, sessionTranscript []byte, requested RequestedItems) error {
	if response == nil {
		return ErrEmptyResponse
	}
	if v.Hash == nil || v.VerifyDeviceSignature == nil {
		return errors.New("mdoc: verifier callbacks are not configured")
	}

	if response.DocType != response.MSO.DocType {
		return fmt.Errorf("%w: response %q, MSO %q",
			ErrDocTypeMismatch, response.DocType, response.MSO.DocType)
	}

	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now()
	}
	validity := response.MSO.ValidityInfo
	if now.Before(validity.ValidFrom) || now.After(validity.ValidUntil) {
		return fmt.Errorf("%w: now %s not within [%s, %s]",
			ErrExpired, now.UTC().Format(time.RFC3339),
			validity.ValidFrom.UTC().Format(time.RFC3339),
			validity.ValidUntil.UTC().Format(time.RFC3339))
	}

	if response.MSO.DigestAlgorithm == "" {
		return fmt.Errorf("%w: empty algorithm identifier", ErrUnsupportedDigestAlgorithm)
	}

	if len(sessionTranscript) == 0 || !ValidCBOR(sessionTranscript) {
		return ErrInvalidSessionTranscript
	}

	if err := verifyIssuerItems(v, response, requested); err != nil {
		return err
	}
	return verifyDeviceAuthentication(v, response, sessionTranscript)
}

func verifyIssuerItems(v Verifier, response *Response, requested RequestedItems) error {
	namespaces := sortedNamespaceNames(response.NameSpaces)
	for _, namespace := range namespaces {
		items := response.NameSpaces[namespace]
		seen := make(map[DigestID]struct{}, len(items))
		digests, namespaceKnown := response.MSO.ValueDigests[namespace]

		for _, item := range items {
			if len(item.Random) < 16 {
				return fmt.Errorf("%w: namespace %q digestID %d has %d-byte random",
					ErrShortRandom, namespace, item.DigestID, len(item.Random))
			}
			if _, dup := seen[item.DigestID]; dup {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDuplicateDigestID, namespace, item.DigestID)
			}
			seen[item.DigestID] = struct{}{}

			if !requested.Contains(namespace, item.ElementIdentifier) {
				return fmt.Errorf("%w: namespace %q element %q",
					ErrNotRequested, namespace, item.ElementIdentifier)
			}

			if !namespaceKnown {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestNotFound, namespace, item.DigestID)
			}
			expected, ok := digests[item.DigestID]
			if !ok || len(expected) == 0 {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestNotFound, namespace, item.DigestID)
			}

			actual, err := issuerItemDigest(v.Hash, response.MSO.DigestAlgorithm, item)
			if err != nil {
				return err
			}
			if len(actual) != len(expected) ||
				subtle.ConstantTimeCompare(actual, expected) != 1 {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestMismatch, namespace, item.DigestID)
			}
		}
	}
	return nil
}

// issuerItemDigest rebuilds the tagged IssuerSignedItemBytes encoding and
// hashes it. Per 18013-5 the MSO value digest is computed over the full
// CBOR tag (tag 24) wrapping the bstr that carries the IssuerSignedItem.
func issuerItemDigest(hash HashFunc, algorithm string, item IssuerSignedItem) ([]byte, error) {
	plain, err := Encode([]any{
		"digestID",
		item.DigestID,
		"random",
		item.Random,
		"elementIdentifier",
		item.ElementIdentifier,
		"elementValue",
		item.ElementValue,
	})
	if err != nil {
		return nil, err
	}
	tagged, err := Encode(Tag{ID: 24, Content: plain})
	if err != nil {
		return nil, err
	}
	digest, err := hash(algorithm, tagged)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHashCallback, err)
	}
	return digest, nil
}

// verifyDeviceAuthentication reconstructs
//
//	[
//	  "DeviceAuthentication",
//	  SessionTranscript,
//	  DocType,
//	  #6.24(bstr .cbor DeviceNameSpaces)
//	]
//
// and hands the canonical encoding plus the COSE_Sign1 to the verifier
// callback. The SessionTranscript is embedded verbatim from the verifier's
// own bytes, so a response relayed from another session cannot authenticate
// against this session.
func verifyDeviceAuthentication(v Verifier, response *Response, sessionTranscript []byte) error {
	nameSpacesPlain, err := encodeDeviceNameSpaces(response.DeviceNameSpaces)
	if err != nil {
		return err
	}
	nameSpacesTagged, err := Encode(Tag{ID: 24, Content: nameSpacesPlain})
	if err != nil {
		return err
	}
	authBytes, err := Encode([]any{
		"DeviceAuthentication",
		RawCBOR(sessionTranscript),
		response.DocType,
		RawCBOR(nameSpacesTagged),
	})
	if err != nil {
		return err
	}
	if err := v.VerifyDeviceSignature(authBytes, response.DeviceSignature); err != nil {
		return fmt.Errorf("%w: %v", ErrDeviceAuthentication, err)
	}
	return nil
}

func encodeDeviceNameSpaces(nameSpaces map[string][]DeviceSignedItem) ([]byte, error) {
	generic := make(map[string]any, len(nameSpaces))
	namespaceNames := make([]string, 0, len(nameSpaces))
	for namespace := range nameSpaces {
		namespaceNames = append(namespaceNames, namespace)
	}
	sortStrings(namespaceNames)
	for _, namespace := range namespaceNames {
		items := nameSpaces[namespace]
		encodedItems := make([]any, 0, len(items))
		for _, item := range items {
			encodedItems = append(encodedItems, map[string]any{
				"elementIdentifier": item.ElementIdentifier,
				"elementValue":      item.ElementValue,
			})
		}
		generic[namespace] = encodedItems
	}
	return Encode(generic)
}

func sortedNamespaceNames(nameSpaces map[string][]IssuerSignedItem) []string {
	names := make([]string, 0, len(nameSpaces))
	for namespace := range nameSpaces {
		names = append(names, namespace)
	}
	sortStrings(names)
	return names
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
