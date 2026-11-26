package hsm

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// ID identifies an HSM object.
type ID string

// KeyType identifies the algorithm family of a key.
type KeyType string

const (
	TypeAES KeyType = "AES"
	TypeRSA KeyType = "RSA"
	TypeEC  KeyType = "EC"
)

// ObjectClass is the PKCS#11 object class modelled by this package.
type ObjectClass string

const (
	ClassSecretKey  ObjectClass = "CKO_SECRET_KEY"
	ClassPrivateKey ObjectClass = "CKO_PRIVATE_KEY"
	ClassPublicKey  ObjectClass = "CKO_PUBLIC_KEY"
	ClassKeyPair    ObjectClass = "CKO_KEY_PAIR"
)

// KeySpec is the import representation of an HSM key object.
//
// Material is the secret/private bytes used by HSM operations. PublicMaterial
// is optional metadata for asymmetric keys and is never involved in symmetric
// wrapping. Both fields are JSON base64 encoded automatically.
type KeySpec struct {
	ID             ID          `json:"id"`
	Label          string      `json:"label,omitempty"`
	Class          ObjectClass `json:"class"`
	Type           KeyType     `json:"type"`
	Material       []byte      `json:"material"`
	PublicMaterial []byte      `json:"public_material,omitempty"`
	Attributes     Attributes  `json:"attributes"`
}

// Validate checks that the specification can be imported.
func (k KeySpec) Validate() error {
	_, err := normalizeKeySpec(k)
	return err
}

// KeyInfo is a non-secret view of an HSM key object.
type KeyInfo struct {
	ID             ID          `json:"id"`
	Label          string      `json:"label,omitempty"`
	Class          ObjectClass `json:"class"`
	Type           KeyType     `json:"type"`
	Attributes     Attributes  `json:"attributes"`
	MaterialLength int         `json:"material_length"`
	PublicLength   int         `json:"public_length,omitempty"`
	Origin         ID          `json:"origin,omitempty"`
}

type keyObject struct {
	spec   KeySpec
	origin ID
}

func (k *keyObject) info() KeyInfo {
	return KeyInfo{
		ID:             k.spec.ID,
		Label:          k.spec.Label,
		Class:          k.spec.Class,
		Type:           k.spec.Type,
		Attributes:     k.spec.Attributes,
		MaterialLength: len(k.spec.Material),
		PublicLength:   len(k.spec.PublicMaterial),
		Origin:         k.origin,
	}
}

func (k *keyObject) clone() *keyObject {
	spec := k.spec
	spec.Material = append([]byte(nil), k.spec.Material...)
	spec.PublicMaterial = append([]byte(nil), k.spec.PublicMaterial...)
	return &keyObject{spec: spec, origin: k.origin}
}

func (k *keyObject) materialFingerprint() [32]byte {
	return sha256.Sum256(k.spec.Material)
}

func normalizeKeySpec(spec KeySpec) (KeySpec, error) {
	if spec.ID == "" {
		return KeySpec{}, fmt.Errorf("%w: empty key identifier", ErrInvalidKey)
	}
	if spec.Type == "" {
		return KeySpec{}, fmt.Errorf("%w: key %q has no type", ErrInvalidKey, spec.ID)
	}
	if spec.Class == "" {
		switch spec.Type {
		case TypeAES:
			spec.Class = ClassSecretKey
		case TypeRSA, TypeEC:
			spec.Class = ClassKeyPair
		}
	}
	switch spec.Type {
	case TypeAES:
		if spec.Class != ClassSecretKey {
			return KeySpec{}, fmt.Errorf("%w: AES key %q must be a secret key", ErrInvalidKey, spec.ID)
		}
		switch len(spec.Material) {
		case 16, 24, 32:
		default:
			return KeySpec{}, fmt.Errorf("%w: AES key %q has %d bytes, want 16, 24, or 32",
				ErrInvalidKey, spec.ID, len(spec.Material))
		}
	case TypeRSA, TypeEC:
		if spec.Class != ClassKeyPair && spec.Class != ClassPrivateKey {
			return KeySpec{}, fmt.Errorf("%w: asymmetric key %q must be a key pair or private key",
				ErrInvalidKey, spec.ID)
		}
		if len(spec.Material) == 0 {
			return KeySpec{}, fmt.Errorf("%w: asymmetric key %q has no private material",
				ErrInvalidKey, spec.ID)
		}
	default:
		return KeySpec{}, fmt.Errorf("%w: unsupported key type %q", ErrInvalidKey, spec.Type)
	}
	if err := validateAttributeInvariants(spec.Attributes); err != nil {
		return KeySpec{}, fmt.Errorf("%w: key %q: %v", ErrInvalidKey, spec.ID, err)
	}
	spec.Material = append([]byte(nil), spec.Material...)
	spec.PublicMaterial = append([]byte(nil), spec.PublicMaterial...)
	return spec, nil
}

// KeyTemplate is used by UnwrapKey, CopyObject, and DeriveKey. Attributes is a
// complete requested vector for unwrap/derive; for CopyObject it is the
// complete attribute vector of the new object.
type KeyTemplate struct {
	ID             ID          `json:"id,omitempty"`
	Label          string      `json:"label,omitempty"`
	Class          ObjectClass `json:"class,omitempty"`
	Type           KeyType     `json:"type,omitempty"`
	MaterialLength int         `json:"material_length,omitempty"`
	Attributes     Attributes  `json:"attributes"`
}

func sortKeyInfos(infos []KeyInfo) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
}
