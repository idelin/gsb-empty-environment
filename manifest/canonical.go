package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// Domain-separation prefixes. Every signed byte string starts with a fixed tag
// so a signature produced for one object cannot be replayed against another.
const (
	domainManifest = "FWMN-v1"
	domainFirmware = "FWIM-v1"
)

// CanonicalEncoding returns the deterministic byte encoding of a manifest.
//
// The encoding is a sequence of length-prefixed fields with big-endian
// integers and domain tags. All sets and maps are emitted in sorted order, so
// semantically equal manifests always produce identical bytes regardless of
// caller-side ordering. Root signatures are never part of the encoding: each
// root signs exactly this representation of the manifest body.
func CanonicalEncoding(m *Manifest) []byte {
	if m == nil {
		return nil
	}
	var b bytes.Buffer
	putBytes(&b, []byte(domainManifest))
	putBytes(&b, []byte(m.DeviceModel))
	putU64(&b, m.Generation)
	putU64(&b, uint64(m.NotBefore.UTC().Unix()))
	putU64(&b, uint64(m.NotAfter.UTC().Unix()))
	putU64(&b, m.Threshold)

	entries := make([]KeyEntry, len(m.Entries))
	copy(entries, m.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].KeyID < entries[j].KeyID })

	putU64(&b, uint64(len(entries)))
	for _, e := range entries {
		putBytes(&b, []byte(e.KeyID))
		putBytes(&b, e.PubKey)

		purposes := append([]string(nil), e.Purposes...)
		sort.Strings(purposes)
		putU64(&b, uint64(len(purposes)))
		for _, p := range purposes {
			putBytes(&b, []byte(p))
		}

		putU64(&b, e.GenStart)
		putU64(&b, e.GenStop)
	}

	revoked := append([]string(nil), m.RevokedKeyIDs...)
	sort.Strings(revoked)
	putU64(&b, uint64(len(revoked)))
	for _, id := range revoked {
		putBytes(&b, []byte(id))
	}

	return b.Bytes()
}

// Digest returns the SHA-256 digest of the manifest's canonical encoding.
// The digest is the value persisted in trusted storage for same-generation
// fork detection.
func ManifestDigest(m *Manifest) []byte {
	sum := sha256.Sum256(CanonicalEncoding(m))
	return sum[:]
}

// FirmwareSigningBytes returns the exact byte string that a firmware key must
// sign. It covers the device model, image digest, firmware version and the
// manifest generation, under its own domain tag.
func FirmwareSigningBytes(img *FirmwareImage) []byte {
	var b bytes.Buffer
	putBytes(&b, []byte(domainFirmware))
	putBytes(&b, []byte(img.DeviceModel))
	putBytes(&b, img.ImageDigest)
	putBytes(&b, []byte(img.Version))
	putU64(&b, img.Generation)
	return b.Bytes()
}

func putU64(b *bytes.Buffer, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	b.Write(buf[:])
}

func putBytes(b *bytes.Buffer, v []byte) {
	putU64(b, uint64(len(v)))
	b.Write(v)
}
