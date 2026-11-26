package hsm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
)

type blobRecord struct {
	wrappingKey      ID
	sourceKey        ID
	sourceOrigin     ID
	sourceClass      ObjectClass
	sourceType       KeyType
	sourceAttributes Attributes
	mechanism        Mechanism
}

// BlobMetadata describes the provenance of a wrapped blob created by this
// HSM instance. It contains no key material.
type BlobMetadata struct {
	WrappingKey      ID          `json:"wrapping_key"`
	SourceKey        ID          `json:"source_key"`
	SourceOrigin     ID          `json:"source_origin"`
	SourceClass      ObjectClass `json:"source_class"`
	SourceType       KeyType     `json:"source_type"`
	SourceAttributes Attributes  `json:"source_attributes"`
	Mechanism        Mechanism   `json:"mechanism"`
}

// HSM is an in-process PKCS#11 policy adjudicator and real AES mechanism
// implementation. Its zero value is not usable; construct it with New or
// NewHardened.
type HSM struct {
	mu            sync.RWMutex
	policy        Policy
	keys          map[ID]*keyObject
	records       map[[32]byte]blobRecord
	permissions   Permissions
	seq           uint64
	deterministic bool
}

// New creates an HSM with policy and imports specs. The variadic form accepts
// zero or more KeySpec values; use Load when a slice is more convenient.
func New(policy Policy, specs ...KeySpec) (*HSM, error) {
	h := &HSM{
		policy:      policy,
		keys:        make(map[ID]*keyObject),
		records:     make(map[[32]byte]blobRecord),
		permissions: AllPermissions(),
	}
	for _, spec := range specs {
		if err := h.AddKey(spec); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// NewHSM is an alias for New.
func NewHSM(policy Policy, specs ...KeySpec) (*HSM, error) { return New(policy, specs...) }

// Load creates an HSM from a slice of key specifications.
func Load(policy Policy, specs []KeySpec) (*HSM, error) { return New(policy, specs...) }

// NewHardened creates an HSM with the strict zero-value policy.
func NewHardened(specs ...KeySpec) (*HSM, error) { return New(HardenedPolicy(), specs...) }

// Policy returns the active policy.
func (h *HSM) Policy() Policy {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.policy
}

// SetPermissions replaces the default caller capability set. It is an
// administrative operation and is not itself permission checked.
func (h *HSM) SetPermissions(p Permissions) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.permissions = p.clone()
}

// Permissions returns a copy of the default caller capability set.
func (h *HSM) Permissions() Permissions {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.permissions.clone()
}

// AddKey imports a key object. Legacy configurations that already contain a
// WRAP+DECRYPT overlap are accepted, but hardened operations on such an object
// are denied. No secret material is returned by read APIs.
func (h *HSM) AddKey(spec KeySpec) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addKeyLocked(spec, spec.ID)
}

func (h *HSM) addKeyLocked(spec KeySpec, origin ID) error {
	normalized, err := normalizeKeySpec(spec)
	if err != nil {
		return err
	}
	if _, exists := h.keys[normalized.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateID, normalized.ID)
	}
	if err := h.validateDuplicateMaterialLocked(normalized, origin); err != nil {
		return err
	}
	h.keys[normalized.ID] = &keyObject{spec: normalized, origin: origin}
	return nil
}

func (h *HSM) validateDuplicateMaterialLocked(spec KeySpec, origin ID) error {
	if h.policy.AllowDuplicateKeyMaterial {
		return nil
	}
	fp := sha256.Sum256(spec.Material)
	for _, existing := range h.keys {
		if existing.materialFingerprint() != fp {
			continue
		}
		if existing.origin == origin {
			continue
		}
		a, b := existing.spec.Attributes, spec.Attributes
		if existing.spec.Type != spec.Type ||
			existing.spec.Class != spec.Class ||
			a.UsageRoles() != b.UsageRoles() ||
			a.Sensitive != b.Sensitive ||
			a.Extractable != b.Extractable {
			return fmt.Errorf("%w: secret material is reused by %q with a different policy vector",
				ErrPolicy, existing.spec.ID)
		}
	}
	return nil
}

// GetKey returns non-secret metadata for one object.
func (h *HSM) GetKey(id ID) (KeyInfo, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	key, ok := h.keys[id]
	if !ok {
		return KeyInfo{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return key.info(), nil
}

// ListKeys returns all objects sorted by identifier.
func (h *HSM) ListKeys() []KeyInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	infos := make([]KeyInfo, 0, len(h.keys))
	for _, key := range h.keys {
		infos = append(infos, key.info())
	}
	sortKeyInfos(infos)
	return infos
}

// InspectBlob returns provenance recorded for a blob created by WrapKey.
func (h *HSM) InspectBlob(blob []byte) (BlobMetadata, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	record, ok := h.records[sha256.Sum256(blob)]
	if !ok {
		return BlobMetadata{}, false
	}
	return BlobMetadata{
		WrappingKey:      record.wrappingKey,
		SourceKey:        record.sourceKey,
		SourceOrigin:     record.sourceOrigin,
		SourceClass:      record.sourceClass,
		SourceType:       record.sourceType,
		SourceAttributes: record.sourceAttributes,
		Mechanism:        record.mechanism,
	}, true
}

func (h *HSM) checkOpLocked(p Permissions, op Operation, keyID ID) error {
	if !p.Allows(op, keyID) {
		return fmt.Errorf("%w: %s on %q", ErrPermission, op, keyID)
	}
	return nil
}

func (h *HSM) keyLocked(id ID) (*keyObject, error) {
	key, ok := h.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return key, nil
}

func (h *HSM) nextSeqLocked() uint64 {
	h.seq++
	return h.seq
}

func (h *HSM) generatedIDLocked(prefix string) (ID, error) {
	for {
		h.seq++
		id := ID(fmt.Sprintf("%s-%d", prefix, h.seq))
		if _, exists := h.keys[id]; !exists {
			return id, nil
		}
	}
}

func (h *HSM) ivForLocked(mech Mechanism) ([]byte, error) {
	var size int
	switch mech {
	case AESKeyWrap:
		return nil, nil
	case AESCBC:
		size = 16
	case AESGCM:
		size = 12
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMechanism, mech)
	}
	iv := make([]byte, size)
	if h.deterministic {
		seq := h.nextSeqLocked()
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], seq)
		sum := sha256.Sum256(append([]byte(mech), counter[:]...))
		copy(iv, sum[:size])
		return iv, nil
	}
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("%w: random IV: %v", ErrCrypto, err)
	}
	return iv, nil
}

// WrapKey calls C_WrapKey semantics using the default permission set.
func (h *HSM) WrapKey(wrappingKey, target ID, mech Mechanism) ([]byte, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.wrapKey(p, wrappingKey, target, mech)
}

func (h *HSM) wrapKey(p Permissions, wrappingKeyID, targetID ID, mech Mechanism) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpWrapKey, wrappingKeyID); err != nil {
		return nil, err
	}
	if !p.Allows(OpWrapKey, targetID) {
		return nil, fmt.Errorf("%w: WrapKey cannot address target %q", ErrPermission, targetID)
	}
	wrappingKey, err := h.keyLocked(wrappingKeyID)
	if err != nil {
		return nil, err
	}
	mech, err = ParseMechanism(string(mech))
	if err != nil {
		return nil, err
	}
	target, err := h.keyLocked(targetID)
	if err != nil {
		return nil, err
	}
	if wrappingKey.spec.Type != TypeAES || !mech.IsAES() {
		return nil, fmt.Errorf("%w: AES wrapping key and mechanism required", ErrUnsupportedMechanism)
	}
	if !wrappingKey.spec.Attributes.Wrap {
		return nil, fmt.Errorf("%w: CKA_WRAP=false on %q", ErrPolicy, wrappingKeyID)
	}
	if err := h.policy.operationRoleAllowed(wrappingKey.spec.Attributes, OpWrapKey); err != nil {
		return nil, err
	}
	if target.spec.Class == ClassPublicKey {
		return nil, fmt.Errorf("%w: cannot wrap public key %q", ErrPolicy, targetID)
	}
	if !target.spec.Attributes.Extractable && !h.policy.AllowWrapOfNonExtractable {
		return nil, fmt.Errorf("%w: %q", ErrNotExtractable, targetID)
	}
	if target.spec.Attributes.WrapWithTrusted && !wrappingKey.spec.Attributes.Trusted &&
		!h.policy.AllowUntrustedWrapWithTrusted {
		return nil, fmt.Errorf("%w: %q requires CKA_TRUSTED wrapper", ErrUntrustedWrapper, targetID)
	}
	iv, err := h.ivForLocked(mech)
	if err != nil {
		return nil, err
	}
	blob, err := encryptWithMechanism(mech, wrappingKey.spec.Material, target.spec.Material, iv)
	if err != nil {
		return nil, err
	}
	h.records[sha256.Sum256(blob)] = blobRecord{
		wrappingKey:      wrappingKeyID,
		sourceKey:        targetID,
		sourceOrigin:     target.origin,
		sourceClass:      target.spec.Class,
		sourceType:       target.spec.Type,
		sourceAttributes: target.spec.Attributes,
		mechanism:        mech,
	}
	return append([]byte(nil), blob...), nil
}

// UnwrapKey calls C_UnwrapKey semantics using the default permission set.
func (h *HSM) UnwrapKey(unwrappingKey ID, blob []byte, template KeyTemplate, mech Mechanism) (ID, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.unwrapKey(p, unwrappingKey, blob, template, mech)
}

func (h *HSM) unwrapKey(p Permissions, unwrappingKeyID ID, blob []byte, template KeyTemplate, mech Mechanism) (ID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpUnwrapKey, unwrappingKeyID); err != nil {
		return "", err
	}
	unwrappingKey, err := h.keyLocked(unwrappingKeyID)
	if err != nil {
		return "", err
	}
	mech, err = ParseMechanism(string(mech))
	if err != nil {
		return "", err
	}
	if unwrappingKey.spec.Type != TypeAES || !mech.IsAES() {
		return "", fmt.Errorf("%w: AES unwrapping key and mechanism required", ErrUnsupportedMechanism)
	}
	if !unwrappingKey.spec.Attributes.Unwrap {
		return "", fmt.Errorf("%w: CKA_UNWRAP=false on %q", ErrPolicy, unwrappingKeyID)
	}
	if err := h.policy.operationRoleAllowed(unwrappingKey.spec.Attributes, OpUnwrapKey); err != nil {
		return "", err
	}

	record, tracked := h.records[sha256.Sum256(blob)]
	actualMech := mech
	if tracked {
		if recordWrapper, ok := h.keys[record.wrappingKey]; ok &&
			!equalMaterialFingerprint(unwrappingKey, recordWrapper) {
			return "", fmt.Errorf("%w: unwrapping key material does not match the recorded wrapper", ErrCrypto)
		}
		if mech != record.mechanism && !h.policy.AllowMechanismConfusion {
			return "", fmt.Errorf("%w: blob was created with %s, requested %s",
				ErrMechanismMismatch, record.mechanism, mech)
		}
		if h.policy.AllowMechanismConfusion {
			actualMech = record.mechanism
		}
	}
	material, err := decryptWithMechanism(actualMech, unwrappingKey.spec.Material, blob)
	if err != nil {
		return "", err
	}

	attrs := template.Attributes
	if err := validateAttributeInvariants(attrs); err != nil {
		return "", err
	}
	if tracked {
		source := record.sourceAttributes
		if !h.policy.AllowUnwrapTemplateDowngrade {
			if source.Sensitive && !attrs.Sensitive {
				return "", fmt.Errorf("%w: unwrap template cannot clear CKA_SENSITIVE", ErrMonotonicity)
			}
			if !source.Extractable && attrs.Extractable {
				return "", fmt.Errorf("%w: unwrap template cannot set CKA_EXTRACTABLE", ErrMonotonicity)
			}
			if source.AlwaysSensitive && !attrs.AlwaysSensitive {
				return "", fmt.Errorf("%w: unwrap template cannot clear CKA_ALWAYS_SENSITIVE", ErrMonotonicity)
			}
			if source.NeverExtractable && !attrs.NeverExtractable {
				return "", fmt.Errorf("%w: unwrap template cannot clear CKA_NEVER_EXTRACTABLE", ErrMonotonicity)
			}
			if source.WrapWithTrusted && !attrs.WrapWithTrusted {
				return "", fmt.Errorf("%w: unwrap template cannot clear CKA_WRAP_WITH_TRUSTED", ErrMonotonicity)
			}
		}
		if !h.policy.AllowRoleMutation && usageRolesChanged(source, attrs) {
			return "", fmt.Errorf("%w: unwrap template changes usage roles from %s to %s",
				ErrRolePromotion, source.UsageRoles().shortString(), attrs.UsageRoles().shortString())
		}
		if !h.policy.AllowUnwrapTemplateDowngrade {
			attrs.AlwaysSensitive = source.AlwaysSensitive && attrs.Sensitive
			attrs.NeverExtractable = source.NeverExtractable && !attrs.Extractable
		}
	} else {
		if !h.policy.AllowUntrustedPermissiveUnwrap && !unwrappingKey.spec.Attributes.Trusted &&
			(!attrs.Sensitive || attrs.Extractable) {
			return "", fmt.Errorf("%w: untrusted unwrap cannot create a permissive template", ErrPolicy)
		}
		attrs.AlwaysSensitive = attrs.Sensitive
		attrs.NeverExtractable = !attrs.Extractable
	}
	if err := validateAttributeInvariants(attrs); err != nil {
		return "", err
	}
	if err := h.policy.validateRoleOverlap(attrs); err != nil {
		return "", err
	}
	if err := h.applyTemplateTrustLocked(p, attrs); err != nil {
		return "", err
	}

	class, keyType := template.Class, template.Type
	if class == "" && tracked {
		class = record.sourceClass
	}
	if keyType == "" && tracked {
		keyType = record.sourceType
	}
	if class == "" {
		class = ClassSecretKey
	}
	if keyType == "" {
		keyType = TypeAES
	}
	if template.MaterialLength > 0 && len(material) != template.MaterialLength {
		return "", fmt.Errorf("%w: unwrapped material is %d bytes, template requests %d",
			ErrInvalidKey, len(material), template.MaterialLength)
	}
	id := template.ID
	if id == "" {
		id, err = h.generatedIDLocked("unwrapped")
		if err != nil {
			return "", err
		}
	}
	if _, exists := h.keys[id]; exists {
		return "", fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}
	origin := id
	if tracked {
		origin = record.sourceOrigin
	}
	spec := KeySpec{
		ID:         id,
		Label:      template.Label,
		Class:      class,
		Type:       keyType,
		Material:   material,
		Attributes: attrs,
	}
	if err := h.addKeyLocked(spec, origin); err != nil {
		return "", err
	}
	return id, nil
}

func (h *HSM) applyTemplateTrustLocked(p Permissions, attrs Attributes) error {
	if !attrs.Trusted {
		return nil
	}
	if !p.CanSetTrusted {
		return fmt.Errorf("%w: CKA_TRUSTED=true requires SO permission", ErrPermission)
	}
	return nil
}

// Encrypt calls C_Encrypt semantics using the default permission set.
func (h *HSM) Encrypt(keyID ID, plaintext []byte, mech Mechanism) ([]byte, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.encrypt(p, keyID, plaintext, mech)
}

func (h *HSM) encrypt(p Permissions, keyID ID, plaintext []byte, mech Mechanism) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpEncrypt, keyID); err != nil {
		return nil, err
	}
	key, err := h.keyLocked(keyID)
	if err != nil {
		return nil, err
	}
	mech, err = ParseMechanism(string(mech))
	if err != nil {
		return nil, err
	}
	if key.spec.Type != TypeAES || !mech.IsAES() {
		return nil, fmt.Errorf("%w: AES key and mechanism required", ErrUnsupportedMechanism)
	}
	if !key.spec.Attributes.Encrypt {
		return nil, fmt.Errorf("%w: CKA_ENCRYPT=false on %q", ErrPolicy, keyID)
	}
	if err := h.policy.operationRoleAllowed(key.spec.Attributes, OpEncrypt); err != nil {
		return nil, err
	}
	iv, err := h.ivForLocked(mech)
	if err != nil {
		return nil, err
	}
	return encryptWithMechanism(mech, key.spec.Material, plaintext, iv)
}

// Decrypt calls C_Decrypt semantics using the default permission set.
func (h *HSM) Decrypt(keyID ID, ciphertext []byte, mech Mechanism) ([]byte, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.decrypt(p, keyID, ciphertext, mech)
}

func (h *HSM) decrypt(p Permissions, keyID ID, ciphertext []byte, mech Mechanism) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpDecrypt, keyID); err != nil {
		return nil, err
	}
	key, err := h.keyLocked(keyID)
	if err != nil {
		return nil, err
	}
	mech, err = ParseMechanism(string(mech))
	if err != nil {
		return nil, err
	}
	if key.spec.Type != TypeAES || !mech.IsAES() {
		return nil, fmt.Errorf("%w: AES key and mechanism required", ErrUnsupportedMechanism)
	}
	if !key.spec.Attributes.Decrypt {
		return nil, fmt.Errorf("%w: CKA_DECRYPT=false on %q", ErrPolicy, keyID)
	}
	if err := h.policy.operationRoleAllowed(key.spec.Attributes, OpDecrypt); err != nil {
		return nil, err
	}

	record, tracked := h.records[sha256.Sum256(ciphertext)]
	actualMech := mech
	if tracked {
		if recordWrapper, ok := h.keys[record.wrappingKey]; ok &&
			!equalMaterialFingerprint(key, recordWrapper) {
			return nil, fmt.Errorf("%w: decryption key material does not match the recorded wrapper", ErrCrypto)
		}
		if mech != record.mechanism && !h.policy.AllowMechanismConfusion {
			return nil, fmt.Errorf("%w: blob was created with %s, requested %s",
				ErrMechanismMismatch, record.mechanism, mech)
		}
		if h.policy.AllowMechanismConfusion {
			actualMech = record.mechanism
		}
	}
	return decryptWithMechanism(actualMech, key.spec.Material, ciphertext)
}

// DeriveKey calls C_DeriveKey semantics using the default permission set.
func (h *HSM) DeriveKey(baseKey ID, template KeyTemplate, mech Mechanism) (ID, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.deriveKey(p, baseKey, template, mech)
}

func (h *HSM) deriveKey(p Permissions, baseKeyID ID, template KeyTemplate, mech Mechanism) (ID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpDeriveKey, baseKeyID); err != nil {
		return "", err
	}
	base, err := h.keyLocked(baseKeyID)
	if err != nil {
		return "", err
	}
	if !base.spec.Attributes.Derive {
		return "", fmt.Errorf("%w: CKA_DERIVE=false on %q", ErrPolicy, baseKeyID)
	}
	if mech == "" {
		mech = HKDFSHA256
	} else {
		mech, err = ParseMechanism(string(mech))
		if err != nil {
			return "", err
		}
	}
	if mech != HKDFSHA256 {
		return "", fmt.Errorf("%w: DeriveKey supports %s", ErrUnsupportedMechanism, HKDFSHA256)
	}
	attrs := template.Attributes
	if err := validateAttributeInvariants(attrs); err != nil {
		return "", err
	}
	if !h.policy.AllowDeriveMaterialCopy {
		if base.spec.Attributes.Sensitive && !attrs.Sensitive {
			return "", fmt.Errorf("%w: derived key cannot be less sensitive than base", ErrMonotonicity)
		}
		if !base.spec.Attributes.Extractable && attrs.Extractable {
			return "", fmt.Errorf("%w: derived key cannot be more extractable than base", ErrMonotonicity)
		}
	}
	if !h.policy.AllowPermissiveDerivation && (!attrs.Sensitive || attrs.Extractable) {
		return "", fmt.Errorf("%w: derived key must keep CKA_SENSITIVE=true and CKA_EXTRACTABLE=false", ErrPolicy)
	}
	if err := h.policy.validateRoleOverlap(attrs); err != nil {
		return "", err
	}
	if err := h.applyTemplateTrustLocked(p, attrs); err != nil {
		return "", err
	}

	keyType := template.Type
	if keyType == "" {
		keyType = TypeAES
	}
	class := template.Class
	if class == "" {
		class = ClassSecretKey
	}
	length := template.MaterialLength
	if length == 0 {
		length = 32
	}
	var material []byte
	if h.policy.AllowDeriveMaterialCopy {
		material = append([]byte(nil), base.spec.Material...)
	} else {
		material, err = hkdfSHA256(base.spec.Material, []byte("hsm-derive-salt"),
			[]byte(template.Label+"|"+string(mech)), length)
		if err != nil {
			return "", err
		}
	}
	attrs.AlwaysSensitive = base.spec.Attributes.AlwaysSensitive && attrs.Sensitive
	attrs.NeverExtractable = base.spec.Attributes.NeverExtractable && !attrs.Extractable
	if err := validateAttributeInvariants(attrs); err != nil {
		return "", err
	}

	id := template.ID
	if id == "" {
		id, err = h.generatedIDLocked("derived")
		if err != nil {
			return "", err
		}
	}
	origin := id
	if h.policy.AllowDeriveMaterialCopy {
		origin = base.origin
	}
	spec := KeySpec{
		ID:         id,
		Label:      template.Label,
		Class:      class,
		Type:       keyType,
		Material:   material,
		Attributes: attrs,
	}
	if err := h.addKeyLocked(spec, origin); err != nil {
		return "", err
	}
	return id, nil
}

// SetAttributeValue applies a PKCS#11 attribute patch using the default
// permission set.
func (h *HSM) SetAttributeValue(keyID ID, patch AttributePatch) error {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.setAttributeValue(p, keyID, patch)
}

func (h *HSM) setAttributeValue(p Permissions, keyID ID, patch AttributePatch) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpSetAttributeValue, keyID); err != nil {
		return err
	}
	key, err := h.keyLocked(keyID)
	if err != nil {
		return err
	}
	old := key.spec.Attributes
	next := patch.Apply(old)
	if err := validateMonotonicTransition(old, next); err != nil {
		return err
	}
	if err := validateAttributeInvariants(next); err != nil {
		return err
	}
	if patch.AlwaysSensitive != nil && old.AlwaysSensitive != next.AlwaysSensitive {
		return fmt.Errorf("%w: CKA_ALWAYS_SENSITIVE is read-only", ErrMonotonicity)
	}
	if patch.NeverExtractable != nil && old.NeverExtractable != next.NeverExtractable {
		return fmt.Errorf("%w: CKA_NEVER_EXTRACTABLE is read-only", ErrMonotonicity)
	}
	if err := h.policy.validateRoleMutation(old, next); err != nil {
		return err
	}
	if err := h.policy.validateRoleOverlap(next); err != nil {
		return err
	}
	if next.Trusted != old.Trusted {
		if !h.policy.AllowTrustedPromotion {
			return fmt.Errorf("%w: changing CKA_TRUSTED is disabled", ErrTrustedPromotion)
		}
		if next.Trusted && !p.CanSetTrusted {
			return fmt.Errorf("%w: setting CKA_TRUSTED requires SO permission", ErrPermission)
		}
	}
	key.spec.Attributes = next
	return nil
}

// CopyObject copies a key object using the default permission set. The new
// object's class and type must match the source.
func (h *HSM) CopyObject(source ID, template KeyTemplate) (ID, error) {
	h.mu.RLock()
	p := h.permissions.clone()
	h.mu.RUnlock()
	return h.copyObject(p, source, template)
}

func (h *HSM) copyObject(p Permissions, sourceID ID, template KeyTemplate) (ID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.checkOpLocked(p, OpCopyObject, sourceID); err != nil {
		return "", err
	}
	source, err := h.keyLocked(sourceID)
	if err != nil {
		return "", err
	}
	if template.Class != "" && template.Class != source.spec.Class {
		return "", fmt.Errorf("%w: CopyObject cannot change object class", ErrPolicy)
	}
	if template.Type != "" && template.Type != source.spec.Type {
		return "", fmt.Errorf("%w: CopyObject cannot change key type", ErrPolicy)
	}
	old := source.spec.Attributes
	next := template.Attributes
	if err := validateMonotonicTransition(old, next); err != nil {
		return "", err
	}
	if err := validateAttributeInvariants(next); err != nil {
		return "", err
	}
	if err := h.policy.validateRoleMutation(old, next); err != nil {
		return "", err
	}
	if err := h.policy.validateRoleOverlap(next); err != nil {
		return "", err
	}
	if next.Trusted != old.Trusted {
		if !h.policy.AllowTrustedPromotion {
			return "", fmt.Errorf("%w: changing CKA_TRUSTED is disabled", ErrTrustedPromotion)
		}
		if next.Trusted && !p.CanSetTrusted {
			return "", fmt.Errorf("%w: setting CKA_TRUSTED requires SO permission", ErrPermission)
		}
	}
	next.AlwaysSensitive = old.AlwaysSensitive && next.Sensitive
	next.NeverExtractable = old.NeverExtractable && !next.Extractable
	if err := validateAttributeInvariants(next); err != nil {
		return "", err
	}

	id := template.ID
	if id == "" {
		id, err = h.generatedIDLocked("copy")
		if err != nil {
			return "", err
		}
	}
	if _, exists := h.keys[id]; exists {
		return "", fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}
	spec := source.spec
	spec.ID = id
	spec.Label = template.Label
	spec.Material = append([]byte(nil), source.spec.Material...)
	spec.PublicMaterial = append([]byte(nil), source.spec.PublicMaterial...)
	spec.Attributes = next
	if err := h.addKeyLocked(spec, source.origin); err != nil {
		return "", err
	}
	return id, nil
}

// Session is a caller-scoped view of an HSM. It is safe to use concurrently
// with other sessions; each operation still takes the HSM lock.
type Session struct {
	h    *HSM
	perm Permissions
}

// As returns a caller-scoped session using p.
func (h *HSM) As(p Permissions) *Session {
	return &Session{h: h, perm: p.clone()}
}

// WrapKey invokes the caller-scoped C_WrapKey operation.
func (s *Session) WrapKey(wrappingKey, target ID, mech Mechanism) ([]byte, error) {
	return s.h.wrapKey(s.perm, wrappingKey, target, mech)
}

// UnwrapKey invokes the caller-scoped C_UnwrapKey operation.
func (s *Session) UnwrapKey(unwrappingKey ID, blob []byte, template KeyTemplate, mech Mechanism) (ID, error) {
	return s.h.unwrapKey(s.perm, unwrappingKey, blob, template, mech)
}

// Encrypt invokes the caller-scoped C_Encrypt operation.
func (s *Session) Encrypt(keyID ID, plaintext []byte, mech Mechanism) ([]byte, error) {
	return s.h.encrypt(s.perm, keyID, plaintext, mech)
}

// Decrypt invokes the caller-scoped C_Decrypt operation.
func (s *Session) Decrypt(keyID ID, ciphertext []byte, mech Mechanism) ([]byte, error) {
	return s.h.decrypt(s.perm, keyID, ciphertext, mech)
}

// DeriveKey invokes the caller-scoped C_DeriveKey operation.
func (s *Session) DeriveKey(baseKey ID, template KeyTemplate, mech Mechanism) (ID, error) {
	return s.h.deriveKey(s.perm, baseKey, template, mech)
}

// SetAttributeValue invokes the caller-scoped C_SetAttributeValue operation.
func (s *Session) SetAttributeValue(keyID ID, patch AttributePatch) error {
	return s.h.setAttributeValue(s.perm, keyID, patch)
}

// CopyObject invokes the caller-scoped C_CopyObject operation.
func (s *Session) CopyObject(source ID, template KeyTemplate) (ID, error) {
	return s.h.copyObject(s.perm, source, template)
}

func (h *HSM) clone() *HSM {
	h.mu.RLock()
	defer h.mu.RUnlock()
	clone := &HSM{
		policy:        h.policy,
		keys:          make(map[ID]*keyObject, len(h.keys)),
		records:       make(map[[32]byte]blobRecord, len(h.records)),
		permissions:   h.permissions.clone(),
		seq:           h.seq,
		deterministic: true,
	}
	for id, key := range h.keys {
		clone.keys[id] = key.clone()
	}
	for fp, record := range h.records {
		clone.records[fp] = record
	}
	return clone
}

func equalMaterialFingerprint(a, b *keyObject) bool {
	return a.materialFingerprint() == b.materialFingerprint()
}
