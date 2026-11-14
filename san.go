package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net/netip"
	"strings"
)

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

func parseCertificateSANs(cert *x509.Certificate) (SANSet, SPIFFEID, error) {
	if cert == nil {
		return nil, SPIFFEID{}, fmt.Errorf("%w: nil certificate", ErrNoCertificate)
	}

	var set SANSet
	var err error
	found := false
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidSubjectAltName) {
			if found {
				return nil, SPIFFEID{}, fmt.Errorf("%w: duplicate SAN extension", ErrInvalidSAN)
			}
			set, err = parseSANEextension(ext.Value)
			found = true
		}
	}
	if !found {
		set, err = sanSetFromFields(cert)
	}
	if err != nil {
		return nil, SPIFFEID{}, err
	}

	set = normalizeSANSet(set)
	var id SPIFFEID
	seenURI := false
	for _, san := range set {
		if san.Type != SANURI {
			continue
		}
		parsed, parseErr := ParseSPIFFEID(san.Value)
		if parseErr != nil {
			return nil, SPIFFEID{}, parseErr
		}
		if seenURI && !parsed.Equal(id) {
			return nil, SPIFFEID{}, fmt.Errorf("%w: %s and %s", ErrMultipleIdentities, id, parsed)
		}
		id = parsed
		seenURI = true
	}
	if !seenURI {
		return nil, SPIFFEID{}, ErrMissingSPIFFEID
	}
	return set, id, nil
}

func parseSANEextension(value []byte) (SANSet, error) {
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(value, &sequence)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed SAN extension: %v", ErrInvalidSAN, err)
	}
	if len(rest) != 0 || sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return nil, fmt.Errorf("%w: SAN extension is not a sequence", ErrInvalidSAN)
	}

	var set SANSet
	content := sequence.Bytes
	for len(content) > 0 {
		var name asn1.RawValue
		content, err = asn1.Unmarshal(content, &name)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed general name: %v", ErrInvalidSAN, err)
		}
		if name.Class != asn1.ClassContextSpecific || name.IsCompound {
			return nil, fmt.Errorf("%w: unsupported general name encoding", ErrInvalidSAN)
		}

		switch name.Tag {
		case 1: // rfc822Name
			return nil, fmt.Errorf("%w: email SANs are not workload identities", ErrSANTypeSubstitution)
		case 2: // dNSName
			dns, dnsErr := canonicalDNSName(string(name.Bytes))
			if dnsErr != nil {
				return nil, dnsErr
			}
			set = append(set, SAN{Type: SANDNS, Value: dns})
		case 6: // uniformResourceIdentifier
			uri := string(name.Bytes)
			id, uriErr := ParseSPIFFEID(uri)
			if uriErr != nil {
				return nil, fmt.Errorf("%w: URI SAN %q is not a SPIFFE identity: %w", ErrSANTypeSubstitution, uri, uriErr)
			}
			set = append(set, SAN{Type: SANURI, Value: id.String()})
		case 7: // iPAddress
			addr, ipErr := parseIPAddress(name.Bytes)
			if ipErr != nil {
				return nil, ipErr
			}
			set = append(set, SAN{Type: SANIP, Value: addr.String()})
		default:
			return nil, fmt.Errorf("%w: unsupported general name tag %d", ErrInvalidSAN, name.Tag)
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("%w: empty SAN extension", ErrInvalidSAN)
	}
	return set, nil
}

func sanSetFromFields(cert *x509.Certificate) (SANSet, error) {
	var set SANSet
	for _, name := range cert.DNSNames {
		dns, err := canonicalDNSName(name)
		if err != nil {
			return nil, err
		}
		set = append(set, SAN{Type: SANDNS, Value: dns})
	}
	for _, rawIP := range cert.IPAddresses {
		addr, err := parseIPAddress(rawIP)
		if err != nil {
			return nil, err
		}
		set = append(set, SAN{Type: SANIP, Value: addr.String()})
	}
	for _, uri := range cert.URIs {
		if uri == nil {
			return nil, fmt.Errorf("%w: nil URI SAN", ErrInvalidSAN)
		}
		id, err := ParseSPIFFEID(uri.String())
		if err != nil {
			return nil, fmt.Errorf("%w: URI SAN %q is not a SPIFFE identity: %w", ErrSANTypeSubstitution, uri.String(), err)
		}
		set = append(set, SAN{Type: SANURI, Value: id.String()})
	}
	if len(cert.EmailAddresses) != 0 {
		return nil, fmt.Errorf("%w: email SANs are not workload identities", ErrSANTypeSubstitution)
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("%w: certificate has no supported SANs", ErrInvalidSAN)
	}
	return set, nil
}

func parseIPAddress(raw []byte) (netip.Addr, error) {
	if len(raw) != 4 && len(raw) != 16 {
		return netip.Addr{}, fmt.Errorf("%w: IP SAN must be 4 or 16 bytes", ErrSANTypeSubstitution)
	}
	addr, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.Addr{}, fmt.Errorf("%w: invalid IP SAN", ErrInvalidSAN)
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if !addr.IsValid() || addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: invalid IP SAN", ErrInvalidSAN)
	}
	return addr, nil
}

func hasPathPrefix(path SPIFFEPath, prefix string) bool {
	return strings.HasPrefix(string(path), prefix)
}
