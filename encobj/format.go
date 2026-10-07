package encobj

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
)

// On-disk layout inside the store directory:
//
//	writer.lock                process exclusion (flock)
//	nonce.log                  high-water reservation records
//	ops.log                    idempotency records (rebuildable index)
//	obj-<id>/current           committed version index: version + manifest
//	                           file name + manifest digest + crc
//	obj-<id>/m<ver>-<h>.mf     AEAD-encrypted manifest (content addressed)
//	obj-<id>/f<fid>-r<rev>-<h>.ef  AEAD-encrypted field value
//
// Field file body:  "EF1" | keyID (len-prefixed) | nonce(12) | ciphertext
// Manifest body:    "EM1" | keyID (len-prefixed) | nonce(12) | ciphertext
//
// The AEAD additional data binds, via the unambiguous length-prefixed
// encoding in codec.go:
//   fields:   "encobj-field-v1", objID, fieldID, revision, schemaVer, keyID
//   manifest: "encobj-manifest-v1", objID, version, schemaVer, keyID

const schemaVersion = 1

const (
	fieldMagic    = "EF1"
	manifestMagic = "EM1"
)

func fieldAD(objID string, fieldID, revision uint64, keyID string) []byte {
	var e encoder
	e.str("encobj-field-v1")
	e.str(objID)
	e.u64(fieldID)
	e.u64(revision)
	e.u64(schemaVersion)
	e.str(keyID)
	return e.buf
}

func manifestAD(objID string, version uint64, keyID string) []byte {
	var e encoder
	e.str("encobj-manifest-v1")
	e.str(objID)
	e.u64(version)
	e.u64(schemaVersion)
	e.str(keyID)
	return e.buf
}

// fieldRef is one live field committed in a manifest.
type fieldRef struct {
	Name     string
	FieldID  uint64
	Revision uint64
	KeyID    string   // key that sealed File
	File     string   // content-addressed file name
	Digest   [32]byte // sha256 of the field file body
}

// manifest is the decrypted, verified manifest content.
type manifest struct {
	Version  uint64
	KeyID    string
	OpID     string
	OpDigest [32]byte
	Fields   []fieldRef // sorted by Name
	NextFID  uint64     // next never-used stable field ID
}

func (m *manifest) encode() []byte {
	var e encoder
	e.u64(schemaVersion)
	e.u64(m.Version)
	e.str(m.KeyID)
	e.str(m.OpID)
	e.bytes(m.OpDigest[:])
	e.u64(m.NextFID)
	e.u64(uint64(len(m.Fields)))
	for _, f := range m.Fields {
		e.str(f.Name)
		e.u64(f.FieldID)
		e.u64(f.Revision)
		e.str(f.KeyID)
		e.str(f.File)
		e.bytes(f.Digest[:])
	}
	return e.buf
}

func decodeManifest(b []byte) (*manifest, error) {
	d := &decoder{buf: b}
	m := &manifest{}
	if v := d.u64(); v != schemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema version %d", ErrCorrupt, v)
	}
	m.Version = d.u64()
	m.KeyID = d.str()
	m.OpID = d.str()
	copy(m.OpDigest[:], d.bytes())
	m.NextFID = d.u64()
	n := d.u64()
	if n > 1<<20 {
		return nil, fmt.Errorf("%w: absurd field count", ErrCorrupt)
	}
	for i := uint64(0); i < n; i++ {
		var f fieldRef
		f.Name = d.str()
		f.FieldID = d.u64()
		f.Revision = d.u64()
		f.KeyID = d.str()
		f.File = d.str()
		copy(f.Digest[:], d.bytes())
		m.Fields = append(m.Fields, f)
	}
	if !d.done() {
		return nil, fmt.Errorf("%w: manifest decode", ErrCorrupt)
	}
	if !sort.IsSorted(byName(m.Fields)) {
		return nil, fmt.Errorf("%w: manifest fields not canonical", ErrCorrupt)
	}
	return m, nil
}

type byName []fieldRef

func (b byName) Len() int           { return len(b) }
func (b byName) Less(i, j int) bool { return b[i].Name < b[j].Name }
func (b byName) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

func shortHash(b []byte) string {
	s := sha256.Sum256(b)
	return fmt.Sprintf("%x", s[:8])
}

func fieldFileName(fieldID, revision uint64, body []byte) string {
	return fmt.Sprintf("f%d-r%d-%s.ef", fieldID, revision, shortHash(body))
}

func manifestFileName(version uint64, body []byte) string {
	return fmt.Sprintf("m%d-%s.mf", version, shortHash(body))
}

// Object files live flat in the store directory so that every atomic
// rename and every directory fsync covers the whole store with one
// filesystem object. Arbitrary object IDs are hex-encoded into the name.
func objPrefix(objID string) string { return "obj-" + hex.EncodeToString([]byte(objID)) + "-" }

// splitObjFile splits "obj-<hexid>-<file>" into its parts.
func splitObjFile(name string) (objID, file string, ok bool) {
	if !strings.HasPrefix(name, "obj-") {
		return "", "", false
	}
	rest := name[4:]
	i := strings.IndexByte(rest, '-')
	if i < 0 {
		return "", "", false
	}
	id, err := hex.DecodeString(rest[:i])
	if err != nil {
		return "", "", false
	}
	return string(id), rest[i+1:], true
}

// sealFile encrypts plaintext and wraps it with magic, key ID and nonce.
func sealFile(magic, keyID string, nonce, plaintext, ad []byte, aead cipher.AEAD) []byte {
	var e encoder
	e.str(magic)
	e.str(keyID)
	e.bytes(nonce)
	out := append([]byte(nil), e.buf...)
	return aead.Seal(out, nonce, plaintext, ad)
}

// openSealed unwraps and authenticates a sealed file. The header is parsed
// first; the caller builds the AEAD additional data from the header's key
// ID, so a tampered header changes the AD/nonce inputs and fails
// authentication. Returns the header key ID, nonce and plaintext.
func openSealed(expectMagic string, body []byte, adFunc func(keyID string) []byte, keys *keyRegistry) (string, []byte, []byte, error) {
	d := &decoder{buf: body}
	magic := d.str()
	keyID := d.str()
	nonce := d.bytes()
	if d.err != nil || magic != expectMagic || len(nonce) != 12 {
		return "", nil, nil, fmt.Errorf("%w: bad %s envelope", ErrCorrupt, expectMagic)
	}
	e, err := keys.lookup(keyID)
	if err != nil {
		return "", nil, nil, err
	}
	pt, err := e.aead.Open(nil, nonce, d.buf, adFunc(keyID))
	if err != nil {
		return "", nil, nil, ErrAuth
	}
	return keyID, nonce, pt, nil
}

// currentRecord is the committed-version index for one object. It is the
// sole authority for which manifest is current; AEAD alone never decides
// freshness.
type currentRecord struct {
	Version        uint64
	ManifestFile   string
	ManifestDigest [32]byte
}

func encodeCurrent(c *currentRecord) []byte {
	var e encoder
	e.str("encobj-current-v1")
	e.u64(c.Version)
	e.str(c.ManifestFile)
	e.bytes(c.ManifestDigest[:])
	sum := sha256.Sum256(e.buf)
	return append(e.buf, sum[:]...)
}

func decodeCurrent(b []byte) (*currentRecord, error) {
	if len(b) < sha256.Size {
		return nil, fmt.Errorf("%w: short current record", ErrCorrupt)
	}
	body, sum := b[:len(b)-sha256.Size], b[len(b)-sha256.Size:]
	if sha256.Sum256(body) != [32]byte(sum) {
		return nil, fmt.Errorf("%w: current record checksum", ErrCorrupt)
	}
	d := &decoder{buf: body}
	c := &currentRecord{}
	if tag := d.str(); tag != "encobj-current-v1" {
		return nil, fmt.Errorf("%w: current record tag", ErrCorrupt)
	}
	c.Version = d.u64()
	c.ManifestFile = d.str()
	copy(c.ManifestDigest[:], d.bytes())
	if !d.done() {
		return nil, fmt.Errorf("%w: current record decode", ErrCorrupt)
	}
	return c, nil
}

// ops.log record: crc32 | version u64 | opID (len-prefixed) | opDigest(32)
// The log is a rebuildable index; the authoritative idempotency record for
// the latest commit also lives inside that commit's manifest.
func encodeOpRec(version uint64, opID string, digest [32]byte) []byte {
	var e encoder
	e.u64(version)
	e.str(opID)
	e.bytes(digest[:])
	var rec [4]byte
	binary.LittleEndian.PutUint32(rec[:], crc32.ChecksumIEEE(e.buf))
	return append(rec[:], e.buf...)
}
