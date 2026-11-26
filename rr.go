package dnssec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	TypeA                    uint16 = 1
	TypeNS                   uint16 = 2
	TypeCNAME                uint16 = 5
	TypeSOA                  uint16 = 6
	TypePTR                  uint16 = 12
	TypeMINFO                uint16 = 14
	TypeMX                   uint16 = 15
	TypeTXT                  uint16 = 16
	TypeRP                   uint16 = 17
	TypeAFSDB                uint16 = 18
	TypeRT                   uint16 = 21
	TypeSIG                  uint16 = 24
	TypePX                   uint16 = 26
	TypeAAAA                 uint16 = 28
	TypeNXT                  uint16 = 30
	TypeKX                   uint16 = 36
	TypeSRV                  uint16 = 33
	TypeNAPTR                uint16 = 35
	TypeDNAME                uint16 = 39
	TypeDS                   uint16 = 43
	TypeIPSECKEY             uint16 = 45
	TypeRRSIG                uint16 = 46
	TypeNSEC                 uint16 = 47
	TypeDNSKEY               uint16 = 48
	TypeNSEC3                uint16 = 50
	TypeNSEC3PARAM           uint16 = 51
	TypeSVCB                 uint16 = 64
	TypeHTTPS                uint16 = 65
	TypeTKEY                 uint16 = 249
	TypeTSIG                 uint16 = 250
	ClassINET                uint16 = 1
	ClassANY                 uint16 = 255
	AlgECDSAP256             uint8  = 13
	AlgorithmECDSAP256SHA256 uint8  = AlgECDSAP256
)

// RR is the resource-record representation used for canonical signing and
// verification. Data is uncompressed RDATA.
type RR struct {
	Name  Name
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
}

// canonicalizeRData lower-cases the uncompressed domain-name fields in RDATA
// for RR types whose RDATA contains names.
func canonicalizeRData(rrtype uint16, rdata []byte) ([]byte, error) {
	out := append([]byte(nil), rdata...)
	lowerNameAt := func(off int) (int, error) {
		if off < 0 || off >= len(out) {
			return off, errors.New("dnssec: RDATA name offset out of range")
		}
		for {
			if off >= len(out) {
				return off, errors.New("dnssec: truncated RDATA name")
			}
			l := int(out[off])
			if l&0xc0 != 0 {
				return off, errors.New("dnssec: compressed name in canonical RDATA")
			}
			off++
			if l == 0 {
				return off, nil
			}
			if off+l > len(out) {
				return off, errors.New("dnssec: truncated RDATA label")
			}
			for i := off; i < off+l; i++ {
				if out[i] >= 'A' && out[i] <= 'Z' {
					out[i] += 'a' - 'A'
				}
			}
			off += l
		}
	}

	switch rrtype {
	case TypeNS, TypeCNAME, TypePTR, TypeDNAME:
		if _, err := lowerNameAt(0); err != nil {
			return nil, err
		}
	case TypeSOA, TypeMINFO, TypeRP:
		off, err := lowerNameAt(0)
		if err != nil {
			return nil, err
		}
		if _, err := lowerNameAt(off); err != nil {
			return nil, err
		}
	case TypeMX, TypeAFSDB, TypeRT, TypeKX:
		if len(out) < 2 {
			return nil, errors.New("dnssec: truncated preference field")
		}
		if _, err := lowerNameAt(2); err != nil {
			return nil, err
		}
	case TypeSRV:
		if len(out) < 6 {
			return nil, errors.New("dnssec: truncated SRV RDATA")
		}
		if _, err := lowerNameAt(6); err != nil {
			return nil, err
		}
	case TypePX:
		if len(out) < 2 {
			return nil, errors.New("dnssec: truncated PX preference field")
		}
		off, err := lowerNameAt(2)
		if err != nil {
			return nil, err
		}
		if _, err := lowerNameAt(off); err != nil {
			return nil, err
		}
	case TypeNAPTR:
		off := 4
		for i := 0; i < 3; i++ {
			if off >= len(out) {
				return nil, errors.New("dnssec: truncated NAPTR character string")
			}
			off += 1 + int(out[off])
			if off > len(out) {
				return nil, errors.New("dnssec: truncated NAPTR character string")
			}
		}
		if _, err := lowerNameAt(off); err != nil {
			return nil, err
		}
	case TypeRRSIG, TypeSIG:
		if len(out) < 18 {
			return nil, errors.New("dnssec: truncated signature RDATA")
		}
		if _, err := lowerNameAt(18); err != nil {
			return nil, err
		}
	case TypeNSEC, TypeNXT:
		if _, err := lowerNameAt(0); err != nil {
			return nil, err
		}
	case TypeSVCB, TypeHTTPS:
		if len(out) < 2 {
			return nil, errors.New("dnssec: truncated SVCB/HTTPS RDATA")
		}
		if _, err := lowerNameAt(2); err != nil {
			return nil, err
		}
	case TypeIPSECKEY:
		if len(out) < 3 {
			return nil, errors.New("dnssec: truncated IPSECKEY RDATA")
		}
		if out[1] == 3 {
			if _, err := lowerNameAt(3); err != nil {
				return nil, err
			}
		}
	case TypeTKEY, TypeTSIG:
		if _, err := lowerNameAt(0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CanonicalRData returns the RFC 4034 canonical RDATA form.
func CanonicalRData(rrtype uint16, rdata []byte) ([]byte, error) {
	return canonicalizeRData(rrtype, rdata)
}

// CanonicalizeRRset canonicalizes all RDATA fields and sorts records by
// canonical RDATA. All records must have the same owner, type and class.
func CanonicalizeRRset(rrset []RR) ([]RR, error) {
	if len(rrset) == 0 {
		return nil, errors.New("dnssec: empty RRset")
	}
	owner := rrset[0].Name
	typ := rrset[0].Type
	class := rrset[0].Class
	if class == 0 {
		class = ClassINET
	}
	out := make([]RR, len(rrset))
	for i, rr := range rrset {
		if !EqualNames(rr.Name, owner) {
			return nil, errors.New("dnssec: RRset has multiple owner names")
		}
		if rr.Type != typ {
			return nil, errors.New("dnssec: RRset has multiple types")
		}
		rrClass := rr.Class
		if rrClass == 0 {
			rrClass = ClassINET
		}
		if rrClass != class {
			return nil, errors.New("dnssec: RRset has multiple classes")
		}
		data, err := canonicalizeRData(typ, rr.Data)
		if err != nil {
			return nil, fmt.Errorf("dnssec: canonicalize RR %d: %w", i, err)
		}
		out[i] = rr
		out[i].Name = Name{Labels: append([]string(nil), owner.Labels...)}
		out[i].Class = class
		out[i].Data = data
	}
	sort.SliceStable(out, func(i, j int) bool {
		return bytes.Compare(out[i].Data, out[j].Data) < 0
	})
	return out, nil
}

// CanonicalRRWire encodes one RR using the supplied owner and TTL and
// canonical RDATA.
func CanonicalRRWire(rr RR, owner Name, ttl uint32) ([]byte, error) {
	data, err := canonicalizeRData(rr.Type, rr.Data)
	if err != nil {
		return nil, err
	}
	if len(data) > 65535 {
		return nil, errors.New("dnssec: RDATA exceeds 65535 octets")
	}
	nameWire, err := CanonicalNameWire(owner)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nameWire)+10+len(data))
	out = append(out, nameWire...)
	out = append(out, byte(rr.Type>>8), byte(rr.Type))
	class := rr.Class
	if class == 0 {
		class = ClassINET
	}
	out = append(out, byte(class>>8), byte(class))
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], ttl)
	out = append(out, buf[:]...)
	binary.BigEndian.PutUint16(buf[:2], uint16(len(data)))
	out = append(out, buf[:2]...)
	out = append(out, data...)
	return out, nil
}

// ParseRRWire decodes one uncompressed wire-format RR.
func ParseRRWire(wire []byte, offset int) (RR, int, error) {
	name, off, err := ParseWireName(wire, offset)
	if err != nil {
		return RR{}, 0, err
	}
	if off+10 > len(wire) {
		return RR{}, 0, errors.New("dnssec: truncated RR header")
	}
	rr := RR{Name: name}
	rr.Type = binary.BigEndian.Uint16(wire[off:])
	rr.Class = binary.BigEndian.Uint16(wire[off+2:])
	rr.TTL = binary.BigEndian.Uint32(wire[off+4:])
	rdlen := int(binary.BigEndian.Uint16(wire[off+8:]))
	off += 10
	if off+rdlen > len(wire) {
		return RR{}, 0, errors.New("dnssec: truncated RR RDATA")
	}
	rr.Data = append([]byte(nil), wire[off:off+rdlen]...)
	return rr, off + rdlen, nil
}
