package netsec

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxStatusListSize = 16 << 20

var (
	// ErrNoStatusList indicates that the credential has no status list
	// reference.
	ErrNoStatusList = errors.New("netsec: credential has no status list")

	// ErrNoStatusListFetcher indicates that a Verifier has no way to retrieve
	// the status list.
	ErrNoStatusListFetcher = errors.New("netsec: no status list fetcher configured")

	// ErrInvalidStatusList indicates that the retrieved status list could not
	// be decoded or does not have revocation semantics.
	ErrInvalidStatusList = errors.New("netsec: invalid status list")
)

// StatusListReference identifies the status list and the credential's index in
// that list.
type StatusListReference struct {
	URI   string `json:"uri"`
	Index uint64 `json:"idx"`
}

// Credential is the subset of a verifiable credential needed for a status
// check.
type Credential struct {
	Issuer     string               `json:"issuer"`
	StatusList *StatusListReference `json:"status_list,omitempty"`
}

// StatusListFetcher retrieves the raw status list resource at uri. A Verifier
// calls it only after the URI has passed the issuer-host binding check.
type StatusListFetcher interface {
	FetchStatusList(ctx context.Context, uri string) ([]byte, error)
}

// StatusListFetcherFunc adapts a function to StatusListFetcher.
type StatusListFetcherFunc func(ctx context.Context, uri string) ([]byte, error)

// FetchStatusList implements StatusListFetcher.
func (f StatusListFetcherFunc) FetchStatusList(ctx context.Context, uri string) ([]byte, error) {
	return f(ctx, uri)
}

// RevocationResult is the outcome of a status list revocation check.
type RevocationResult struct {
	// Revoked is true only when a trusted, same-host status list says that the
	// credential index is revoked.
	Revoked bool

	// StatusListURI is the URI that was actually trusted and checked.
	StatusListURI string
}

// Verifier checks credential status using issuer-bound status lists.
type Verifier struct {
	Fetcher StatusListFetcher
}

// CheckRevocation retrieves the referenced status list and reports whether the
// credential index is revoked.
//
// The status list URI is validated before the fetcher is called. If the list
// is hosted on a different host than the issuer, no request is made and an
// error wrapping ErrStatusListHostMismatch is returned.
func (v *Verifier) CheckRevocation(ctx context.Context, issuer string, ref StatusListReference) (RevocationResult, error) {
	if err := ValidateStatusListURI(issuer, ref.URI); err != nil {
		return RevocationResult{}, err
	}
	return v.checkValidated(ctx, ref)
}

// CheckCredential checks the status list reference carried by cred.
func (v *Verifier) CheckCredential(ctx context.Context, cred Credential) (RevocationResult, error) {
	if cred.StatusList == nil {
		return RevocationResult{}, ErrNoStatusList
	}
	return v.CheckRevocation(ctx, cred.Issuer, *cred.StatusList)
}

func (v *Verifier) checkValidated(ctx context.Context, ref StatusListReference) (RevocationResult, error) {
	if v == nil || v.Fetcher == nil {
		return RevocationResult{}, ErrNoStatusListFetcher
	}

	raw, err := v.Fetcher.FetchStatusList(ctx, ref.URI)
	if err != nil {
		return RevocationResult{}, fmt.Errorf("netsec: fetch status list: %w", err)
	}

	list, err := ParseBitstringStatusList(raw)
	if err != nil {
		return RevocationResult{}, err
	}
	revoked, err := list.Revoked(ref.Index)
	if err != nil {
		return RevocationResult{}, err
	}
	return RevocationResult{Revoked: revoked, StatusListURI: ref.URI}, nil
}

// BitstringStatusList is a decoded W3C Bitstring Status List.
type BitstringStatusList struct {
	Purpose string
	bits    []byte
}

type bitstringStatusListDocument struct {
	CredentialSubject *struct {
		StatusPurpose string `json:"statusPurpose"`
		EncodedList   string `json:"encodedList"`
		StatusSize    *int   `json:"statusSize,omitempty"`
	} `json:"credentialSubject"`
}

// ParseBitstringStatusList decodes a W3C Bitstring Status List document. The
// encodedList value is base64url-encoded, GZIP-compressed bitstring data.
func ParseBitstringStatusList(raw []byte) (*BitstringStatusList, error) {
	var doc bitstringStatusListDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStatusList, err)
	}
	if doc.CredentialSubject == nil {
		return nil, fmt.Errorf("%w: missing credentialSubject", ErrInvalidStatusList)
	}
	if doc.CredentialSubject.StatusPurpose != "revocation" {
		return nil, fmt.Errorf("%w: statusPurpose %q is not revocation", ErrInvalidStatusList, doc.CredentialSubject.StatusPurpose)
	}
	if doc.CredentialSubject.StatusSize != nil && *doc.CredentialSubject.StatusSize != 1 {
		return nil, fmt.Errorf("%w: unsupported statusSize %d", ErrInvalidStatusList, *doc.CredentialSubject.StatusSize)
	}
	if doc.CredentialSubject.EncodedList == "" {
		return nil, fmt.Errorf("%w: missing encodedList", ErrInvalidStatusList)
	}

	compressed, err := decodeBase64URL(doc.CredentialSubject.EncodedList)
	if err != nil {
		return nil, fmt.Errorf("%w: encodedList: %v", ErrInvalidStatusList, err)
	}
	if len(compressed) > maxStatusListSize {
		return nil, fmt.Errorf("%w: encodedList is too large", ErrInvalidStatusList)
	}

	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("%w: encodedList is not gzip data: %v", ErrInvalidStatusList, err)
	}
	defer zr.Close()

	bits, err := io.ReadAll(io.LimitReader(zr, maxStatusListSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: decompress encodedList: %v", ErrInvalidStatusList, err)
	}
	if len(bits) > maxStatusListSize {
		return nil, fmt.Errorf("%w: decoded status list is too large", ErrInvalidStatusList)
	}

	return &BitstringStatusList{
		Purpose: doc.CredentialSubject.StatusPurpose,
		bits:    bits,
	}, nil
}

// Revoked reports whether the bit at index is set. Bits are read in W3C
// Bitstring Status List order: index 0 is the most significant bit of byte 0.
func (l *BitstringStatusList) Revoked(index uint64) (bool, error) {
	if l == nil {
		return false, fmt.Errorf("%w: nil status list", ErrInvalidStatusList)
	}
	byteIndex := index / 8
	if byteIndex >= uint64(len(l.bits)) {
		return false, fmt.Errorf("%w: status index %d is outside the list", ErrInvalidStatusList, index)
	}
	mask := byte(1 << (7 - (index % 8)))
	return l.bits[byteIndex]&mask != 0, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if data, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
