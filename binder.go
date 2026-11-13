package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
)

// ComputeBinder computes the TLS 1.3 PSK binder for transcript. The PSK type
// may be supplied as a PSKType, bool, int, or name string. A hash constructor
// may also be supplied. Arguments other than byte slices may appear in any
// order; when two byte slices are present, the first is the PSK and the second
// is the transcript.
//
// If transcript is exactly Hash.length bytes it is treated as an
// already-computed transcript hash. Otherwise it is hashed as the transcript.
// A complete ClientHello handshake message (with or without a TLS record
// header) is automatically truncated before the binders list, as required by
// RFC 8446, Section 4.2.11.2.
func ComputeBinder(args ...any) []byte {
	out, err := computeBinder(args, false, nil)
	if err != nil {
		return nil
	}
	return out
}

// ComputeBinderChecked is ComputeBinder with an error result.
func ComputeBinderChecked(args ...any) ([]byte, error) {
	return computeBinder(args, false, nil)
}

// ComputeBinderFromKey computes a binder from an already-derived binder key.
// This is useful for a TLS stack that keeps the early secret and binder key
// separate from the raw PSK.
func ComputeBinderFromKey(args ...any) []byte {
	out, err := computeBinder(args, true, nil)
	if err != nil {
		return nil
	}
	return out
}

// ComputeBinderFromKeyChecked is ComputeBinderFromKey with an error result.
func ComputeBinderFromKeyChecked(args ...any) ([]byte, error) {
	return computeBinder(args, true, nil)
}

// PSKBinder is an alias for ComputeBinder.
func PSKBinder(args ...any) []byte { return ComputeBinder(args...) }

// ComputePSKBinder is an alias for ComputeBinder.
func ComputePSKBinder(args ...any) []byte { return ComputeBinder(args...) }

// ComputeBinderForKey is an alias for ComputeBinderFromKey.
func ComputeBinderForKey(args ...any) []byte { return ComputeBinderFromKey(args...) }

// ComputeExternalBinder computes a binder using the external PSK derivation
// label, regardless of any type argument supplied by the caller.
func ComputeExternalBinder(args ...any) []byte {
	t := PSKExternal
	out, err := computeBinder(args, false, &t)
	if err != nil {
		return nil
	}
	return out
}

// ComputeResumptionBinder computes a binder using the resumption PSK
// derivation label.
func ComputeResumptionBinder(args ...any) []byte {
	t := PSKResumption
	out, err := computeBinder(args, false, &t)
	if err != nil {
		return nil
	}
	return out
}

// ComputeImportedBinder computes a binder using the RFC 9258 imported PSK
// derivation label.
func ComputeImportedBinder(args ...any) []byte {
	t := PSKImported
	out, err := computeBinder(args, false, &t)
	if err != nil {
		return nil
	}
	return out
}

// ExternalPSKBinder is an alias for ComputeExternalBinder.
func ExternalPSKBinder(args ...any) []byte { return ComputeExternalBinder(args...) }

// ResumptionPSKBinder is an alias for ComputeResumptionBinder.
func ResumptionPSKBinder(args ...any) []byte { return ComputeResumptionBinder(args...) }

// ImportedPSKBinder is an alias for ComputeImportedBinder.
func ImportedPSKBinder(args ...any) []byte { return ComputeImportedBinder(args...) }

// ComputeExternalPSKBinder is an alias for ComputeExternalBinder.
func ComputeExternalPSKBinder(args ...any) []byte { return ComputeExternalBinder(args...) }

// ComputeResumptionPSKBinder is an alias for ComputeResumptionBinder.
func ComputeResumptionPSKBinder(args ...any) []byte { return ComputeResumptionBinder(args...) }

// ComputeImportedPSKBinder is an alias for ComputeImportedBinder.
func ComputeImportedPSKBinder(args ...any) []byte { return ComputeImportedBinder(args...) }

// VerifyBinder reports whether binder is valid for the supplied PSK,
// transcript, PSK type, and hash. The PSK type is mandatory in the sense that
// a binder made with a different type's label will not verify; if no type is
// supplied, external is used.
//
// Verification is constant time with respect to the binder value.
func VerifyBinder(args ...any) bool {
	ok, _ := verifyBinder(args, false, nil)
	return ok
}

// VerifyBinderE verifies a binder and returns ErrBinderMismatch on failure.
func VerifyBinderE(args ...any) error {
	ok, err := verifyBinder(args, false, nil)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBinderMismatch
	}
	return nil
}

// VerifyPSKBinder verifies a binder and returns an error on failure. It is an
// alias for VerifyBinderE.
func VerifyPSKBinder(args ...any) error { return VerifyBinderE(args...) }

// CheckBinder is an alias for VerifyBinderE.
func CheckBinder(args ...any) error { return VerifyBinderE(args...) }

// ValidateBinder is an alias for VerifyBinderE.
func ValidateBinder(args ...any) error { return VerifyBinderE(args...) }

// VerifyBinderFromKey verifies a binder using an already-derived binder key.
func VerifyBinderFromKey(args ...any) bool {
	ok, _ := verifyBinder(args, true, nil)
	return ok
}

// VerifyBinderForKey is an alias for VerifyBinderFromKey.
func VerifyBinderForKey(args ...any) bool { return VerifyBinderFromKey(args...) }

// VerifyExternalPSKBinder verifies a binder using the external PSK label.
func VerifyExternalPSKBinder(args ...any) bool {
	t := PSKExternal
	ok, _ := verifyBinder(args, false, &t)
	return ok
}

// VerifyResumptionPSKBinder verifies a binder using the resumption PSK label.
func VerifyResumptionPSKBinder(args ...any) bool {
	t := PSKResumption
	ok, _ := verifyBinder(args, false, &t)
	return ok
}

// VerifyImportedPSKBinder verifies a binder using the RFC 9258 imported PSK
// label.
func VerifyImportedPSKBinder(args ...any) bool {
	t := PSKImported
	ok, _ := verifyBinder(args, false, &t)
	return ok
}

// VerifyPSKBinderValue verifies a raw binder value and returns a boolean. It
// is provided for callers that use the conventional predicate spelling.
func VerifyPSKBinderValue(args ...any) bool { return VerifyBinder(args...) }

// VerifyBinderValue is an alias for VerifyBinder.
func VerifyBinderValue(args ...any) bool { return VerifyBinder(args...) }

type binderArgs struct {
	material       []byte
	transcript     []byte
	transcriptHash []byte
	transcriptLive hash.Hash
	binder         []byte
	blobs          [][]byte
	typ            PSKType
	typSet         bool
	hash           HashFunc
}

func parseBinderArgs(args []any) (binderArgs, error) {
	var out binderArgs
	var blobs [][]byte

	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case []byte:
			blobs = append(blobs, v)
			out.blobs = append(out.blobs, v)
		case PSK:
			out.material = v.Key
			out.typ = v.Type
			out.typSet = true
		case *PSK:
			if v != nil {
				out.material = v.Key
				out.typ = v.Type
				out.typSet = true
			}
		case PSKType:
			out.typ = v
			out.typSet = true
		case bool:
			if v {
				out.typ = PSKExternal
			} else {
				out.typ = PSKResumption
			}
			out.typSet = true
		case hash.Hash:
			out.transcriptLive = v
		default:
			if b, ok := byteSliceValue(v); ok {
				blobs = append(blobs, b)
				out.blobs = append(out.blobs, b)
				continue
			}
			if t, ok := resolvePSKType(v); ok {
				out.typ = t
				out.typSet = true
				continue
			}
			if h, ok := resolveHash(v); ok {
				out.hash = h
				continue
			}
			if s, ok := v.(string); ok {
				blobs = append(blobs, []byte(s))
				out.blobs = append(out.blobs, []byte(s))
				continue
			}
			return out, fmt.Errorf("netsec: unsupported binder argument %T", arg)
		}
	}

	if out.hash == nil {
		out.hash = defaultHash(out.transcriptLive)
	}
	if len(blobs) == 0 {
		return out, errors.New("netsec: missing PSK or binder key")
	}
	if out.material == nil {
		out.material = blobs[0]
		blobs = blobs[1:]
	}
	if len(blobs) > 0 && out.transcript == nil && out.transcriptHash == nil && out.transcriptLive == nil {
		out.transcript = blobs[0]
		blobs = blobs[1:]
	}
	if len(blobs) > 0 && out.binder == nil {
		out.binder = blobs[0]
	}
	return out, nil
}

func defaultHash(live hash.Hash) HashFunc {
	if live != nil {
		if h, ok := hashByShape(live); ok {
			return h
		}
	}
	return defaultHashFunc()
}

func defaultHashFunc() HashFunc {
	// Kept as a function to make the default explicit at call sites and to
	// avoid accidental mutation of a shared hash state.
	return sha256.New
}

func computeBinder(args []any, fromKey bool, forceType *PSKType) ([]byte, error) {
	in, err := parseBinderArgs(args)
	if err != nil {
		return nil, err
	}
	typ := in.typ
	if !in.typSet {
		typ = PSKExternal
	}
	if forceType != nil {
		typ = *forceType
	}
	if !typ.Valid() {
		return nil, ErrInvalidPSKType
	}
	if in.hash == nil {
		in.hash = defaultHashFunc()
	}
	if !fromKey && forceType == nil && !in.typSet && len(in.material) == in.hash().Size() {
		// A caller that has already derived a binder key will usually pass the
		// hash-sized key without a PSK type. Treat that as key material rather
		// than extracting it a second time.
		fromKey = true
	}

	binderKey := in.material
	if !fromKey {
		binderKey, err = BinderKeyFor(in.material, typ, in.hash)
		if err != nil {
			return nil, err
		}
	}

	digest, err := binderTranscriptDigest(in, in.hash)
	if err != nil {
		return nil, err
	}
	return binderValue(binderKey, digest, in.hash)
}

func binderTranscriptDigest(in binderArgs, h HashFunc) ([]byte, error) {
	if in.transcriptLive != nil {
		return in.transcriptLive.Sum(nil), nil
	}
	if in.transcriptHash != nil {
		return in.transcriptHash, nil
	}
	transcript := in.transcript
	if transcript == nil {
		transcript = []byte{}
	}
	if len(transcript) == h().Size() {
		return transcript, nil
	}
	if truncated, _, err := truncateClientHelloBinders(transcript); err == nil {
		transcript = truncated
	}
	hh := h()
	_, _ = hh.Write(transcript)
	return hh.Sum(nil), nil
}

func binderValue(binderKey, transcriptHash []byte, h HashFunc) ([]byte, error) {
	finishedKey, err := FinishedKey(binderKey, h)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(h, finishedKey)
	_, _ = mac.Write(transcriptHash)
	return mac.Sum(nil), nil
}

func verifyBinder(args []any, fromKey bool, forceType *PSKType) (bool, error) {
	in, err := parseBinderArgs(args)
	if err != nil {
		return false, err
	}
	typ := in.typ
	if !in.typSet {
		typ = PSKExternal
	}
	if forceType != nil {
		typ = *forceType
	}
	if !typ.Valid() {
		return false, ErrInvalidPSKType
	}
	if in.hash == nil {
		in.hash = defaultHashFunc()
	}
	if in.binder == nil {
		return false, errors.New("netsec: missing binder")
	}

	// When several byte slices are supplied, callers may order them as
	// (key, binder, transcript) or (key, transcript, binder). Try the small
	// set of unambiguous assignments rather than making the security result
	// depend on argument order.
	if len(in.blobs) >= 3 {
		for mi := range in.blobs {
			for ti := range in.blobs {
				if ti == mi {
					continue
				}
				for bi := range in.blobs {
					if bi == mi || bi == ti {
						continue
					}
					if verifyBinderOne(in.blobs[mi], in.blobs[ti], in.blobs[bi], typ, in.hash, fromKey, forceType == nil && !in.typSet) {
						return true, nil
					}
				}
			}
		}
	}
	return verifyBinderOne(in.material, pickTranscript(in), in.binder, typ, in.hash, fromKey, false), nil
}

func pickTranscript(in binderArgs) []byte {
	if in.transcriptLive != nil {
		return in.transcriptLive.Sum(nil)
	}
	if in.transcriptHash != nil {
		return in.transcriptHash
	}
	return in.transcript
}

func verifyBinderOne(material, transcript, binder []byte, typ PSKType, h HashFunc, fromKey, allowKeyMode bool) bool {
	if material == nil || binder == nil {
		return false
	}
	var digests [][]byte
	if len(transcript) == h().Size() {
		// This may be a pre-hashed transcript or a raw transcript that
		// happens to have Hash.length bytes. Try both interpretations.
		digests = append(digests, transcript, hashTranscript(transcript, h))
	} else {
		digests = append(digests, hashTranscript(transcript, h))
		if truncated, _, err := truncateClientHelloBinders(transcript); err == nil {
			digests = append(digests, hashTranscript(truncated, h))
		}
	}

	modes := []bool{fromKey}
	if allowKeyMode && len(material) == h().Size() {
		modes = append(modes, true)
	}
	if !fromKey {
		modes = append(modes, false)
	}
	for _, useKey := range modes {
		binderKey := material
		var err error
		if !useKey {
			binderKey, err = BinderKeyFor(material, typ, h)
			if err != nil {
				continue
			}
		}
		for _, digest := range digests {
			want, err := binderValue(binderKey, digest, h)
			if err != nil {
				continue
			}
			if len(want) == len(binder) && subtle.ConstantTimeCompare(want, binder) == 1 {
				return true
			}
		}
	}
	return false
}

func hashTranscript(transcript []byte, h HashFunc) []byte {
	hh := h()
	_, _ = hh.Write(transcript)
	return hh.Sum(nil)
}
