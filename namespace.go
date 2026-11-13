package netsec

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ObjectID identifies one concrete object instance. It is independent of the
// object's name and changes when the name is deleted and recreated.
type ObjectID [16]byte

// IsZero reports whether id is the zero object identifier.
func (id ObjectID) IsZero() bool {
	return id == ObjectID{}
}

// String returns the lower-case hexadecimal object identifier.
func (id ObjectID) String() string {
	const hexDigits = "0123456789abcdef"
	var out [32]byte
	for i, b := range id {
		out[2*i] = hexDigits[b>>4]
		out[2*i+1] = hexDigits[b&0x0f]
	}
	return string(out[:])
}

// ObjectHandle is an immutable snapshot of the security metadata returned
// when an object is created or opened.
type ObjectHandle struct {
	ObjectID           ObjectID
	Name               Name
	Type               ObjectType
	Generation         uint64
	CreatorSID         string
	CreatorIntegrity   IntegrityLevel
	SecurityDescriptor SecurityDescriptor
	CreationNonce      [16]byte
}

// DescriptorDigest returns the digest of the immutable security descriptor.
func (h ObjectHandle) DescriptorDigest() Digest {
	return HashDescriptor(h.SecurityDescriptor)
}

// IdentityEqual reports whether both handles are identical snapshots of one
// object instance, including its creation metadata.
func (h ObjectHandle) IdentityEqual(other ObjectHandle) bool {
	return h.ObjectID == other.ObjectID &&
		h.Name == other.Name &&
		h.Type == other.Type &&
		h.Generation == other.Generation &&
		h.CreatorSID == other.CreatorSID &&
		h.CreatorIntegrity == other.CreatorIntegrity &&
		h.SecurityDescriptor == other.SecurityDescriptor &&
		h.CreationNonce == other.CreationNonce
}

// CreateRequest asks the namespace to create an object if, and only if, its
// case-folded name is absent.
type CreateRequest struct {
	Name               Name
	Creator            Creator
	SecurityDescriptor SecurityDescriptor
	Nonce              [16]byte
}

type objectRecord struct {
	id         ObjectID
	name       Name
	creator    Creator
	generation uint64
	sd         SecurityDescriptor
	nonce      [16]byte
}

func (r *objectRecord) handle() ObjectHandle {
	return ObjectHandle{
		ObjectID:           r.id,
		Name:               r.name,
		Type:               r.name.Type,
		Generation:         r.generation,
		CreatorSID:         r.creator.SID,
		CreatorIntegrity:   r.creator.Integrity,
		SecurityDescriptor: r.sd,
		CreationNonce:      r.nonce,
	}
}

// Namespace is an in-memory object manager. Names are unique by scope and
// case-folded UTF-16 path; object type is verified separately so an existing
// name of the wrong type is a mismatch, not a free slot.
type Namespace struct {
	mu          sync.RWMutex
	objects     map[string]*objectRecord
	ids         map[ObjectID]struct{}
	generations map[string]uint64
}

// NewNamespace creates an empty namespace.
func NewNamespace() *Namespace {
	return &Namespace{
		objects:     make(map[string]*objectRecord),
		ids:         make(map[ObjectID]struct{}),
		generations: make(map[string]uint64),
	}
}

func (n *Namespace) initLocked() {
	if n.objects == nil {
		n.objects = make(map[string]*objectRecord)
	}
	if n.ids == nil {
		n.ids = make(map[ObjectID]struct{})
	}
	if n.generations == nil {
		n.generations = make(map[string]uint64)
	}
}

// CreateIfAbsent atomically creates req.Name when no object exists under its
// case-folded name. It never replaces a pre-existing object and never alters
// an existing object's metadata.
func (n *Namespace) CreateIfAbsent(req CreateRequest) (ObjectHandle, error) {
	if n == nil {
		return ObjectHandle{}, ErrInvalidNamespace
	}
	if err := req.Name.Validate(); err != nil {
		return ObjectHandle{}, err
	}
	if err := req.Creator.Validate(); err != nil {
		return ObjectHandle{}, err
	}
	key, err := req.Name.canonicalKey()
	if err != nil {
		return ObjectHandle{}, err
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.initLocked()

	if existing, ok := n.objects[key]; ok {
		return ObjectHandle{}, occupiedError(existing, req.Name)
	}

	var id ObjectID
	for {
		id, err = random128()
		if err != nil {
			return ObjectHandle{}, fmt.Errorf("%w: %v", ErrRandomSource, err)
		}
		if id.IsZero() {
			continue
		}
		if _, exists := n.ids[id]; !exists {
			break
		}
	}

	generation := n.generations[key] + 1
	n.generations[key] = generation

	record := &objectRecord{
		id:         id,
		name:       req.Name,
		creator:    req.Creator,
		generation: generation,
		sd:         req.SecurityDescriptor,
		nonce:      req.Nonce,
	}
	n.objects[key] = record
	n.ids[id] = struct{}{}
	return record.handle(), nil
}

func occupiedError(existing *objectRecord, requested Name) error {
	errs := []error{ErrAlreadyExists}
	if existing.name.Text != requested.Text {
		// The lookup itself is case-folded, so different text means an actual
		// case collision rather than an unrelated occupant.
		errs = append(errs, fmt.Errorf("%w: %q occupies %q", ErrCaseCollision, existing.name.Text, requested.Text))
	}
	if existing.name.Type != requested.Type {
		errs = append(errs, fmt.Errorf("%w: existing object is %s, requested %s", ErrTypeMismatch, existing.name.Type, requested.Type))
	}
	return errors.Join(errs...)
}

// OpenExisting resolves a name and verifies the requested object type. It
// never creates an object when the name is absent or has the wrong type.
func (n *Namespace) OpenExisting(name Name) (ObjectHandle, error) {
	return n.openExistingVerified(name, nil)
}

func (n *Namespace) openExistingVerified(name Name, check func(ObjectHandle) error) (ObjectHandle, error) {
	if n == nil {
		return ObjectHandle{}, ErrInvalidNamespace
	}
	key, err := name.canonicalKey()
	if err != nil {
		return ObjectHandle{}, err
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	record, ok := n.objects[key]
	if !ok {
		return ObjectHandle{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	handle := record.handle()
	if record.name.Type != name.Type {
		return ObjectHandle{}, fmt.Errorf("%w: requested %s, found %s", ErrTypeMismatch, name.Type, record.name.Type)
	}
	if check != nil {
		if err := check(handle); err != nil {
			return ObjectHandle{}, err
		}
	}
	return handle, nil
}

// Lookup returns the current object under a scope and textual path without
// requiring its type. It is useful for diagnostics and adversarial tests.
func (n *Namespace) Lookup(scope Scope, text string) (ObjectHandle, bool) {
	name := Name{Scope: scope, Text: text, Type: ObjectTypeMutex}
	key, err := name.canonicalKey()
	if err != nil {
		return ObjectHandle{}, false
	}
	if n == nil {
		return ObjectHandle{}, false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	record, ok := n.objects[key]
	if !ok {
		return ObjectHandle{}, false
	}
	return record.handle(), true
}

// Delete removes the named object. A later CreateIfAbsent under the same
// folded name receives a strictly greater generation.
func (n *Namespace) Delete(name Name) error {
	if n == nil {
		return ErrInvalidNamespace
	}
	key, err := name.canonicalKey()
	if err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	record, ok := n.objects[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if record.name.Type != name.Type {
		return fmt.Errorf("%w: requested %s, found %s", ErrTypeMismatch, name.Type, record.name.Type)
	}
	delete(n.objects, key)
	delete(n.ids, record.id)
	// generations is deliberately retained so delete/recreate cannot reuse
	// an old generation.
	return nil
}

// DeleteIf deletes the exact object instance in handle. A stale handle
// cannot delete the replacement object that now occupies the same name.
func (n *Namespace) DeleteIf(handle ObjectHandle) error {
	if n == nil {
		return ErrInvalidNamespace
	}
	key, err := handle.Name.canonicalKey()
	if err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	record, ok := n.objects[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, handle.Name)
	}
	current := record.handle()
	if !current.IdentityEqual(handle) {
		return fmt.Errorf("%w: name now contains generation %d", ErrStaleHandle, current.Generation)
	}
	delete(n.objects, key)
	delete(n.ids, record.id)
	return nil
}

// Len returns the number of live objects.
func (n *Namespace) Len() int {
	if n == nil {
		return 0
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.objects)
}

// withCurrent runs check and fn against the current record while holding the
// namespace read lock. A delete/recreate cannot interleave between revalidation
// and the host callback. Callbacks must not mutate the namespace.
func (n *Namespace) withCurrent(name Name, check func(ObjectHandle) error, fn func(ObjectHandle) error) error {
	if n == nil {
		return ErrInvalidNamespace
	}
	key, err := name.canonicalKey()
	if err != nil {
		return err
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	record, ok := n.objects[key]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	handle := record.handle()
	if record.name.Type != name.Type {
		return fmt.Errorf("%w: requested %s, found %s", ErrTypeMismatch, name.Type, record.name.Type)
	}
	if check != nil {
		if err := check(handle); err != nil {
			return err
		}
	}
	if fn != nil {
		return fn(handle)
	}
	return nil
}

func random128() ([16]byte, error) {
	var out [16]byte
	if _, err := io.ReadFull(rand.Reader, out[:]); err != nil {
		return out, err
	}
	return out, nil
}

// NewCreationNonce returns a service-generated 128-bit creation nonce. A
// zero value is rejected and regenerated.
func NewCreationNonce() ([16]byte, error) {
	for {
		nonce, err := random128()
		if err != nil {
			return [16]byte{}, fmt.Errorf("%w: %v", ErrRandomSource, err)
		}
		if nonce != ([16]byte{}) {
			return nonce, nil
		}
	}
}
