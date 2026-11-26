package rpki

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
)

var ErrSignedObject = errors.New("rpki: signed object validation failed")

// ROAPrefix is a prefix and optional RFC 6482 maxLength.
type ROAPrefix struct {
	Prefix    IPPrefix
	MaxLength int
}

// ROA is the parsed RFC 6482 eContent.
type ROA struct {
	ASN      uint64
	Prefixes []ROAPrefix
}

// ASPA is the parsed ASPA eContent needed for resource validation.
type ASPA struct {
	CustomerAS  uint64
	ProviderASs []uint64
}

type roaWire struct {
	Version asn1.RawValue `asn1:"optional,tag:0,context"`
	ASID    *big.Int
	Blocks  []roaFamilyWire
}

type roaFamilyWire struct {
	AddressFamily []byte
	Addresses     []roaAddressWire
}

type roaAddressWire struct {
	Address   asn1.BitString
	MaxLength int `asn1:"optional"`
}

type aspaWire struct {
	Version     asn1.RawValue `asn1:"optional,tag:0,context"`
	CustomerAS  *big.Int
	ProviderASs []*big.Int
}

// ParseROA parses ROA eContent.
func ParseROA(der []byte) (*ROA, error) {
	var wire roaWire
	rest, err := asn1.Unmarshal(der, &wire)
	if err != nil {
		return nil, fmt.Errorf("%w: ROA: %v", ErrSignedObject, err)
	}
	if len(rest) != 0 || wire.ASID == nil {
		return nil, fmt.Errorf("%w: malformed ROA", ErrSignedObject)
	}
	roa := &ROA{ASN: wire.ASID.Uint64()}
	for _, block := range wire.Blocks {
		family, err := afiToFamily(block.AddressFamily)
		if err != nil {
			return nil, err
		}
		for _, addr := range block.Addresses {
			prefix, err := bitStringToPrefix(addr.Address, family)
			if err != nil {
				return nil, err
			}
			maxLength := addr.MaxLength
			if maxLength == 0 {
				maxLength = prefix.Bits
			}
			if maxLength < prefix.Bits || maxLength > family.bitLen() {
				return nil, fmt.Errorf("%w: ROA maxLength %d invalid for /%d", ErrSignedObject, maxLength, prefix.Bits)
			}
			roa.Prefixes = append(roa.Prefixes, ROAPrefix{Prefix: prefix, MaxLength: maxLength})
		}
	}
	if len(roa.Prefixes) == 0 {
		return nil, fmt.Errorf("%w: ROA has no prefixes", ErrSignedObject)
	}
	return roa, nil
}

// ValidateAgainstEE ensures every ROA prefix is inside the EE certificate set.
func (r *ROA) ValidateAgainstEE(ee *CertInfo) error {
	if ee == nil {
		return fmt.Errorf("%w: nil EE", ErrSignedObject)
	}
	for _, p := range r.Prefixes {
		pr := p.Prefix.Range()
		if !ee.Resources.ContainsIPRange(pr) {
			return fmt.Errorf("%w: ROA prefix %s/%d outside EE resources", ErrOutOfScope, ipString(p.Prefix.Prefix, p.Prefix.Family), p.Prefix.Bits)
		}
	}
	return nil
}

// ParseASPA parses ASPA eContent.
func ParseASPA(der []byte) (*ASPA, error) {
	var wire aspaWire
	rest, err := asn1.Unmarshal(der, &wire)
	if err != nil {
		return nil, fmt.Errorf("%w: ASPA: %v", ErrSignedObject, err)
	}
	if len(rest) != 0 || wire.CustomerAS == nil {
		return nil, fmt.Errorf("%w: malformed ASPA", ErrSignedObject)
	}
	out := &ASPA{CustomerAS: wire.CustomerAS.Uint64()}
	for _, asn := range wire.ProviderASs {
		if asn == nil {
			return nil, fmt.Errorf("%w: nil provider AS", ErrSignedObject)
		}
		out.ProviderASs = append(out.ProviderASs, asn.Uint64())
	}
	if len(out.ProviderASs) == 0 {
		return nil, fmt.Errorf("%w: ASPA has no providers", ErrSignedObject)
	}
	return out, nil
}

// ValidateAgainstEE requires the customer AS to be delegated to the ASPA EE.
func (a *ASPA) ValidateAgainstEE(ee *CertInfo) error {
	if ee == nil {
		return fmt.Errorf("%w: nil EE", ErrSignedObject)
	}
	if !ee.Resources.ContainsASRange(ASRange{Start: a.CustomerAS, End: a.CustomerAS}) {
		return fmt.Errorf("%w: ASPA customer AS%d outside EE ASN resources", ErrOutOfScope, a.CustomerAS)
	}
	return nil
}
