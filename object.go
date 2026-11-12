package netsec

import "bytes"

// Handle is an opaque object handle from a tenant's private handle space.
// Handle values are generated with a cryptographically secure random source;
// they are deliberately not derived from object indices or allocation order.
type Handle uint64

// TenantID identifies an authenticated tenant. The proxy must obtain this
// value from the tenant's authenticated connection, never from unauthenticated
// client-controlled input.
type TenantID = string

// ObjectClass identifies the kind of object stored in the simulated token.
// ClassZero (the zero value) is treated as "any class" by Filter.
type ObjectClass uint32

const (
	ClassZero ObjectClass = iota
	ClassData
	ClassCertificate
	ClassPublicKey
	ClassPrivateKey
	ClassSecretKey
)

// Attributes describes an object being created. Token corresponds to
// CKA_TOKEN: when true the object belongs to the tenant's token and is visible
// to all of that tenant's sessions after login. When false, the object is a
// session object and is visible only to the Session that created it.
type Attributes struct {
	Class   ObjectClass
	Label   string
	ID      []byte
	Value   []byte
	Token   bool
	Private bool
}

// Object is a snapshot of an object returned to an authorized caller. All
// byte slices are copied, so modifying the returned value cannot modify token
// state.
type Object struct {
	Handle  Handle
	Class   ObjectClass
	Label   string
	ID      []byte
	Value   []byte
	Token   bool
	Private bool
}

// Filter selects objects returned by FindObjects. Zero fields are wildcards,
// except that nil pointer fields also mean wildcard. A nil ID slice matches
// every ID, while a non-nil empty slice matches only an empty ID.
type Filter struct {
	Class   ObjectClass
	Label   string
	ID      []byte
	Token   *bool
	Private *bool
}

type storedObject struct {
	handle       Handle
	class        ObjectClass
	label        string
	id           []byte
	value        []byte
	token        bool
	private      bool
	ownerTenant  TenantID
	ownerSession uint64
}

func newStoredObject(h Handle, attrs Attributes, tenant TenantID, session uint64) *storedObject {
	return &storedObject{
		handle:       h,
		class:        attrs.Class,
		label:        attrs.Label,
		id:           cloneBytes(attrs.ID),
		value:        cloneBytes(attrs.Value),
		token:        attrs.Token,
		private:      attrs.Private,
		ownerTenant:  tenant,
		ownerSession: session,
	}
}

func (o *storedObject) snapshot() Object {
	return Object{
		Handle:  o.handle,
		Class:   o.class,
		Label:   o.label,
		ID:      cloneBytes(o.id),
		Value:   cloneBytes(o.value),
		Token:   o.token,
		Private: o.private,
	}
}

func (o *storedObject) matches(f Filter) bool {
	if f.Class != ClassZero && f.Class != o.class {
		return false
	}
	if f.Label != "" && f.Label != o.label {
		return false
	}
	if f.ID != nil && !bytes.Equal(f.ID, o.id) {
		return false
	}
	if f.Token != nil && *f.Token != o.token {
		return false
	}
	if f.Private != nil && *f.Private != o.private {
		return false
	}
	return true
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
