package netsec

import (
	"crypto/subtle"
	"fmt"
	"time"
)

// minRandomBytes is the minimum length of IssuerSignedItem.random mandated by
// ISO/IEC 18013-5 (128 bits).
const minRandomBytes = 16

// VerifyResponse validates a decoded mdoc response against verifier-local
// state and the MSO digests, then checks the device signature.
//
// The checks performed are, in order:
//
//   - callbacks and the verifier SessionTranscript are present;
//   - the response docType equals the MSO docType;
//   - validityInfo.validFrom <= now < validityInfo.validUntil;
//   - every returned element is part of the requested set;
//   - every random value is at least 16 bytes long;
//   - no digestID is repeated within a returned namespace;
//   - every digestID exists in the MSO valueDigests and the recomputed
//     digest of the tagged IssuerSignedItem equals the MSO digest;
//   - the DeviceAuthentication array, binding the verifier's own
//     SessionTranscript, docType and device namespaces, is canonical-encoded
//     and the device signature verifies over it.
//
// Binding the verifier's own SessionTranscript into the signed content makes
// a response relayed from another verifier session fail the device-signature
// check, while the per-item digest checks reject data elements the issuer
// never signed.
func VerifyResponse(resp *Response, opts *Options) error {
	if resp == nil || opts == nil {
		return fmt.Errorf("%w: nil response or options", ErrInvalidResponse)
	}
	if opts.NewHash == nil || opts.VerifyDeviceSignature == nil {
		return ErrMissingCallback
	}
	if len(opts.SessionTranscript) == 0 {
		return ErrSessionTranscript
	}
	if resp.DocType == "" || resp.MSO.DocType == "" || resp.DocType != resp.MSO.DocType {
		return fmt.Errorf("%w: response %q vs MSO %q",
			ErrDocTypeMismatch, resp.DocType, resp.MSO.DocType)
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	validity := resp.MSO.ValidityInfo
	if now.Before(validity.ValidFrom) || !now.Before(validity.ValidUntil) {
		return fmt.Errorf("%w: now %s not within [%s, %s)",
			ErrExpired, now.UTC().Format(time.RFC3339),
			validity.ValidFrom.UTC().Format(time.RFC3339),
			validity.ValidUntil.UTC().Format(time.RFC3339))
	}

	if err := verifyIssuerElements(resp, opts); err != nil {
		return err
	}

	return verifyDeviceSigned(resp, opts)
}

// verifyIssuerElements enforces the requested-subset, random-length,
// duplicate-digestID and MSO digest rules for every returned data element.
func verifyIssuerElements(resp *Response, opts *Options) error {
	requested := make(map[string]struct{}, len(opts.Requested))
	for _, item := range opts.Requested {
		requested[requestKey(item.NameSpace, item.ElementIdentifier)] = struct{}{}
	}

	msoDigests := resp.MSO.ValueDigests
	for nameSpace, items := range resp.IssuerSigned.NameSpaces {
		seenDigestIDs := make(map[uint64]struct{}, len(items))
		namespaceDigests, namespaceKnown := msoDigests[nameSpace]

		for _, item := range items {
			if len(item.Random) < minRandomBytes {
				return fmt.Errorf("%w: namespace %q element %q has %d random bytes",
					ErrShortRandom, nameSpace, item.ElementIdentifier, len(item.Random))
			}
			if _, dup := seenDigestIDs[item.DigestID]; dup {
				return fmt.Errorf("%w: digestID %d repeated in namespace %q",
					ErrDuplicateDigestID, item.DigestID, nameSpace)
			}
			seenDigestIDs[item.DigestID] = struct{}{}

			if _, ok := requested[requestKey(nameSpace, item.ElementIdentifier)]; !ok {
				return fmt.Errorf("%w: namespace %q element %q",
					ErrNotRequested, nameSpace, item.ElementIdentifier)
			}

			if !namespaceKnown {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestNotFound, nameSpace, item.DigestID)
			}
			expected, ok := namespaceDigests[item.DigestID]
			if !ok {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestNotFound, nameSpace, item.DigestID)
			}

			actual, err := recomputeDigest(opts.NewHash, resp.MSO.DigestAlgorithm, item)
			if err != nil {
				return fmt.Errorf("netsec: digest computation failed: %w", err)
			}
			if len(actual) != len(expected) ||
				subtle.ConstantTimeCompare(actual, expected) != 1 {
				return fmt.Errorf("%w: namespace %q digestID %d",
					ErrDigestMismatch, nameSpace, item.DigestID)
			}
		}
	}
	return nil
}

// recomputeDigest hashes the tag-24 encoded IssuerSignedItem with the MSO
// digest algorithm supplied through the Hasher callback.
func recomputeDigest(newHash Hasher, algorithm string, item IssuerSignedItem) ([]byte, error) {
	hasher, err := newHash(algorithm)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeIssuerSignedItem(item)
	if err != nil {
		return nil, err
	}
	if _, err := hasher.Write(encoded); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

// verifyDeviceSigned rebuilds the DeviceAuthentication structure from the
// verifier's own SessionTranscript and verifies the device signature.
func verifyDeviceSigned(resp *Response, opts *Options) error {
	var deviceNameSpacesBytes []byte
	switch {
	case resp.DeviceSigned.NameSpacesRaw != nil:
		deviceNameSpacesBytes = resp.DeviceSigned.NameSpacesRaw
	case resp.DeviceSigned.NameSpaces != nil:
		encoded, err := encodeDeviceNameSpaces(resp.DeviceSigned.NameSpaces)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDeviceSignature, err)
		}
		deviceNameSpacesBytes = encoded
	default:
		// No device namespaces: the DeviceAuthentication array carries null.
	}

	deviceAuthentication, err := encodeDeviceAuthentication(
		opts.SessionTranscript, resp.DocType, deviceNameSpacesBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDeviceSignature, err)
	}

	if err := opts.VerifyDeviceSignature(
		resp.MSO.DeviceKey,
		resp.DeviceSigned.DeviceAuth.DeviceAuthCOSE,
		deviceAuthentication,
	); err != nil {
		return fmt.Errorf("%w: %v", ErrDeviceSignature, err)
	}
	return nil
}

func requestKey(nameSpace, elementIdentifier string) string {
	return nameSpace + "\x00" + elementIdentifier
}
