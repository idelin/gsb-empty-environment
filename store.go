package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// BindApproval normalizes a reviewed template and freezes its security
// snapshot into an approval. The mutable allow-list is validated up front so
// an approval can never grant write access to a frozen field.
func BindApproval(approvalID string, t WorkloadTemplate, mutableFields []string) (*Approval, error) {
	normalized, err := NormalizeTemplate(t)
	if err != nil {
		return nil, err
	}
	registry, err := newFieldRegistry(mutableFields)
	if err != nil {
		return nil, err
	}
	_ = registry

	return &Approval{
		ApprovalID:      approvalID,
		TemplateID:      t.Metadata.Name,
		TemplateVersion: 1,
		Fingerprint:     normalized.Fingerprint,
		ImageDigest:     normalized.Image.Digest,
		MutableFields:   append([]string(nil), mutableFields...),
		Bound:           normalized,
	}, nil
}

// TemplateStore is the concurrency-safe in-memory registry used by the
// autoscaler control host. Approvals bind a specific template version; any
// later update invalidates them.
type TemplateStore struct {
	mu        sync.RWMutex
	templates map[string]*storedTemplate
}

type storedTemplate struct {
	current WorkloadTemplate
	version int64
	// approvals keyed by approval id, each pinned to a version.
	approvals map[string]*Approval
}

// NewTemplateStore creates an empty store.
func NewTemplateStore() *TemplateStore {
	return &TemplateStore{templates: map[string]*storedTemplate{}}
}

// Create registers a new template at version 1.
func (s *TemplateStore) Create(t WorkloadTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[t.Metadata.Name]; ok {
		return errors.Join(errConflict, fmt.Errorf("template %q already exists", t.Metadata.Name))
	}
	s.templates[t.Metadata.Name] = &storedTemplate{
		current:   deepClone(t),
		version:   1,
		approvals: map[string]*Approval{},
	}
	return nil
}

// Get returns a clone of the current template and its version.
func (s *TemplateStore) Get(id string) (WorkloadTemplate, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.templates[id]
	if !ok {
		return WorkloadTemplate{}, 0, errors.Join(errNotFound, fmt.Errorf("template %q", id))
	}
	return deepClone(st.current), st.version, nil
}

// Update applies a template change only when expectedVersion matches the
// current version (optimistic concurrency). Every approval pinned to an older
// version becomes stale and is rejected at derive time.
func (s *TemplateStore) Update(id string, expectedVersion int64, t WorkloadTemplate) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.templates[id]
	if !ok {
		return 0, errors.Join(errNotFound, fmt.Errorf("template %q", id))
	}
	if st.version != expectedVersion {
		return st.version, errors.Join(errConflict, fmt.Errorf(
			"expected version %d, current version %d", expectedVersion, st.version))
	}
	st.current = deepClone(t)
	st.version++
	return st.version, nil
}

// ApproveTemplate approves a specific template by id.
func (s *TemplateStore) ApproveTemplate(templateID, approvalID string, mutableFields []string) (*Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.templates[templateID]
	if !ok {
		return nil, errors.Join(errNotFound, fmt.Errorf("template %q", templateID))
	}
	approval, err := BindApproval(approvalID, st.current, mutableFields)
	if err != nil {
		return nil, err
	}
	approval.TemplateVersion = st.version
	st.approvals[approvalID] = approval
	return approval, nil
}

// Derive materializes an instance through a structured patch. The approval is
// rejected if the template changed after approval, if any change is
// unauthorized, or if the candidate no longer matches the approved digest.
func (s *TemplateStore) Derive(templateID, approvalID, instanceID string, ops []PatchOperation) (*Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	st, ok := s.templates[templateID]
	if !ok {
		return nil, errors.Join(errNotFound, fmt.Errorf("template %q", templateID))
	}
	approval, ok := st.approvals[approvalID]
	if !ok {
		return nil, errors.Join(errApproval, fmt.Errorf("approval %q not found", approvalID))
	}
	if approval.TemplateVersion != st.version {
		return nil, errors.Join(errStaleApproval, fmt.Errorf(
			"approval pinned to v%d, template is v%d", approval.TemplateVersion, st.version))
	}

	registry, err := newFieldRegistry(approval.MutableFields)
	if err != nil {
		return nil, err
	}
	normalized, decisions, err := Authorize(approval.Bound, registry, ops)
	result := &Instance{
		InstanceID:   instanceID,
		ApprovalID:   approvalID,
		TemplateID:   templateID,
		TemplateName: approval.Bound.Template.Metadata.Name,
		Decisions:    decisions,
	}
	if err != nil {
		// On denial the instance keeps the approved spec; the rejected
		// candidate exists only inside the audit decisions.
		result.Spec = approval.Bound.Template.Spec
		return result, err
	}
	result.Spec = normalized.Template.Spec
	return result, nil
}

// Version returns the current version of a template.
func (s *TemplateStore) Version(id string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.templates[id]
	if !ok {
		return 0, errors.Join(errNotFound, fmt.Errorf("template %q", id))
	}
	return st.version, nil
}
