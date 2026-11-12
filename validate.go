package netsec

import (
	"errors"
	"fmt"
)

// ObjectKind distinguishes BPF maps from BPF programs in the fd table.
type ObjectKind int

const (
	KindMap ObjectKind = iota
	KindProg
)

// Object describes a BPF object reachable by file descriptor, as known to
// the loading service. The caller (loader) maintains this table from its
// own bookkeeping; fds absent from the table are rejected.
type Object struct {
	Owner string
	Kind  ObjectKind

	// Map-only fields.
	MapType MapType
	// InnerFD is the inner map template fd for map-in-map types
	// (ArrayOfMaps / HashOfMaps); -1 when not applicable.
	InnerFD int
	// ProgFDs lists program fds currently installed in a ProgArray map
	// (tail-call targets).
	ProgFDs []int
	// MapFDs lists map fds currently stored as values of a map-in-map.
	MapFDs []int
}

// Resolver resolves an fd to the object the loader has on record for it.
type Resolver interface {
	Lookup(fd int) (Object, bool)
}

// ResolverFunc adapts a plain function to Resolver.
type ResolverFunc func(fd int) (Object, bool)

// Lookup implements Resolver.
func (f ResolverFunc) Lookup(fd int) (Object, bool) { return f(fd) }

// TableResolver is a convenience Resolver backed by a map.
type TableResolver map[int]Object

// Lookup implements Resolver.
func (t TableResolver) Lookup(fd int) (Object, bool) {
	obj, ok := t[fd]
	return obj, ok
}

var (
	ErrUnknownFD    = errors.New("netsec: fd not present in loader table")
	ErrCrossTenant  = errors.New("netsec: reference to object owned by another tenant")
	ErrKindMismatch = errors.New("netsec: fd refers to unexpected object kind")
	ErrDepthLimit   = errors.New("netsec: reference closure exceeds depth limit")
)

// DefaultMaxDepth bounds recursion through map-in-map templates and
// program arrays so adversarial object graphs cannot exhaust the stack.
const DefaultMaxDepth = 32

// Validator checks that every BPF object reachable from a program being
// loaded belongs to the tenant loading it.
type Validator struct {
	// Tenant is the tenant identity that must own the whole closure.
	Tenant string
	// Res resolves fds to loader-known objects.
	Res Resolver
	// MaxDepth bounds reference-closure recursion; zero uses DefaultMaxDepth.
	MaxDepth int
}

// ValidateProgram decodes code and verifies the reference closure of the
// program: LD_IMM64 encoding rules, map ownership for BPF_PSEUDO_MAP_FD and
// BPF_PSEUDO_MAP_VALUE, and recursive ownership of map-in-map templates and
// tail-call programs in program arrays.
func (v *Validator) ValidateProgram(code []byte) error {
	insns, err := Decode(code)
	if err != nil {
		return err
	}
	visited := make(map[int]ObjectKind)
	for i, ins := range insns {
		if !ins.IsPseudoMapRef() {
			continue
		}
		if err := v.checkMapFD(int(ins.Imm), i, visited, 0); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) maxDepth() int {
	if v.MaxDepth > 0 {
		return v.MaxDepth
	}
	return DefaultMaxDepth
}

func (v *Validator) lookup(fd int) (Object, error) {
	obj, ok := v.Res.Lookup(fd)
	if !ok {
		return Object{}, fmt.Errorf("%w: fd %d", ErrUnknownFD, fd)
	}
	return obj, nil
}

func (v *Validator) checkOwner(obj Object, fd int) error {
	if obj.Owner != v.Tenant {
		return fmt.Errorf("%w: fd %d owned by %q, tenant %q", ErrCrossTenant, fd, obj.Owner, v.Tenant)
	}
	return nil
}

func (v *Validator) checkMapFD(fd, slot int, visited map[int]ObjectKind, depth int) error {
	if depth > v.maxDepth() {
		return fmt.Errorf("%w: %d", ErrDepthLimit, depth)
	}
	if kind, seen := visited[fd]; seen {
		if kind != KindMap {
			return fmt.Errorf("%w: fd %d was a program, need map", ErrKindMismatch, fd)
		}
		return nil
	}
	obj, err := v.lookup(fd)
	if err != nil {
		return fmt.Errorf("slot %d: %w", slot, err)
	}
	if obj.Kind != KindMap {
		return fmt.Errorf("slot %d: %w: fd %d is a program, need map", slot, ErrKindMismatch, fd)
	}
	if err := v.checkOwner(obj, fd); err != nil {
		return fmt.Errorf("slot %d: %w", slot, err)
	}
	visited[fd] = KindMap

	if obj.MapType.IsMapInMap() && obj.InnerFD >= 0 {
		if err := v.checkMapFD(obj.InnerFD, slot, visited, depth+1); err != nil {
			return fmt.Errorf("map fd %d inner template: %w", fd, err)
		}
	}
	for _, inner := range obj.MapFDs {
		if err := v.checkMapFD(inner, slot, visited, depth+1); err != nil {
			return fmt.Errorf("map fd %d entry: %w", fd, err)
		}
	}
	if obj.MapType == MapTypeProgArray {
		for _, progFD := range obj.ProgFDs {
			if err := v.checkProgFD(progFD, visited, depth+1); err != nil {
				return fmt.Errorf("map fd %d tail-call target: %w", fd, err)
			}
		}
	}
	return nil
}

func (v *Validator) checkProgFD(fd int, visited map[int]ObjectKind, depth int) error {
	if depth > v.maxDepth() {
		return fmt.Errorf("%w: %d", ErrDepthLimit, depth)
	}
	if kind, seen := visited[fd]; seen {
		if kind != KindProg {
			return fmt.Errorf("%w: fd %d was a map, need program", ErrKindMismatch, fd)
		}
		return nil
	}
	obj, err := v.lookup(fd)
	if err != nil {
		return err
	}
	if obj.Kind != KindProg {
		return fmt.Errorf("%w: fd %d is a map, need program", ErrKindMismatch, fd)
	}
	if err := v.checkOwner(obj, fd); err != nil {
		return err
	}
	visited[fd] = KindProg
	return nil
}
