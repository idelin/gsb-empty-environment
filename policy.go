package netsec

import (
	"fmt"
	"strings"
)

// Policy fixes the admission requirements for one expected name.
type Policy struct {
	Name               Name
	AllowedCreatorSIDs []string
	MinimumIntegrity   IntegrityLevel
	SecurityDescriptor SecurityDescriptor
	DescriptorDigest   Digest
}

// NewPolicy builds a policy and binds it to the exact security descriptor
// digest expected at the name.
func NewPolicy(name Name, allowedCreatorSIDs []string, minIntegrity IntegrityLevel, sd SecurityDescriptor) (Policy, error) {
	p := Policy{
		Name:               name,
		AllowedCreatorSIDs: append([]string(nil), allowedCreatorSIDs...),
		MinimumIntegrity:   minIntegrity,
		SecurityDescriptor: sd,
		DescriptorDigest:   HashDescriptor(sd),
	}
	if err := p.Name.Validate(); err != nil {
		return Policy{}, err
	}
	if !p.MinimumIntegrity.Valid() {
		return Policy{}, fmt.Errorf("%w: %d", ErrInvalidIntegrity, p.MinimumIntegrity)
	}
	return p, nil
}

// MustPolicy is NewPolicy for fixed configuration that cannot recover.
func MustPolicy(name Name, allowedCreatorSIDs []string, minIntegrity IntegrityLevel, sd SecurityDescriptor) Policy {
	p, err := NewPolicy(name, allowedCreatorSIDs, minIntegrity, sd)
	if err != nil {
		panic(err)
	}
	return p
}

type policyRecord struct {
	name    Name
	key     string
	allowed map[string]struct{}
	minimum IntegrityLevel
	sd      SecurityDescriptor
	digest  Digest
}

func compilePolicy(p Policy) (*policyRecord, error) {
	if err := p.Name.Validate(); err != nil {
		return nil, err
	}
	if !p.MinimumIntegrity.Valid() {
		return nil, fmt.Errorf("%w: %d", ErrInvalidIntegrity, p.MinimumIntegrity)
	}

	allowed := make(map[string]struct{}, len(p.AllowedCreatorSIDs))
	for _, sid := range p.AllowedCreatorSIDs {
		if sid == "" || strings.ContainsRune(sid, 0) {
			return nil, fmt.Errorf("%w: bad allowed creator SID %q", ErrInvalidPolicy, sid)
		}
		allowed[sid] = struct{}{}
	}

	digest := p.DescriptorDigest
	actual := HashDescriptor(p.SecurityDescriptor)
	if digest.IsZero() {
		digest = actual
	} else if p.SecurityDescriptor != "" && digest != actual {
		return nil, fmt.Errorf("%w: descriptor does not match its digest", ErrInvalidPolicy)
	}

	key, err := p.Name.canonicalKey()
	if err != nil {
		return nil, err
	}

	return &policyRecord{
		name:    p.Name,
		key:     key,
		allowed: allowed,
		minimum: p.MinimumIntegrity,
		sd:      p.SecurityDescriptor,
		digest:  digest,
	}, nil
}

func (p *policyRecord) permitsSID(sid string) bool {
	_, ok := p.allowed[sid]
	return ok
}
