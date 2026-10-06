// Package verisched schedules verification checks against frozen packages
// and decides release gates from bound receipts.
package verisched

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors. Go 1.11 has no errors.Is, compare with ==.
var (
	ErrNoCurrentPackage = errors.New("verisched: no frozen package is active")
	ErrUnknownPackage   = errors.New("verisched: receipt references unknown package")
	ErrUnknownCheck     = errors.New("verisched: receipt references unknown check")
	ErrBindingMismatch  = errors.New("verisched: receipt binding does not match the frozen package")
	ErrPackageArchived  = errors.New("verisched: package is archived; receipt recorded for audit only")
	ErrDuplicateReceipt = errors.New("verisched: receipt already recorded for this check and run")
	ErrCheckRevoked     = errors.New("verisched: check has been revoked")
	ErrUnknownRun       = errors.New("verisched: run does not belong to the current package")
)

// CheckSpec is one check inside a frozen package. Version pins the
// validator rules; bumping it produces a different frozen package.
type CheckSpec struct {
	ID        string
	Validator string
	Version   string
}

// Package is a frozen, immutable set of checks bound to one input digest.
// ID is a content hash: any change to the input or to any rule version
// yields a different package.
type Package struct {
	ID          string
	InputDigest string
	Checks      []CheckSpec
}

// Freeze builds a frozen package. Checks are sorted by ID so the package
// identity does not depend on declaration order.
func Freeze(inputDigest string, checks []CheckSpec) (Package, error) {
	if inputDigest == "" {
		return Package{}, errors.New("verisched: input digest is required")
	}
	frozen := make([]CheckSpec, len(checks))
	copy(frozen, checks)
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].ID < frozen[j].ID })
	seen := map[string]bool{}
	for _, c := range frozen {
		if c.ID == "" || c.Validator == "" || c.Version == "" {
			return Package{}, fmt.Errorf("verisched: check %q is incomplete", c.ID)
		}
		if seen[c.ID] {
			return Package{}, fmt.Errorf("verisched: duplicate check id %q", c.ID)
		}
		seen[c.ID] = true
	}
	h := sha256.New()
	h.Write([]byte("input\x00" + inputDigest))
	for _, c := range frozen {
		h.Write([]byte("\x00check\x00" + c.ID + "\x00" + c.Validator + "\x00" + c.Version))
	}
	return Package{
		ID:          "pkg-" + hex.EncodeToString(h.Sum(nil))[:16],
		InputDigest: inputDigest,
		Checks:      frozen,
	}, nil
}

// Spec returns the spec for checkID, or false if absent.
func (p Package) Spec(checkID string) (CheckSpec, bool) {
	for _, c := range p.Checks {
		if c.ID == checkID {
			return c, true
		}
	}
	return CheckSpec{}, false
}

// Status is the verdict carried by a receipt.
type Status int

const (
	StatusInvalid Status = iota
	StatusPassed
	StatusFailed
)

// Receipt is a single verification result. It is only meaningful when
// every binding field matches the frozen package it is submitted against:
// package id, input digest, validator version and run id.
type Receipt struct {
	PackageID        string
	CheckID          string
	RunID            string
	InputDigest      string
	ValidatorVersion string
	Status           Status
}

// Run is one execution of the current frozen package.
type Run struct {
	ID        string
	PackageID string
}
