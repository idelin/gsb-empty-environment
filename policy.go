package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

func policyFingerprint(p Policy) string {
	n := p.normalized()
	var b bytes.Buffer
	writeCanonicalString(&b, n.DomainID)
	writeCanonicalUint64(&b, n.Revision)
	writeCanonicalString(&b, n.SSID)
	writeCanonicalStrings(&b, n.AllowedBSSIDs)
	writeCanonicalStrings(&b, n.AllowedServerIdentities)
	writeCanonicalUint16s(&b, n.AllowedTLSVersions)
	writeCanonicalUint16s(&b, n.AllowedCipherSuites)
	writeCanonicalUint16s(&b, n.AllowedVLANs)
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:])
}

func policiesEqual(a, b Policy) bool {
	return policyFingerprint(a) == policyFingerprint(b)
}

// policyTightened reports whether b is a restriction relative to a. A revision
// increase is treated as a tightening because revisions in this component are
// expected to advance monotonically as policy is made stricter. When the
// revision is unchanged, subset relations on every allow-list decide.
func policyTightened(oldPolicy, newPolicy Policy) bool {
	oldPolicy = oldPolicy.normalized()
	newPolicy = newPolicy.normalized()

	if newPolicy.Revision > oldPolicy.Revision {
		return true
	}
	if newPolicy.Revision < oldPolicy.Revision {
		return false
	}
	if newPolicy.DomainID != oldPolicy.DomainID || newPolicy.SSID != oldPolicy.SSID {
		return false
	}

	return allStringsSubset(newPolicy.AllowedBSSIDs, oldPolicy.AllowedBSSIDs) &&
		allStringsSubset(newPolicy.AllowedServerIdentities, oldPolicy.AllowedServerIdentities) &&
		allUint16Subset(newPolicy.AllowedTLSVersions, oldPolicy.AllowedTLSVersions) &&
		allUint16Subset(newPolicy.AllowedCipherSuites, oldPolicy.AllowedCipherSuites) &&
		allUint16Subset(newPolicy.AllowedVLANs, oldPolicy.AllowedVLANs)
}

func writeCanonicalString(b *bytes.Buffer, s string) {
	writeCanonicalUint64(b, uint64(len(s)))
	b.WriteString(s)
}

func writeCanonicalStrings(b *bytes.Buffer, values []string) {
	writeCanonicalUint64(b, uint64(len(values)))
	for _, s := range values {
		writeCanonicalString(b, s)
	}
}

func writeCanonicalUint16s(b *bytes.Buffer, values []uint16) {
	writeCanonicalUint64(b, uint64(len(values)))
	for _, v := range values {
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], v)
		b.Write(buf[:])
	}
}

func writeCanonicalUint64(b *bytes.Buffer, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	b.Write(buf[:])
}

func policyChangeError(oldPolicy, newPolicy Policy) error {
	if policiesEqual(oldPolicy, newPolicy) {
		return nil
	}
	if policyTightened(oldPolicy, newPolicy) {
		return ErrPolicyTightened
	}
	return ErrPolicyChanged
}
