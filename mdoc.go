package netsec

import (
	"crypto"
	"errors"
	"fmt"
	"strings"
	"time"
)

// issuerSignedItem is a disclosed data element. Raw holds the exact encoded
// bytes of IssuerSignedItemBytes (#6.24(bstr .cbor IssuerSignedItem)) as they
// appeared in the presentation; the digest is computed over these bytes.
type issuerSignedItem struct {
	DigestID          uint64
	Random            []byte
	ElementIdentifier string
	ElementValue      any
	Raw               []byte
}

func parseIssuerSignedItem(raw []byte) (*issuerSignedItem, error) {
	v, err := decodeCBOR(raw)
	if err != nil {
		return nil, fmt.Errorf("mdoc: decoding IssuerSignedItemBytes: %w", err)
	}
	tag, ok := v.(cborTag)
	if !ok || tag.num != 24 {
		return nil, errors.New("mdoc: IssuerSignedItemBytes must be tag 24")
	}
	inner, ok := tag.value.([]byte)
	if !ok {
		return nil, errors.New("mdoc: tag 24 must wrap a bstr")
	}
	decoded, err := decodeCBOR(inner)
	if err != nil {
		return nil, fmt.Errorf("mdoc: decoding IssuerSignedItem: %w", err)
	}
	m, ok := decoded.(map[any]any)
	if !ok {
		return nil, errors.New("mdoc: IssuerSignedItem must be a map")
	}
	item := &issuerSignedItem{Raw: raw}
	id, ok := m["digestID"].(uint64)
	if !ok {
		return nil, errors.New("mdoc: IssuerSignedItem missing digestID")
	}
	item.DigestID = id
	if item.Random, ok = m["random"].([]byte); !ok {
		return nil, errors.New("mdoc: IssuerSignedItem missing random")
	}
	if item.ElementIdentifier, ok = m["elementIdentifier"].(string); !ok {
		return nil, errors.New("mdoc: IssuerSignedItem missing elementIdentifier")
	}
	var present bool
	if item.ElementValue, present = m["elementValue"]; !present {
		return nil, errors.New("mdoc: IssuerSignedItem missing elementValue")
	}
	return item, nil
}

// mobileSecurityObject is the parsed MSO payload of the issuerAuth
// COSE_Sign1 structure.
type mobileSecurityObject struct {
	Version         string
	DigestAlgorithm string
	DocType         string
	// ValueDigests maps namespace -> digestID -> digest.
	ValueDigests map[string]map[uint64][]byte
	ValidFrom    time.Time
	ValidUntil   time.Time
}

func parseMSO(payload []byte) (*mobileSecurityObject, error) {
	v, err := decodeCBOR(payload)
	if err != nil {
		return nil, fmt.Errorf("mdoc: decoding MSO payload: %w", err)
	}
	// MobileSecurityObjectBytes = #6.24(bstr .cbor MobileSecurityObject)
	if tag, ok := v.(cborTag); ok && tag.num == 24 {
		b, ok := tag.value.([]byte)
		if !ok {
			return nil, errors.New("mdoc: MSO tag 24 must wrap a bstr")
		}
		if v, err = decodeCBOR(b); err != nil {
			return nil, fmt.Errorf("mdoc: decoding MSO: %w", err)
		}
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, errors.New("mdoc: MSO must be a map")
	}
	mso := &mobileSecurityObject{ValueDigests: map[string]map[uint64][]byte{}}
	if mso.Version, ok = m["version"].(string); !ok {
		return nil, errors.New("mdoc: MSO missing version")
	}
	if mso.DigestAlgorithm, ok = m["digestAlgorithm"].(string); !ok {
		return nil, errors.New("mdoc: MSO missing digestAlgorithm")
	}
	if mso.DocType, ok = m["docType"].(string); !ok {
		return nil, errors.New("mdoc: MSO missing docType")
	}
	vd, ok := m["valueDigests"].(map[any]any)
	if !ok {
		return nil, errors.New("mdoc: MSO missing valueDigests")
	}
	for ns, digests := range vd {
		nsStr, ok := ns.(string)
		if !ok {
			return nil, errors.New("mdoc: valueDigests namespace must be text")
		}
		dm, ok := digests.(map[any]any)
		if !ok {
			return nil, fmt.Errorf("mdoc: valueDigests[%q] must be a map", nsStr)
		}
		entry := make(map[uint64][]byte, len(dm))
		for id, digest := range dm {
			idNum, ok := id.(uint64)
			if !ok {
				return nil, fmt.Errorf("mdoc: valueDigests[%q] key must be uint", nsStr)
			}
			digestBytes, ok := digest.([]byte)
			if !ok {
				return nil, fmt.Errorf("mdoc: valueDigests[%q][%d] must be a bstr", nsStr, idNum)
			}
			entry[idNum] = digestBytes
		}
		mso.ValueDigests[nsStr] = entry
	}
	vi, ok := m["validityInfo"].(map[any]any)
	if !ok {
		return nil, errors.New("mdoc: MSO missing validityInfo")
	}
	if mso.ValidFrom, err = parseTDate(vi["validFrom"]); err != nil {
		return nil, fmt.Errorf("mdoc: validityInfo.validFrom: %w", err)
	}
	if mso.ValidUntil, err = parseTDate(vi["validUntil"]); err != nil {
		return nil, fmt.Errorf("mdoc: validityInfo.validUntil: %w", err)
	}
	return mso, nil
}

func parseTDate(v any) (time.Time, error) {
	tag, ok := v.(cborTag)
	if !ok || tag.num != 0 {
		return time.Time{}, errors.New("expected tag 0 (tdate)")
	}
	s, ok := tag.value.(string)
	if !ok {
		return time.Time{}, errors.New("tdate must be text")
	}
	return time.Parse(time.RFC3339, s)
}

func digestHash(name string) (crypto.Hash, error) {
	switch strings.ToUpper(name) {
	case "SHA-256":
		return crypto.SHA256, nil
	case "SHA-384":
		return crypto.SHA384, nil
	case "SHA-512":
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("mdoc: unsupported digest algorithm %q", name)
	}
}
