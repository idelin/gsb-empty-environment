package gittree

import (
	"context"
	"fmt"
)

// RawObject is an object as stored, before any digest verification.
type RawObject struct {
	Type    ObjectType
	Content []byte
}

// ObjectSource supplies raw, unverified Git objects by ID. Implementations
// are expected to be safe for concurrent use.
type ObjectSource interface {
	// Object returns the object stored under id, or an error wrapping
	// ErrObjectNotFound when the object is unavailable.
	Object(ctx context.Context, id ObjectID) (RawObject, error)
}

// MemorySource is an in-memory ObjectSource useful for tests and for
// verifying a bundle of proof objects shipped alongside a signature.
type MemorySource struct {
	objects map[ObjectID]RawObject
}

// NewMemorySource creates an empty MemorySource.
func NewMemorySource() *MemorySource {
	return &MemorySource{objects: make(map[ObjectID]RawObject)}
}

// Object implements ObjectSource.
func (s *MemorySource) Object(_ context.Context, id ObjectID) (RawObject, error) {
	obj, ok := s.objects[id]
	if !ok {
		return RawObject{}, fmt.Errorf("%w: %s", ErrObjectNotFound, id)
	}
	// Return a copy so callers cannot mutate stored content through the slice.
	out := RawObject{Type: obj.Type, Content: append([]byte(nil), obj.Content...)}
	return out, nil
}

// Put stores a raw object under its claimed ID. The content is copied, so
// later mutation by the caller does not affect the source.
func (s *MemorySource) Put(id ObjectID, obj RawObject) {
	s.objects[id] = RawObject{Type: obj.Type, Content: append([]byte(nil), obj.Content...)}
}

// Add stores an object and returns its computed content ID.
func (s *MemorySource) Add(algo Algorithm, typ ObjectType, content []byte) ObjectID {
	id := HashObject(algo, typ, content)
	s.Put(id, RawObject{Type: typ, Content: content})
	return id
}

// AddTree marshals entries, stores the resulting tree object and returns its
// content ID.
func (s *MemorySource) AddTree(algo Algorithm, entries []Entry) (ObjectID, error) {
	content, err := MarshalTree(algo, entries)
	if err != nil {
		return "", err
	}
	id := HashObject(algo, TypeTree, content)
	s.Put(id, RawObject{Type: TypeTree, Content: content})
	return id, nil
}

// Len reports the number of stored objects.
func (s *MemorySource) Len() int { return len(s.objects) }

// Has reports whether id is present in the source.
func (s *MemorySource) Has(id ObjectID) bool {
	_, ok := s.objects[id]
	return ok
}
