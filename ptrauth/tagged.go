package ptrauth

// TaggedPtr is an opaque, authenticated pointer capability. Its fields are
// unexported so code outside the package cannot forge or mutate an address,
// generation, kind, context or span without recomputing the HMAC tag.
//
// A zero TaggedPtr is never valid: Verify rejects it with ErrTagForged.
type TaggedPtr struct {
	address    uint64
	generation uint64
	kind       Kind
	context    ContextID
	size       uint64
	tag        []byte
}

// Address returns the authenticated target address.
func (p TaggedPtr) Address() uint64 { return p.address }

// Generation returns the object generation captured when the tag was minted.
func (p TaggedPtr) Generation() uint64 { return p.generation }

// Kind returns the authenticated access kind.
func (p TaggedPtr) Kind() Kind { return p.kind }

// Context returns the call context the pointer was authenticated for.
func (p TaggedPtr) Context() ContextID { return p.context }

// Size returns the authorized span in bytes.
func (p TaggedPtr) Size() uint64 { return p.size }

// cloneTag returns an independent copy of the MAC so callers cannot mutate
// registry-owned backing arrays through a returned slice.
func cloneTag(tag []byte) []byte {
	out := make([]byte, len(tag))
	copy(out, tag)
	return out
}
