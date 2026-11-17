package netsec

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const abortTimeout = 5 * time.Second

// CreateRequest describes a resource creation. Labels supplied here are
// untrusted request labels; inherited and mandatory labels are applied by the
// host before anything is staged.
type CreateRequest struct {
	ID       string
	Kind     string
	ParentID string
	Data     []byte
	Labels   Labels
}

// InheritFunc resolves trusted inherited labels for a creation request. A
// custom function replaces the default parent-object inheritance.
type InheritFunc func(ctx context.Context, req CreateRequest) (Labels, error)

// Access is the exact authorization input presented to an Authorizer. The
// digest is computed from the final merged label set and must match Labels.
type Access struct {
	Subject     string
	ObjectID    string
	Labels      Labels
	LabelDigest LabelDigest
}

// Verify checks that the access decision is bound to the supplied final label
// set. It must be called before making a permission decision.
func (a Access) Verify() error {
	if err := a.Labels.Validate(); err != nil {
		return err
	}
	if a.LabelDigest != a.Labels.Digest() {
		return fmt.Errorf("%w: access labels and digest disagree", ErrDigestMismatch)
	}
	return nil
}

// Authorizer makes a permission decision from the final label set and its
// digest. Implementations should reject access when Verify fails.
type Authorizer interface {
	Authorize(ctx context.Context, access Access) (bool, error)
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(ctx context.Context, access Access) (bool, error)

// Authorize implements Authorizer.
func (f AuthorizerFunc) Authorize(ctx context.Context, access Access) (bool, error) {
	return f(ctx, access)
}

// LabelAuthorizer permits access when access.Labels[Key] equals Value.
type LabelAuthorizer struct {
	Key   string
	Value string
}

// Authorize implements Authorizer.
func (a LabelAuthorizer) Authorize(ctx context.Context, access Access) (bool, error) {
	if err := access.Verify(); err != nil {
		return false, err
	}
	return access.Labels[a.Key] == a.Value, nil
}

// Object is an immutable snapshot of a committed resource. Its labels and data
// cannot be mutated through this type.
type Object struct {
	id       string
	kind     string
	parentID string
	data     []byte
	labels   Labels
	digest   LabelDigest
}

// ID returns the object identifier.
func (o *Object) ID() string { return o.id }

// Kind returns the object kind.
func (o *Object) Kind() string { return o.kind }

// ParentID returns the parent identifier, if any.
func (o *Object) ParentID() string { return o.parentID }

// Data returns a copy of the object data.
func (o *Object) Data() []byte { return cloneBytes(o.data) }

// Labels returns a copy of the final merged label set.
func (o *Object) Labels() Labels { return o.labels.Clone() }

// Digest returns the digest of the final merged label set.
func (o *Object) Digest() LabelDigest { return o.digest }

// Host coordinates creation transactions and authorized reads for one resource
// namespace.
type Host struct {
	store      Store
	mandatory  Labels
	inherited  Labels
	inherit    InheritFunc
	authorizer Authorizer
}

// HostOption configures a Host.
type HostOption func(*Host) error

// WithMandatoryLabels installs labels that every created object must carry.
// Requests that contradict them are rejected.
func WithMandatoryLabels(labels Labels) HostOption {
	return func(h *Host) error {
		if err := labels.Validate(); err != nil {
			return fmt.Errorf("netsec: mandatory labels: %w", err)
		}
		h.mandatory = labels.Clone()
		return nil
	}
}

// WithInheritedLabels installs host-level default labels. Parent labels, when
// a ParentID is supplied, override these defaults; request labels then override
// both, except where mandatory labels apply.
func WithInheritedLabels(labels Labels) HostOption {
	return func(h *Host) error {
		if err := labels.Validate(); err != nil {
			return fmt.Errorf("netsec: inherited labels: %w", err)
		}
		h.inherited = labels.Clone()
		return nil
	}
}

// WithInheritor replaces the default parent-object inheritance function.
func WithInheritor(inherit InheritFunc) HostOption {
	return func(h *Host) error {
		if inherit == nil {
			return fmt.Errorf("netsec: nil inheritor")
		}
		h.inherit = inherit
		return nil
	}
}

// WithAuthorizer installs the authorizer used by Host.Read. Without one,
// protected reads fail closed.
func WithAuthorizer(authorizer Authorizer) HostOption {
	return func(h *Host) error {
		if authorizer == nil {
			return fmt.Errorf("netsec: nil authorizer")
		}
		h.authorizer = authorizer
		return nil
	}
}

// NewHost creates a resource host backed by store.
func NewHost(store Store, opts ...HostOption) (*Host, error) {
	if store == nil {
		return nil, ErrNilStore
	}
	h := &Host{store: store}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(h); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Create validates the final merged labels and then runs the staging,
// label-persistence, and publication steps. The object is never visible to
// readers before publication, and publication cannot happen without persisted
// labels.
func (h *Host) Create(ctx context.Context, req CreateRequest) (obj *Object, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	meta := ObjectMeta{
		ID:       req.ID,
		Kind:     req.Kind,
		ParentID: req.ParentID,
		Data:     cloneBytes(req.Data),
	}
	if err := validateObjectMeta(meta); err != nil {
		return nil, err
	}
	requestedLabels := req.Labels.Clone()

	inherited, err := h.resolveInherited(ctx, req)
	if err != nil {
		return nil, err
	}
	final, err := MergeLabels(inherited, requestedLabels, h.mandatory)
	if err != nil {
		return nil, err
	}
	digest := final.Digest()

	token, err := h.store.CreateObject(ctx, meta)
	if err != nil {
		return nil, err
	}
	published := false
	defer func() {
		if published {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
		defer cancel()
		if abortErr := h.store.Abort(cleanupCtx, token); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("netsec: abort creation: %w", abortErr))
		}
	}()

	if err = h.store.PersistLabels(ctx, token, final, digest); err != nil {
		return nil, err
	}
	if err = h.store.Publish(ctx, token); err != nil {
		return nil, err
	}
	published = true
	return &Object{
		id:       meta.ID,
		kind:     meta.Kind,
		parentID: meta.ParentID,
		data:     cloneBytes(meta.Data),
		labels:   final.Clone(),
		digest:   digest,
	}, nil
}

func (h *Host) resolveInherited(ctx context.Context, req CreateRequest) (Labels, error) {
	if h.inherit != nil {
		labels, err := h.inherit(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("netsec: resolve inherited labels: %w", err)
		}
		return labels.Clone(), nil
	}

	inherited := h.inherited.Clone()
	if req.ParentID == "" {
		return inherited, nil
	}
	parent, err := h.store.Get(ctx, req.ParentID)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve inherited labels from parent %q: %w", req.ParentID, err)
	}
	if err := verifyRecord(parent); err != nil {
		return nil, fmt.Errorf("netsec: verify inherited labels from parent %q: %w", req.ParentID, err)
	}
	return mergeInherited(inherited, parent.Labels), nil
}

// Get returns a committed object after verifying that its stored labels still
// match their digest. Staged objects are not visible.
//
// Get performs no permission check. It is intended for trusted internal callers;
// external readers must use Read so that the authorizer is bound to the final
// label digest.
func (h *Host) Get(ctx context.Context, id string) (*Object, error) {
	record, err := h.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := verifyRecord(record); err != nil {
		return nil, err
	}
	return &Object{
		id:       record.ID,
		kind:     record.Kind,
		parentID: record.ParentID,
		data:     cloneBytes(record.Data),
		labels:   record.Labels.Clone(),
		digest:   record.LabelDigest,
	}, nil
}

func verifyRecord(record ObjectRecord) error {
	if err := validateObjectMeta(record.ObjectMeta); err != nil {
		return err
	}
	if err := record.Labels.Validate(); err != nil {
		return err
	}
	if want := record.Labels.Digest(); record.LabelDigest != want {
		return fmt.Errorf("%w: object %q", ErrDigestMismatch, record.ID)
	}
	return nil
}

// Read performs an authorized read. The permission decision receives the final
// label set and its digest from the same immutable object snapshot.
func (h *Host) Read(ctx context.Context, subject, id string) (*Object, error) {
	if subject == "" {
		return nil, fmt.Errorf("%w: empty subject", ErrInvalidRequest)
	}
	if h.authorizer == nil {
		return nil, ErrNoAuthorizer
	}
	obj, err := h.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	access := Access{
		Subject:     subject,
		ObjectID:    obj.id,
		Labels:      obj.labels.Clone(),
		LabelDigest: obj.digest,
	}
	if err := access.Verify(); err != nil {
		return nil, err
	}
	allowed, err := h.authorizer.Authorize(ctx, access)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrAccessDenied
	}
	return obj, nil
}
