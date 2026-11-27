package dnssec

// ValidateDNSKEY enforces the key-level requirements of RFC 4035 section 5.2
// and RFC 5011: a key usable for zone signing must have protocol number 3,
// the ZONE flag set, and the REVOKE flag clear. Unsupported cryptographic
// algorithms are also rejected here.
func ValidateDNSKEY(key *DNSKEY) error {
	if key.Protocol != 3 {
		return ErrBogus("DNSKEY protocol field is not 3")
	}
	if !key.IsZoneKey() {
		return ErrBogus("DNSKEY lacks the ZONE flag")
	}
	if key.IsRevoked() {
		return ErrBogus("REVOKED DNSKEY must not validate the zone")
	}
	switch key.Algorithm {
	case AlgECDSAP256SHA256, AlgECDSAP384SHA384:
	default:
		return ErrUnsupportedAlgorithm
	}
	return nil
}

// FindMatchingDNSKEY returns the validated DNSKEY whose key tag matches the
// RRSIG signer field, or a BogusError when no usable key exists.
func FindMatchingDNSKEY(keys []RR, keyTag uint16) (*DNSKEY, error) {
	var candidates []*DNSKEY
	for _, rr := range keys {
		if rr.Type != TypeDNSKEY {
			continue
		}
		key, err := ParseDNSKEY(rr.Rdata)
		if err != nil {
			continue
		}
		if err := ValidateDNSKEY(key); err != nil {
			continue
		}
		if DNSKEYKeyTag(key) == keyTag {
			candidates = append(candidates, key)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrBogus("no usable DNSKEY matches RRSIG key tag")
	}
	return candidates[0], nil
}

// VerifyDNSKEYByDS implements the DS -> DNSKEY trust anchor link of RFC 4035
// section 5.2: the child DNSKEY must be a valid zone key and its DS digest
// must match the parent-anchored DS record.
func VerifyDNSKEYByDS(dsRR RR, childOwner Name, dnskeyRR RR) error {
	if dsRR.Type != TypeDS {
		return ErrBogus("trust anchor record is not DS")
	}
	if dnskeyRR.Type != TypeDNSKEY {
		return ErrBogus("child record is not DNSKEY")
	}
	ds, err := ParseDS(dsRR.Rdata)
	if err != nil {
		return ErrBogus("malformed DS RDATA: " + err.Error())
	}
	key, err := ParseDNSKEY(dnskeyRR.Rdata)
	if err != nil {
		return ErrBogus("malformed DNSKEY RDATA: " + err.Error())
	}
	if err := ValidateDNSKEY(key); err != nil {
		return err
	}
	return VerifyDS(ds, childOwner, key)
}
