package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// LocalSystemSID is the default service identity used by NewService.
const LocalSystemSID = "S-1-5-18"

// BoundObject is a service-owned binding to one concrete object instance.
// Every Use revalidates the current namespace record and the saved identity
// before releasing a handle to a host callback.
type BoundObject struct {
	service    *Service
	policy     *policyRecord
	key        string
	name       Name
	objectID   ObjectID
	generation uint64
	nonce      [16]byte
	digest     Digest
}

// Name returns the policy's canonical name.
func (b *BoundObject) Name() Name { return b.name }

// ObjectID returns the saved object identifier.
func (b *BoundObject) ObjectID() ObjectID { return b.objectID }

// Generation returns the saved object generation.
func (b *BoundObject) Generation() uint64 { return b.generation }

// DescriptorDigest returns the saved security descriptor digest.
func (b *BoundObject) DescriptorDigest() Digest { return b.digest }

// CreationNonce returns the service-generated nonce saved for this binding.
func (b *BoundObject) CreationNonce() [16]byte { return b.nonce }

// Type returns the bound object type.
func (b *BoundObject) Type() ObjectType { return b.name.Type }

// VerifiedHandle is the only handle shape delivered to a host callback.
// It is constructed after policy and generation revalidation succeeded.
type VerifiedHandle struct {
	ObjectID           ObjectID
	Name               Name
	Type               ObjectType
	Generation         uint64
	CreatorSID         string
	CreatorIntegrity   IntegrityLevel
	SecurityDescriptor SecurityDescriptor
}

// DescriptorDigest returns the digest of the verified descriptor.
func (h VerifiedHandle) DescriptorDigest() Digest {
	return HashDescriptor(h.SecurityDescriptor)
}

// Use revalidates the object, then invokes callback with a verified handle.
// The callback is not invoked when any revalidation step fails.
func (b *BoundObject) Use(callback func(VerifiedHandle) error) error {
	if b == nil || b.service == nil {
		return ErrInvalidHandle
	}
	if callback == nil {
		return ErrNilCallback
	}
	return b.service.useBound(b, callback)
}

// Lock is the mutex-specific host entry point. It behaves exactly like Use,
// so a stale or attacker-controlled binding never reaches the callback.
func (b *BoundObject) Lock(callback func(VerifiedHandle) error) error {
	if b == nil {
		return ErrInvalidHandle
	}
	if b.name.Type != ObjectTypeMutex {
		return fmt.Errorf("%w: bound object is %s, not mutex", ErrTypeMismatch, b.name.Type)
	}
	if callback == nil {
		return ErrNilCallback
	}
	return b.Use(callback)
}

// Map is the section-specific host entry point. It behaves exactly like Use.
func (b *BoundObject) Map(callback func(VerifiedHandle) error) error {
	if b == nil {
		return ErrInvalidHandle
	}
	if b.name.Type != ObjectTypeSection {
		return fmt.Errorf("%w: bound object is %s, not section", ErrTypeMismatch, b.name.Type)
	}
	if callback == nil {
		return ErrNilCallback
	}
	return b.Use(callback)
}

// Service is the trusted hosting side of the namespace. It owns the policy
// table and the saved identity of objects it has opened or created.
type Service struct {
	ns       *Namespace
	identity Creator

	mu       sync.RWMutex
	policies map[string]*policyRecord
	bound    map[string]*BoundObject
}

// NewService creates a service over ns. If identity is omitted, the service
// runs as LocalSystem at IntegritySystem.
func NewService(ns *Namespace, identity ...Creator) *Service {
	id := Creator{SID: LocalSystemSID, Integrity: IntegritySystem}
	if len(identity) > 0 {
		id = identity[0]
	}
	return &Service{
		ns:       ns,
		identity: id,
		policies: make(map[string]*policyRecord),
		bound:    make(map[string]*BoundObject),
	}
}

// Identity returns the service's creation identity.
func (s *Service) Identity() Creator { return s.identity }

// RegisterPolicy registers the fixed expectation for a folded name.
func (s *Service) RegisterPolicy(p Policy) error {
	if s == nil || s.ns == nil {
		return ErrInvalidNamespace
	}
	compiled, err := compilePolicy(p)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.policies[compiled.key]; ok {
		if existing.name.Text != compiled.name.Text {
			return errors.Join(ErrPolicyExists,
				fmt.Errorf("%w: %q already registered as %q", ErrCaseCollision, compiled.name.Text, existing.name.Text))
		}
		return fmt.Errorf("%w: %s", ErrPolicyExists, compiled.name)
	}
	s.policies[compiled.key] = compiled
	return nil
}

// Register is an alias for RegisterPolicy.
func (s *Service) Register(p Policy) error {
	return s.RegisterPolicy(p)
}

func (s *Service) policyFor(name Name) (*policyRecord, error) {
	if s == nil || s.ns == nil {
		return nil, ErrInvalidNamespace
	}
	key, err := name.canonicalKey()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	p, ok := s.policies[key]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPolicyNotFound, name)
	}
	if p.name.Type != name.Type {
		return nil, fmt.Errorf("%w: policy fixes %s, requested %s", ErrTypeMismatch, p.name.Type, name.Type)
	}
	return p, nil
}

// OpenExisting opens and fully verifies an existing object against policy.
// It never creates an object, and a type mismatch is returned as an error.
func (s *Service) OpenExisting(name Name) (*BoundObject, error) {
	p, err := s.policyFor(name)
	if err != nil {
		return nil, err
	}

	handle, err := s.ns.openExistingVerified(p.name, func(handle ObjectHandle) error {
		return s.checkPolicy(p, handle)
	})
	if err != nil {
		return nil, err
	}

	bound := newBoundObject(s, p, handle)
	s.storeBound(bound)
	return bound, nil
}

// CreateNew generates a fresh nonce, atomically creates the policy name if
// absent, then binds and verifies the resulting object. An existing name is
// never overwritten, regardless of its type or owner.
func (s *Service) CreateNew(name Name) (*BoundObject, error) {
	p, err := s.policyFor(name)
	if err != nil {
		return nil, err
	}
	if err := s.checkCreator(p, s.identity); err != nil {
		return nil, err
	}
	if HashDescriptor(p.sd) != p.digest {
		return nil, fmt.Errorf("%w: policy has no usable descriptor for creation", ErrDescriptorMismatch)
	}

	nonce, err := NewCreationNonce()
	if err != nil {
		return nil, err
	}

	handle, err := s.ns.CreateIfAbsent(CreateRequest{
		Name:               p.name,
		Creator:            s.identity,
		SecurityDescriptor: p.sd,
		Nonce:              nonce,
	})
	if err != nil {
		return nil, err
	}

	bound := newBoundObject(s, p, handle)
	if err := s.ns.withCurrent(p.name, func(current ObjectHandle) error {
		if err := s.checkPolicy(p, current); err != nil {
			return err
		}
		if !current.IdentityEqual(handle) || current.DescriptorDigest() != bound.digest {
			return ErrStaleHandle
		}
		return nil
	}, nil); err != nil {
		// Best effort: remove only the exact object this service created.
		_ = s.ns.DeleteIf(handle)
		return nil, err
	}

	s.storeBound(bound)
	return bound, nil
}

func (s *Service) storeBound(b *BoundObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bound[b.key] = b
}

func (s *Service) boundFor(name Name) (*BoundObject, error) {
	p, err := s.policyFor(name)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	b := s.bound[p.key]
	s.mu.RUnlock()
	if b == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotBound, p.name)
	}
	return b, nil
}

// Use revalidates the service's current binding for name before invoking the
// callback. A service with no binding rejects the call; it does not implicitly
// open or create a replacement.
func (s *Service) Use(name Name, callback func(VerifiedHandle) error) error {
	if callback == nil {
		return ErrNilCallback
	}
	bound, err := s.boundFor(name)
	if err != nil {
		return err
	}
	return bound.Use(callback)
}

// Lock is the service-level mutex callback entry point.
func (s *Service) Lock(name Name, callback func(VerifiedHandle) error) error {
	if name.Type != ObjectTypeMutex {
		return fmt.Errorf("%w: requested %s, not mutex", ErrTypeMismatch, name.Type)
	}
	return s.Use(name, callback)
}

// Map is the service-level section callback entry point.
func (s *Service) Map(name Name, callback func(VerifiedHandle) error) error {
	if name.Type != ObjectTypeSection {
		return fmt.Errorf("%w: requested %s, not section", ErrTypeMismatch, name.Type)
	}
	return s.Use(name, callback)
}

func newBoundObject(s *Service, p *policyRecord, h ObjectHandle) *BoundObject {
	return &BoundObject{
		service:    s,
		policy:     p,
		key:        p.key,
		name:       p.name,
		objectID:   h.ObjectID,
		generation: h.Generation,
		nonce:      h.CreationNonce,
		digest:     h.DescriptorDigest(),
	}
}

func (s *Service) useBound(b *BoundObject, callback func(VerifiedHandle) error) error {
	if callback == nil {
		return ErrNilCallback
	}
	return s.ns.withCurrent(b.name, func(current ObjectHandle) error {
		if err := s.checkPolicy(b.policy, current); err != nil {
			return err
		}
		if current.ObjectID != b.objectID ||
			current.Generation != b.generation ||
			current.CreationNonce != b.nonce ||
			current.DescriptorDigest() != b.digest {
			return fmt.Errorf("%w: expected (%s, generation %d), found (%s, generation %d)",
				ErrStaleHandle, b.objectID, b.generation, current.ObjectID, current.Generation)
		}
		return nil
	}, func(current ObjectHandle) error {
		return callback(VerifiedHandle{
			ObjectID:           current.ObjectID,
			Name:               current.Name,
			Type:               current.Type,
			Generation:         current.Generation,
			CreatorSID:         current.CreatorSID,
			CreatorIntegrity:   current.CreatorIntegrity,
			SecurityDescriptor: current.SecurityDescriptor,
		})
	})
}

func (s *Service) checkCreator(p *policyRecord, creator Creator) error {
	if err := creator.Validate(); err != nil {
		return err
	}
	if p.name.Scope.Global && creator.Integrity.IsLowPrivilege() {
		return fmt.Errorf("%w: integrity %s", ErrLowPrivilegeGlobal, creator.Integrity)
	}
	if !p.permitsSID(creator.SID) {
		return fmt.Errorf("%w: %s", ErrCreatorDenied, creator.SID)
	}
	if creator.Integrity < p.minimum {
		return fmt.Errorf("%w: %s < %s", ErrIntegrityTooLow, creator.Integrity, p.minimum)
	}
	return nil
}

func (s *Service) checkPolicy(p *policyRecord, handle ObjectHandle) error {
	if handle.Type != p.name.Type || handle.Name.Type != p.name.Type {
		return fmt.Errorf("%w: policy fixes %s, found %s", ErrTypeMismatch, p.name.Type, handle.Type)
	}
	if handle.Name.Scope != p.name.Scope {
		return fmt.Errorf("%w: expected %s, found %s", ErrScopeMismatch, p.name.Scope, handle.Name.Scope)
	}
	if !sameFoldedName(handle.Name.Text, p.name.Text) {
		return fmt.Errorf("%w: expected %q, found %q", ErrNameMismatch, p.name.Text, handle.Name.Text)
	}

	// This check deliberately precedes the SID allowlist and minimum-integrity
	// check. A permissive DACL or explicit SID allowlist cannot make a
	// low-privilege creator acceptable for a cross-session Global object.
	if p.name.Scope.Global && handle.CreatorIntegrity.IsLowPrivilege() {
		return fmt.Errorf("%w: integrity %s", ErrLowPrivilegeGlobal, handle.CreatorIntegrity)
	}
	if !p.permitsSID(handle.CreatorSID) {
		return fmt.Errorf("%w: %s", ErrCreatorDenied, handle.CreatorSID)
	}
	if handle.CreatorIntegrity < p.minimum {
		return fmt.Errorf("%w: %s < %s", ErrIntegrityTooLow, handle.CreatorIntegrity, p.minimum)
	}
	if handle.DescriptorDigest() != p.digest {
		return fmt.Errorf("%w: expected %s, found %s", ErrDescriptorMismatch, p.digest, handle.DescriptorDigest())
	}
	return nil
}
