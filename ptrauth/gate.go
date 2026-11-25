package ptrauth

// mint recomputes the HMAC for a full set of authenticated attributes.
func (r *Registry) mint(address, generation uint64, kind Kind, ctx ContextID, size uint64) (TaggedPtr, error) {
	normalized, err := kind.normalize()
	if err != nil {
		return TaggedPtr{}, err
	}
	if ctx == "" {
		return TaggedPtr{}, ErrContextMismatch
	}
	tag := r.auth.Tag(address, generation, normalized, ctx, size)
	return TaggedPtr{
		address:    address,
		generation: generation,
		kind:       normalized,
		context:    ctx,
		size:       size,
		tag:        tag,
	}, nil
}

// checkKindForObject rejects executable rights on data objects and storage
// rights on code-only objects.
func checkKindForObject(obj *object, kind Kind) error {
	if kind&KindExecute != 0 && obj.code == nil {
		return ErrNotExecutable
	}
	if kind&KindWrite != 0 && obj.data == nil {
		return ErrNotData
	}
	return nil
}

// Tag mints the first authenticated pointer for an object generation. The
// call context binds the pointer: presenting it in any other context fails
// the gate unless Recontext re-authenticates it.
func (r *Registry) Tag(obj *Object, kind Kind, ctx ContextID) (TaggedPtr, error) {
	if obj == nil || obj.registry != r {
		return TaggedPtr{}, ErrUnknownAddress
	}
	normalized, err := kind.normalize()
	if err != nil {
		return TaggedPtr{}, err
	}
	target := obj.inner
	target.mu.RLock()
	defer target.mu.RUnlock()
	if !target.live.Load() {
		return TaggedPtr{}, ErrGenerationRetired
	}
	if err := checkKindForObject(target, normalized); err != nil {
		return TaggedPtr{}, err
	}
	return r.mint(target.address, target.generation.Load(), normalized, ctx, target.size)
}

// verifyTag checks the HMAC and the structural context/kind fields before
// the object table is consulted. A forged or context-swapped pointer never
// reaches the generation gate.
func (r *Registry) verifyTag(ptr TaggedPtr, ctx ContextID) error {
	if len(ptr.tag) == 0 {
		return ErrTagForged
	}
	normalized, err := ptr.kind.normalize()
	if err != nil {
		return ErrTagForged
	}
	if !r.auth.Verify(ptr.address, ptr.generation, normalized, ptr.context, ptr.size, ptr.tag) {
		return ErrTagForged
	}
	if ctx == "" || ctx != ptr.context {
		return ErrContextMismatch
	}
	return nil
}

// lookup resolves the object containing an authenticated address and runs
// the atomic generation gate. Interior pointers produced by Derive resolve
// to their parent object while remaining bound to their own authenticated
// span, which must not escape the object. The returned object is locked
// (read or write mode) for the caller's entire dereference, so a concurrent
// Free blocks until the access drains.
func (r *Registry) lookup(ptr TaggedPtr, forWrite bool) (*object, func(), error) {
	r.mu.RLock()
	var obj *object
	if exact := r.objects[ptr.address]; exact != nil {
		obj = exact
	} else {
		for _, candidate := range r.objects {
			if ptr.address >= candidate.address &&
				ptr.address-candidate.address <= candidate.size &&
				ptr.size <= candidate.size-(ptr.address-candidate.address) {
				obj = candidate
				break
			}
		}
	}
	r.mu.RUnlock()
	if obj == nil {
		return nil, nil, ErrUnknownAddress
	}

	if forWrite {
		obj.mu.Lock()
	} else {
		obj.mu.RLock()
	}
	unlock := obj.mu.RUnlock
	if forWrite {
		unlock = obj.mu.Unlock
	}

	if !obj.live.Load() {
		unlock()
		return nil, nil, ErrGenerationRetired
	}
	if obj.generation.Load() != ptr.generation {
		unlock()
		return nil, nil, ErrGenerationStale
	}
	return obj, unlock, nil
}

// gate combines HMAC verification, context matching, kind authorization and
// the atomic object-generation check.
func (r *Registry) gate(ptr TaggedPtr, ctx ContextID, required Kind, forWrite bool) (*object, func(), error) {
	if err := r.verifyTag(ptr, ctx); err != nil {
		return nil, nil, err
	}
	if !ptr.kind.permits(required) {
		return nil, nil, ErrKindNotPermitted
	}
	return r.lookup(ptr, forWrite)
}

// Check validates a pointer against the current context without dereferencing
// it. It exercises the same gate as Read/Write/Call.
func (r *Registry) Check(ptr TaggedPtr, ctx ContextID) error {
	if err := r.verifyTag(ptr, ctx); err != nil {
		return err
	}
	_, unlock, err := r.lookup(ptr, false)
	if err != nil {
		return err
	}
	unlock()
	return nil
}

// spanCheck validates offset+length against the authenticated span with
// explicit overflow protection.
func spanCheck(ptr TaggedPtr, offset, length uint64) error {
	if length == 0 {
		return nil
	}
	if offset > ptr.size || length > ptr.size-offset {
		return ErrOutOfBounds
	}
	return nil
}

// Read loads length bytes at offset through an authenticated read-capable
// pointer.
func (r *Registry) Read(ptr TaggedPtr, ctx ContextID, offset, length uint64) ([]byte, error) {
	if err := spanCheck(ptr, offset, length); err != nil {
		return nil, err
	}
	obj, unlock, err := r.gate(ptr, ctx, KindRead, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if obj.data == nil {
		return nil, ErrNotData
	}
	if length == 0 {
		return []byte{}, nil
	}
	out := make([]byte, length)
	copy(out, obj.data[offset:offset+length])
	return out, nil
}

// Write stores data at offset through an authenticated write-capable
// pointer. The object lock is held exclusively so Free cannot retire the
// generation while the store is in flight.
func (r *Registry) Write(ptr TaggedPtr, ctx ContextID, offset uint64, data []byte) error {
	length := uint64(len(data))
	if err := spanCheck(ptr, offset, length); err != nil {
		return err
	}
	obj, unlock, err := r.gate(ptr, ctx, KindWrite, true)
	if err != nil {
		return err
	}
	defer unlock()
	if obj.data == nil {
		return ErrNotData
	}
	if length == 0 {
		return nil
	}
	copy(obj.data[offset:offset+length], data)
	return nil
}

// Call invokes the executable object bound to ptr. A data pointer can never
// reach this path: KindExecute must be present in the authenticated kind and
// the target object must carry a code entry point.
func (r *Registry) Call(ptr TaggedPtr, ctx ContextID, arg uint64) (uint64, error) {
	obj, unlock, err := r.gate(ptr, ctx, KindExecute, false)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if obj.code == nil {
		return 0, ErrNotExecutable
	}
	return obj.code(arg), nil
}
