package dnssec

import (
	"bytes"
	"sort"
)

// nameBearingTypes is the set of types whose RDATA embeds domain names that
// must be emitted in lowercase, uncompressed canonical form (RFC 4034 6.2).
var nameBearingTypes = map[uint16]bool{
	TypeNS:    true,
	TypeCNAME: true,
	TypePTR:   true,
	TypeSOA:   true,
	TypeMX:    true,
	TypeSRV:   true,
}

// CanonicalRdata returns the canonical form of rr.Rdata: embedded domain
// names are lowercased and uncompressed, NSEC/NSEC3 bitmaps are re-encoded
// deterministically, and every other type is returned verbatim.
func CanonicalRdata(rr RR) ([]byte, error) {
	switch rr.Type {
	case TypeNS, TypeCNAME, TypePTR:
		n, _, err := readName(rr.Rdata, 0)
		if err != nil {
			return nil, err
		}
		return n.LowerWire(), nil

	case TypeMX:
		if len(rr.Rdata) < 2 {
			return nil, ErrRdataLen
		}
		n, _, err := readName(rr.Rdata, 2)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, 2+len(n.LowerWire()))
		out = append(out, rr.Rdata[:2]...)
		out = append(out, n.LowerWire()...)
		return out, nil

	case TypeSOA:
		mname, n1, err := readName(rr.Rdata, 0)
		if err != nil {
			return nil, err
		}
		rname, n2, err := readName(rr.Rdata, n1)
		if err != nil {
			return nil, err
		}
		if len(rr.Rdata) < n2+20 {
			return nil, ErrRdataLen
		}
		out := make([]byte, 0, len(mname.LowerWire())+len(rname.LowerWire())+20)
		out = append(out, mname.LowerWire()...)
		out = append(out, rname.LowerWire()...)
		out = append(out, rr.Rdata[n2:n2+20]...)
		return out, nil

	case TypeSRV:
		if len(rr.Rdata) < 6 {
			return nil, ErrRdataLen
		}
		n, _, err := readName(rr.Rdata, 6)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, 6+len(n.LowerWire()))
		out = append(out, rr.Rdata[:6]...)
		out = append(out, n.LowerWire()...)
		return out, nil

	case TypeNSEC:
		nsec, err := ParseNSEC(rr.Rdata)
		if err != nil {
			return nil, err
		}
		out := nsec.NextName.LowerWire()
		out = append(out, encodeTypeBitmap(nsec.Types)...)
		return out, nil

	case TypeNSEC3:
		n3, err := ParseNSEC3(rr.Rdata)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, 6+len(n3.Salt)+len(n3.NextHashed)+len(encodeTypeBitmap(n3.Types)))
		out = append(out, n3.HashAlg, n3.Flags)
		out = appendU16(out, n3.Iterations)
		out = append(out, byte(len(n3.Salt)))
		out = append(out, n3.Salt...)
		out = append(out, byte(len(n3.NextHashed)))
		out = append(out, n3.NextHashed...)
		out = append(out, encodeTypeBitmap(n3.Types)...)
		return out, nil

	default:
		// A, AAAA, TXT, DNSKEY, DS, RRSIG, etc.: RDATA is already binary
		// and contains no domain names.
		return append([]byte(nil), rr.Rdata...), nil
	}
}

// CanonicalRR renders one RR in the canonical wire form used by RRSIG
// verification: lowercase owner name, fixed type/class, the RRSIG Original
// TTL (not the record's own TTL), canonical RDATA, no compression.
func CanonicalRR(rr RR, origTTL uint32) ([]byte, error) {
	rdata, err := CanonicalRdata(rr)
	if err != nil {
		return nil, err
	}
	out := rr.Name.LowerWire()
	out = appendU16(out, rr.Type)
	out = appendU16(out, rr.Class)
	out = appendU32(out, origTTL)
	out = appendU16(out, uint16(len(rdata)))
	out = append(out, rdata...)
	return out, nil
}

type canonicalEntry struct {
	rr    RR
	rdata []byte
	wire  []byte
}

// CanonicalRRSet performs RFC 4034 section 6.3 ordering and de-duplication of
// an RRset and returns the concatenated canonical wire records that feed the
// signature digest. Records are sorted by canonical owner name (all owners in
// an RRset are identical) and then by canonical RDATA byte order; duplicate
// RDATA are removed.
func CanonicalRRSet(rrset []RR, origTTL uint32) ([]byte, error) {
	entries := make([]canonicalEntry, 0, len(rrset))
	seen := map[string]bool{}
	for _, rr := range rrset {
		rdata, err := CanonicalRdata(rr)
		if err != nil {
			return nil, err
		}
		key := string(rdata)
		if seen[key] {
			continue
		}
		seen[key] = true
		wire, err := CanonicalRR(rr, origTTL)
		if err != nil {
			return nil, err
		}
		entries = append(entries, canonicalEntry{rr: rr, rdata: rdata, wire: wire})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if c := CanonicalCompare(entries[i].rr.Name, entries[j].rr.Name); c != 0 {
			return c < 0
		}
		return bytes.Compare(entries[i].rdata, entries[j].rdata) < 0
	})

	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(e.wire)
	}
	return buf.Bytes(), nil
}

// SignedData assembles the data actually signed by an RRSIG (RFC 4034
// section 3.1.8.1): RRSIG RDATA with the Signature field removed, followed by
// the canonical, sorted RRset.
func SignedData(sig *RRSIG, canonicalRRset []byte) []byte {
	out := make([]byte, 0, 18+len(sig.Signer.Wire())+len(canonicalRRset))
	out = appendU16(out, sig.TypeCovered)
	out = append(out, sig.Algorithm, sig.Labels)
	out = appendU32(out, sig.OrigTTL)
	out = appendU32(out, sig.Expiration)
	out = appendU32(out, sig.Inception)
	out = appendU16(out, sig.KeyTag)
	out = append(out, sig.Signer.LowerWire()...)
	out = append(out, canonicalRRset...)
	return out
}

func appendU16(b []byte, v uint16) []byte {
	return append(b, byte(v>>8), byte(v))
}

func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
