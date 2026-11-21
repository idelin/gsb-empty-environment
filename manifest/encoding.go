package manifest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// Canonical encoding tags. The encoding is a deterministic length-delimited
// binary format; every length is an unsigned 64-bit big-endian integer so the
// same value always produces identical bytes, independent of map ordering.
const (
	tagManifest   byte = 0x01
	tagFirmware   byte = 0x02
	tagString     byte = 0x10
	tagUint64     byte = 0x11
	tagTime       byte = 0x12
	tagBytes      byte = 0x13
	tagStringList byte = 0x14
	tagKeyList    byte = 0x15
	tagKeyEntry   byte = 0x16
)

func putU64(b *[]byte, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	*b = append(*b, buf[:]...)
}

func putTagLen(b *[]byte, tag byte, n int) {
	*b = append(*b, tag)
	putU64(b, uint64(n))
}

func putBytes(b *[]byte, tag byte, v []byte) {
	putTagLen(b, tag, len(v))
	*b = append(*b, v...)
}

func putString(b *[]byte, tag byte, v string) {
	putBytes(b, tag, []byte(v))
}

func putUint64(b *[]byte, tag byte, v uint64) {
	putTagLen(b, tag, 8)
	putU64(b, v)
}

func putTime(b *[]byte, tag byte, t time.Time) {
	putUint64(b, tag, uint64(t.UTC().UnixMilli()))
}

func putStringList(b *[]byte, tag byte, vs []string) {
	var inner []byte
	for _, v := range vs {
		putString(&inner, tagString, v)
	}
	putBytes(b, tag, inner)
}

// EncodeManifest produces the canonical manifest bytes covered by root
// signatures and used for the persisted digest. Key entries and revocation
// entries are sorted by keyID inside the encoding so the serialized form is
// independent of input order, but semantic validation still rejects duplicate
// keyIDs.
func EncodeManifest(m Manifest) ([]byte, error) {
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}

	revoked := append([]string(nil), m.RevokedKeyIDs...)
	sort.Strings(revoked)

	keys := append([]FirmwareKey(nil), m.Keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyID < keys[j].KeyID })

	var keyList []byte
	for _, k := range keys {
		var entry []byte
		putString(&entry, tagString, k.KeyID)
		putBytes(&entry, tagBytes, k.Public)
		putStringList(&entry, tagStringList, k.Purposes)
		putUint64(&entry, tagUint64, k.StartGeneration)
		putUint64(&entry, tagUint64, k.EndGeneration)
		putBytes(&keyList, tagKeyEntry, entry)
	}

	var body []byte
	putString(&body, tagString, m.DeviceModel)
	putUint64(&body, tagUint64, m.Generation)
	putTime(&body, tagTime, m.NotBefore)
	putTime(&body, tagTime, m.NotAfter)
	putUint64(&body, tagUint64, m.FirmwareThreshold)
	putBytes(&body, tagKeyList, keyList)
	putStringList(&body, tagStringList, revoked)

	var out []byte
	putBytes(&out, tagManifest, body)
	return out, nil
}

// EncodeFirmwareSigningInput produces the canonical bytes signed by firmware
// keys: device model, image digest, firmware version and the manifest
// generation the image was authorized under.
func EncodeFirmwareSigningInput(deviceModel string, imageDigest []byte, version string, manifestGeneration uint64) []byte {
	var body []byte
	putString(&body, tagString, deviceModel)
	putBytes(&body, tagBytes, imageDigest)
	putString(&body, tagString, version)
	putUint64(&body, tagUint64, manifestGeneration)

	var out []byte
	putBytes(&out, tagFirmware, body)
	return out
}

// ImageDigest returns the canonical digest of a firmware image.
func ImageDigest(image []byte) []byte {
	sum := sha256.Sum256(image)
	return sum[:]
}

// ManifestDigest returns the SHA-256 digest of the canonical manifest encoding.
func ManifestDigest(m Manifest) ([]byte, error) {
	encoded, err := EncodeManifest(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

// PublicKeyFingerprint returns the SHA-256 digest of raw public key material,
// used to collapse alias keyIDs carrying the same key.
func PublicKeyFingerprint(public ed25519.PublicKey) []byte {
	sum := sha256.Sum256(public)
	return sum[:]
}

// SignManifest signs the canonical manifest encoding with a root Ed25519 key.
func SignManifest(private ed25519.PrivateKey, m Manifest) ([]byte, error) {
	encoded, err := EncodeManifest(m)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(private, encoded), nil
}

// SignFirmware signs the canonical firmware descriptor with a firmware
// Ed25519 key.
func SignFirmware(private ed25519.PrivateKey, deviceModel string, imageDigest []byte, version string, manifestGeneration uint64) []byte {
	return ed25519.Sign(private, EncodeFirmwareSigningInput(deviceModel, imageDigest, version, manifestGeneration))
}

// ValidateManifest enforces structural rules independent of trusted state.
func ValidateManifest(m Manifest) error {
	if m.DeviceModel == "" {
		return fmt.Errorf("%w: empty device model", ErrInvalidManifest)
	}
	if !m.NotBefore.UTC().Before(m.NotAfter.UTC()) {
		return fmt.Errorf("%w: validity window must be left-closed/right-open with NotBefore < NotAfter", ErrInvalidManifest)
	}
	if m.FirmwareThreshold == 0 {
		return fmt.Errorf("%w: firmware threshold must be positive", ErrInvalidManifest)
	}

	seenKey := make(map[string]struct{}, len(m.Keys))
	for _, k := range m.Keys {
		if k.KeyID == "" {
			return fmt.Errorf("%w: empty keyID", ErrInvalidManifest)
		}
		if _, dup := seenKey[k.KeyID]; dup {
			return fmt.Errorf("%w: duplicate keyID %q", ErrInvalidManifest, k.KeyID)
		}
		seenKey[k.KeyID] = struct{}{}
		if len(k.Public) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: key %q has invalid Ed25519 public key size", ErrInvalidManifest, k.KeyID)
		}
		if len(k.Purposes) == 0 {
			return fmt.Errorf("%w: key %q has no purposes", ErrInvalidManifest, k.KeyID)
		}
		for _, p := range k.Purposes {
			if p == "" {
				return fmt.Errorf("%w: key %q has empty purpose", ErrInvalidManifest, k.KeyID)
			}
		}
		if k.StartGeneration >= k.EndGeneration {
			return fmt.Errorf("%w: key %q window must be left-closed/right-open", ErrInvalidManifest, k.KeyID)
		}
	}

	seenRevoked := make(map[string]struct{}, len(m.RevokedKeyIDs))
	for _, id := range m.RevokedKeyIDs {
		if id == "" {
			return fmt.Errorf("%w: empty revoked keyID", ErrInvalidManifest)
		}
		if _, dup := seenRevoked[id]; dup {
			return fmt.Errorf("%w: duplicate revoked keyID %q", ErrInvalidManifest, id)
		}
		seenRevoked[id] = struct{}{}
	}
	return nil
}

// decoder is used by the FileStore to re-verify persisted canonical bytes.
type decoder struct {
	data []byte
	pos  int
}

func (d *decoder) readTagLen(want byte) ([]byte, error) {
	if d.pos >= len(d.data) || d.data[d.pos] != want {
		return nil, fmt.Errorf("%w: expected tag %d", ErrCorruptState, want)
	}
	d.pos++
	if d.pos+8 > len(d.data) {
		return nil, fmt.Errorf("%w: truncated length", ErrCorruptState)
	}
	n := int(binary.BigEndian.Uint64(d.data[d.pos : d.pos+8]))
	d.pos += 8
	if n < 0 || d.pos+n > len(d.data) {
		return nil, fmt.Errorf("%w: length exceeds buffer", ErrCorruptState)
	}
	v := d.data[d.pos : d.pos+n]
	d.pos += n
	return v, nil
}

func (d *decoder) readString(want byte) (string, error) {
	v, err := d.readTagLen(want)
	return string(v), err
}

func (d *decoder) readUint64(want byte) (uint64, error) {
	v, err := d.readTagLen(want)
	if err != nil {
		return 0, err
	}
	if len(v) != 8 {
		return 0, fmt.Errorf("%w: bad uint64 width", ErrCorruptState)
	}
	return binary.BigEndian.Uint64(v), nil
}

// decodeCanonicalManifest reverses EncodeManifest and returns the manifest
// alongside its raw canonical bytes for digest comparison.
func decodeCanonicalManifest(raw []byte) (Manifest, error) {
	d := &decoder{data: raw}
	outer, err := d.readTagLen(tagManifest)
	if err != nil {
		return Manifest{}, err
	}
	if d.pos != len(raw) {
		return Manifest{}, fmt.Errorf("%w: trailing bytes", ErrCorruptState)
	}

	inner := &decoder{data: outer}
	m := Manifest{}
	if m.DeviceModel, err = inner.readString(tagString); err != nil {
		return Manifest{}, err
	}
	if m.Generation, err = inner.readUint64(tagUint64); err != nil {
		return Manifest{}, err
	}
	before, err := inner.readUint64(tagTime)
	if err != nil {
		return Manifest{}, err
	}
	after, err := inner.readUint64(tagTime)
	if err != nil {
		return Manifest{}, err
	}
	m.NotBefore = time.UnixMilli(int64(before)).UTC()
	m.NotAfter = time.UnixMilli(int64(after)).UTC()
	if m.FirmwareThreshold, err = inner.readUint64(tagUint64); err != nil {
		return Manifest{}, err
	}

	keyListRaw, err := inner.readTagLen(tagKeyList)
	if err != nil {
		return Manifest{}, err
	}
	kd := &decoder{data: keyListRaw}
	for kd.pos < len(keyListRaw) {
		entryRaw, err := kd.readTagLen(tagKeyEntry)
		if err != nil {
			return Manifest{}, err
		}
		ed := &decoder{data: entryRaw}
		k := FirmwareKey{}
		if k.KeyID, err = ed.readString(tagString); err != nil {
			return Manifest{}, err
		}
		pub, err := ed.readTagLen(tagBytes)
		if err != nil {
			return Manifest{}, err
		}
		k.Public = append([]byte(nil), pub...)
		purposeRaw, err := ed.readTagLen(tagStringList)
		if err != nil {
			return Manifest{}, err
		}
		pd := &decoder{data: purposeRaw}
		for pd.pos < len(purposeRaw) {
			p, err := pd.readString(tagString)
			if err != nil {
				return Manifest{}, err
			}
			k.Purposes = append(k.Purposes, p)
		}
		if k.StartGeneration, err = ed.readUint64(tagUint64); err != nil {
			return Manifest{}, err
		}
		if k.EndGeneration, err = ed.readUint64(tagUint64); err != nil {
			return Manifest{}, err
		}
		if ed.pos != len(entryRaw) {
			return Manifest{}, fmt.Errorf("%w: trailing key bytes", ErrCorruptState)
		}
		m.Keys = append(m.Keys, k)
	}

	revokedRaw, err := inner.readTagLen(tagStringList)
	if err != nil {
		return Manifest{}, err
	}
	rd := &decoder{data: revokedRaw}
	for rd.pos < len(revokedRaw) {
		id, err := rd.readString(tagString)
		if err != nil {
			return Manifest{}, err
		}
		m.RevokedKeyIDs = append(m.RevokedKeyIDs, id)
	}
	if inner.pos != len(outer) {
		return Manifest{}, fmt.Errorf("%w: trailing manifest bytes", ErrCorruptState)
	}

	reEncoded, err := EncodeManifest(m)
	if err != nil || !bytes.Equal(reEncoded, raw) {
		return Manifest{}, fmt.Errorf("%w: canonical re-encoding mismatch", ErrCorruptState)
	}
	return m, nil
}
