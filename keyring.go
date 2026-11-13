package netsec

import (
	"fmt"
	"io"
)

// Identity is a User ID or User Attribute packet together with the signatures
// that follow it.
type Identity struct {
	UserID        string
	UserAttribute []byte
	Signatures    []*Signature
}

// Subkey is a public subkey and its binding/revocation signatures.
type Subkey struct {
	Key         *Key
	Signatures  []*Signature
	Revocations []*Signature
}

// Entity is a primary public key and the packets associated with it.
type Entity struct {
	PrimaryKey *Key

	// DirectSignatures are signatures made directly over the primary key
	// (including direct-key self-signatures).
	DirectSignatures []*Signature

	Identities []*Identity
	Subkeys    []*Subkey

	// KeyRevocations contains key revocation signatures (type 0x20) for
	// PrimaryKey, regardless of where they appeared in the key block.
	KeyRevocations []*Signature
}

// Fingerprint returns the full fingerprint of the entity's primary key.
func (e *Entity) Fingerprint() []byte {
	if e == nil || e.PrimaryKey == nil {
		return nil
	}
	return e.PrimaryKey.Fingerprint
}

// Keyring is a collection of OpenPGP entities indexed by full fingerprint and
// by the deliberately ambiguous 64-bit key ID.
type Keyring struct {
	Entities []*Entity

	byFingerprint map[string][]*Entity
	byKeyID       map[string][]*Entity
}

// NewKeyring creates an empty keyring.  Entities can be added with AddEntity.
func NewKeyring(entities ...*Entity) *Keyring {
	kr := &Keyring{}
	for _, entity := range entities {
		kr.AddEntity(entity)
	}
	return kr
}

// ParseKeyring parses binary or ASCII-armored OpenPGP public key data.
func ParseKeyring(data []byte) (*Keyring, error) {
	binaryData, err := decodeKeyring(data)
	if err != nil {
		return nil, err
	}
	packets, err := parsePackets(binaryData)
	if err != nil {
		return nil, err
	}

	kr := &Keyring{}
	var current *Entity
	var lastIdentity *Identity
	var lastSubkey *Subkey

	for _, p := range packets {
		switch p.tag {
		case 6: // public key
			key, err := parsePublicKeyPacket(p.tag, p.body)
			if err != nil {
				return nil, err
			}
			current = &Entity{PrimaryKey: key}
			lastIdentity = nil
			lastSubkey = nil
			kr.Entities = append(kr.Entities, current)
		case 13: // user ID
			if current == nil {
				return nil, fmt.Errorf("%w: user ID before public key", ErrInvalidPacket)
			}
			identity := &Identity{UserID: string(p.body)}
			current.Identities = append(current.Identities, identity)
			lastIdentity = identity
			lastSubkey = nil
		case 17: // user attribute
			if current == nil {
				return nil, fmt.Errorf("%w: user attribute before public key", ErrInvalidPacket)
			}
			identity := &Identity{UserAttribute: append([]byte(nil), p.body...)}
			current.Identities = append(current.Identities, identity)
			lastIdentity = identity
			lastSubkey = nil
		case 14: // public subkey
			if current == nil {
				return nil, fmt.Errorf("%w: public subkey before public key", ErrInvalidPacket)
			}
			key, err := parsePublicKeyPacket(p.tag, p.body)
			if err != nil {
				return nil, err
			}
			subkey := &Subkey{Key: key}
			current.Subkeys = append(current.Subkeys, subkey)
			lastSubkey = subkey
			lastIdentity = nil
		case 2: // signature
			if current == nil {
				return nil, fmt.Errorf("%w: signature before public key", ErrInvalidPacket)
			}
			sig, err := parseSignaturePacket(p.body)
			if err != nil {
				return nil, err
			}
			switch sig.Type {
			case SigKeyRevocation:
				current.KeyRevocations = append(current.KeyRevocations, sig)
			case SigSubkeyRevocation:
				if lastSubkey == nil {
					return nil, fmt.Errorf("%w: subkey revocation without subkey", ErrInvalidPacket)
				}
				lastSubkey.Revocations = append(lastSubkey.Revocations, sig)
			default:
				switch {
				case lastSubkey != nil:
					lastSubkey.Signatures = append(lastSubkey.Signatures, sig)
				case lastIdentity != nil:
					lastIdentity.Signatures = append(lastIdentity.Signatures, sig)
				default:
					current.DirectSignatures = append(current.DirectSignatures, sig)
				}
			}
		case 5, 7: // secret key or secret subkey
			return nil, fmt.Errorf("%w: secret key packets are not accepted", ErrInvalidPacket)
		}
	}
	if len(kr.Entities) == 0 {
		return nil, fmt.Errorf("%w: no public key found", ErrKeyNotFound)
	}
	kr.reindex()
	return kr, nil
}

// LoadKeyring reads binary or ASCII-armored OpenPGP public key data from r.
func LoadKeyring(r io.Reader) (*Keyring, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPacketBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPacketBody {
		return nil, fmt.Errorf("%w: keyring is too large", ErrInvalidPacket)
	}
	return ParseKeyring(data)
}

// ParseKey parses a key block and returns its primary public key.
func ParseKey(data []byte) (*Key, error) {
	kr, err := ParseKeyring(data)
	if err != nil {
		return nil, err
	}
	if len(kr.Entities) != 1 {
		return nil, fmt.Errorf("%w: expected one public key, found %d", ErrInvalidPacket, len(kr.Entities))
	}
	return kr.Entities[0].PrimaryKey, nil
}

// AddEntity adds an entity to the keyring and updates its indexes.
func (kr *Keyring) AddEntity(entity *Entity) {
	if kr == nil || entity == nil || entity.PrimaryKey == nil {
		return
	}
	kr.Entities = append(kr.Entities, entity)
	if kr.byFingerprint == nil {
		kr.reindex()
		return
	}
	kr.indexEntity(entity)
}

// RebuildIndex refreshes the lookup indexes after Entities has been modified
// directly.
func (kr *Keyring) RebuildIndex() {
	if kr != nil {
		kr.reindex()
	}
}

func (kr *Keyring) reindex() {
	kr.byFingerprint = make(map[string][]*Entity)
	kr.byKeyID = make(map[string][]*Entity)
	for _, entity := range kr.Entities {
		kr.indexEntity(entity)
	}
}

func (kr *Keyring) indexEntity(entity *Entity) {
	if entity == nil || entity.PrimaryKey == nil {
		return
	}
	fp := string(entity.PrimaryKey.Fingerprint)
	id := string(entity.PrimaryKey.KeyID)
	kr.byFingerprint[fp] = append(kr.byFingerprint[fp], entity)
	kr.byKeyID[id] = append(kr.byKeyID[id], entity)
}

// LookupByFingerprint returns the entity whose complete fingerprint matches fp.
// It never performs prefix or key-ID matching.
func (kr *Keyring) LookupByFingerprint(fp []byte) *Entity {
	matches := kr.LookupAllByFingerprint(fp)
	if len(matches) == 0 {
		return nil
	}
	return matches[0]
}

// LookupAllByFingerprint returns all entities matching the complete
// fingerprint.  Multiple entities with one fingerprint represent duplicate
// imports of the same key.
func (kr *Keyring) LookupAllByFingerprint(fp []byte) []*Entity {
	if kr == nil || (len(fp) != 20 && len(fp) != 32) {
		return nil
	}
	kr.ensureIndex()
	return append([]*Entity(nil), kr.byFingerprint[string(fp)]...)
}

// LookupByKeyID returns all entities with the given 64-bit key ID.  The result
// is intentionally a slice because key IDs are not unique; callers must not use
// this method to authorize a designated revoker.
func (kr *Keyring) LookupByKeyID(id []byte) []*Entity {
	if kr == nil || len(id) != 8 {
		return nil
	}
	kr.ensureIndex()
	return append([]*Entity(nil), kr.byKeyID[string(id)]...)
}

func (kr *Keyring) ensureIndex() {
	if kr.byFingerprint == nil {
		kr.reindex()
	}
}

// KeysByFingerprint returns primary public keys with the complete fingerprint.
func (kr *Keyring) KeysByFingerprint(fp []byte) []*Key {
	entities := kr.LookupAllByFingerprint(fp)
	keys := make([]*Key, 0, len(entities))
	for _, entity := range entities {
		keys = append(keys, entity.PrimaryKey)
	}
	return keys
}
