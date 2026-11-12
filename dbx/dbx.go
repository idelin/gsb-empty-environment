// Package dbx implements revocation decisions against the forbidden
// signature database (dbx) for a boot loader, plus authenticated,
// rollback-protected dbx updates.
//
// Revocation is fail-closed: an image is rejected when its own hash is
// listed in dbx or when any certificate in its signing chain is listed,
// even if the allowed-signature database (db) would accept it.
package dbx

import (
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
)

// EntryKind identifies what a dbx entry revokes.
type EntryKind uint8

const (
	// EntryImageHash revokes a specific image by its SHA-256 digest.
	EntryImageHash EntryKind = 1
	// EntryCertTBSHash revokes a certificate by the SHA-256 digest of its
	// DER-encoded TBSCertificate. Any image whose chain contains the
	// certificate is rejected.
	EntryCertTBSHash EntryKind = 2
)

// Entry is a single dbx revocation record.
type Entry struct {
	Kind EntryKind
	Hash [sha256.Size]byte
}

// ErrInvalidEntry is returned when a dbx entry is malformed.
var ErrInvalidEntry = errors.New("dbx: invalid entry")

func (e Entry) validate() error {
	switch e.Kind {
	case EntryImageHash, EntryCertTBSHash:
		return nil
	default:
		return fmt.Errorf("%w: unknown kind %d", ErrInvalidEntry, e.Kind)
	}
}

// DBX is an immutable, indexed view of the forbidden-signature database.
type DBX struct {
	timestamp uint64
	entries   []Entry
	images    map[[sha256.Size]byte]struct{}
	certs     map[[sha256.Size]byte]struct{}
}

// New builds an indexed DBX from raw entries. Every entry is validated so a
// malformed record can never weaken revocation checks later.
func New(timestamp uint64, entries []Entry) (*DBX, error) {
	d := &DBX{
		timestamp: timestamp,
		entries:   make([]Entry, len(entries)),
		images:    make(map[[sha256.Size]byte]struct{}),
		certs:     make(map[[sha256.Size]byte]struct{}),
	}
	for i, e := range entries {
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		d.entries[i] = e
		switch e.Kind {
		case EntryImageHash:
			d.images[e.Hash] = struct{}{}
		case EntryCertTBSHash:
			d.certs[e.Hash] = struct{}{}
		}
	}
	return d, nil
}

// Timestamp returns the timestamp of the authenticated dbx variable this
// view was built from.
func (d *DBX) Timestamp() uint64 { return d.timestamp }

// Entries returns a copy of the raw revocation entries.
func (d *DBX) Entries() []Entry { return append([]Entry(nil), d.entries...) }

// CertTBSHash computes the revocation identifier of a certificate: SHA-256
// over its DER-encoded TBSCertificate.
func CertTBSHash(cert *x509.Certificate) [sha256.Size]byte {
	return sha256.Sum256(cert.RawTBSCertificate)
}

// ImageRevoked reports whether the image hash itself is listed in dbx.
func (d *DBX) ImageRevoked(imageHash [sha256.Size]byte) bool {
	_, hit := d.images[imageHash]
	return hit
}

// ChainRevoked reports whether any certificate in the chain — leaf,
// intermediates, or root — is listed in dbx by TBS hash.
func (d *DBX) ChainRevoked(chain []*x509.Certificate) bool {
	for _, cert := range chain {
		if cert == nil {
			continue
		}
		if _, hit := d.certs[CertTBSHash(cert)]; hit {
			return true
		}
	}
	return false
}

// IsRevoked is the decision the loader must apply after db evaluation: it
// returns true — reject the image — when the image hash or any certificate
// in the signing chain is revoked, regardless of what db allowed.
func (d *DBX) IsRevoked(imageHash [sha256.Size]byte, chain []*x509.Certificate) bool {
	return d.ImageRevoked(imageHash) || d.ChainRevoked(chain)
}
