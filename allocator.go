package netsec

import (
	"fmt"
	"reflect"
)

// Allocator is intentionally broad so test hosts can provide whatever
// allocation shape they already use.  Reconstruct recognizes:
//
//	Allocate() (*Object, error) + Release(*Object)
//	Allocate() (ObjectID, error) + Release(ObjectID)
//	Alloc()/Free() variants of both
//	func() (*Object, error) and func() (ObjectID, error)
//
// The canonical interface is the first form.  Heap implements it.  When an
// allocator returns only identifiers, Reconstruct owns the new Object records
// and exposes them through Frame.Objects.
type Allocator = any

type objectAllocator interface {
	Allocate() (*Object, error)
	Release(*Object)
}

type objectAllocFree interface {
	Alloc() (*Object, error)
	Free(*Object)
}

type idAllocator interface {
	Allocate() (ObjectID, error)
	Release(ObjectID)
}

type idAllocFree interface {
	Alloc() (ObjectID, error)
	Free(ObjectID)
}

type allocAdapter struct {
	allocate func() (*Object, error)
	release  func(*Object)
}

func newAllocAdapter(a any) (*allocAdapter, error) {
	if a == nil {
		return nil, ErrNoAllocator
	}
	rv := reflect.ValueOf(a)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		if rv.IsNil() {
			return nil, ErrNoAllocator
		}
	}

	switch x := a.(type) {
	case objectAllocator:
		return &allocAdapter{allocate: safeAllocate(x.Allocate), release: safeRelease(x.Release)}, nil
	case objectAllocFree:
		return &allocAdapter{allocate: safeAllocate(x.Alloc), release: safeRelease(x.Free)}, nil
	case idAllocator:
		return &allocAdapter{
			allocate: safeAllocate(func() (*Object, error) {
				id, err := x.Allocate()
				if err != nil {
					return nil, err
				}
				return &Object{ID: id, Fields: make(map[string]Value)}, nil
			}),
			release: safeRelease(func(o *Object) { x.Release(o.ID) }),
		}, nil
	case idAllocFree:
		return &allocAdapter{
			allocate: safeAllocate(func() (*Object, error) {
				id, err := x.Alloc()
				if err != nil {
					return nil, err
				}
				return &Object{ID: id, Fields: make(map[string]Value)}, nil
			}),
			release: safeRelease(func(o *Object) { x.Free(o.ID) }),
		}, nil
	case func() (*Object, error):
		return &allocAdapter{allocate: safeAllocate(x)}, nil
	case func() (ObjectID, error):
		return &allocAdapter{
			allocate: safeAllocate(func() (*Object, error) {
				id, err := x()
				if err != nil {
					return nil, err
				}
				return &Object{ID: id, Fields: make(map[string]Value)}, nil
			}),
		}, nil
	}

	return reflectAllocator(a)
}

func safeRelease(fn func(*Object)) func(*Object) {
	return func(o *Object) {
		defer func() { _ = recover() }()
		fn(o)
	}
}

func safeAllocate(fn func() (*Object, error)) func() (*Object, error) {
	return func() (obj *Object, err error) {
		defer func() {
			if r := recover(); r != nil {
				obj = nil
				err = fmt.Errorf("%w: allocator panic: %v", ErrAllocationFailed, r)
			}
		}()
		return fn()
	}
}

var (
	errorType     = reflect.TypeOf((*error)(nil)).Elem()
	objectType    = reflect.TypeOf(Object{})
	objectPtrType = reflect.TypeOf((*Object)(nil))
)

func reflectAllocator(a any) (*allocAdapter, error) {
	rv := reflect.ValueOf(a)
	if !rv.IsValid() || ((rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface) && rv.IsNil()) {
		return nil, ErrNoAllocator
	}

	allocMethod := methodByName(rv, "Allocate", "Alloc", "NewObject")
	if !allocMethod.IsValid() {
		return nil, fmt.Errorf("%w: allocator has no Allocate method", ErrNoAllocator)
	}
	if allocMethod.Type().NumIn() != 0 || allocMethod.Type().NumOut() < 1 || allocMethod.Type().NumOut() > 2 {
		return nil, fmt.Errorf("%w: unsupported allocation signature %s", ErrNoAllocator, allocMethod.Type())
	}

	releaseMethod := methodByName(rv, "Release", "Free", "ReleaseObject")
	adapter := &allocAdapter{allocate: func() (*Object, error) {
		return callAllocate(allocMethod)
	}}
	if releaseMethod.IsValid() {
		if releaseMethod.Type().NumIn() != 1 {
			return nil, fmt.Errorf("%w: unsupported release signature %s", ErrNoAllocator, releaseMethod.Type())
		}
		adapter.release = func(o *Object) { callRelease(releaseMethod, o) }
	}
	return adapter, nil
}

func methodByName(v reflect.Value, names ...string) reflect.Value {
	for _, name := range names {
		if m := v.MethodByName(name); m.IsValid() {
			return m
		}
	}
	return reflect.Value{}
}

func callAllocate(m reflect.Value) (obj *Object, err error) {
	defer func() {
		if r := recover(); r != nil {
			obj = nil
			err = fmt.Errorf("%w: allocator panic: %v", ErrAllocationFailed, r)
		}
	}()

	outs := m.Call(nil)
	if len(outs) == 2 {
		ev := outs[1]
		if ev.Type().Implements(errorType) {
			if !ev.IsNil() {
				err = ev.Interface().(error)
			}
		} else if !ev.IsZero() {
			return nil, fmt.Errorf("%w: second result is not an error", ErrAllocationFailed)
		}
	}

	first := outs[0]
	if !first.IsValid() {
		if err != nil {
			return nil, err
		}
		return nil, ErrNilObject
	}
	if first.Type().Implements(errorType) && (first.Kind() == reflect.Interface || first.Kind() == reflect.Ptr) {
		if first.IsNil() {
			if err != nil {
				return nil, err
			}
			return nil, ErrNilObject
		}
		if err == nil {
			err = first.Interface().(error)
		}
		return nil, err
	}
	if first.Kind() == reflect.Interface && !first.IsNil() {
		first = first.Elem()
	}

	switch first.Kind() {
	case reflect.Ptr, reflect.Interface:
		if first.IsNil() {
			if err != nil {
				return nil, err
			}
			return nil, ErrNilObject
		}
		if first.Type() == objectPtrType {
			return first.Interface().(*Object), err
		}
		return nil, fmt.Errorf("%w: unsupported allocation result %s", ErrInvalidReconstruction, first.Type())
	case reflect.Struct:
		if first.Type() == objectType {
			o := first.Interface().(Object)
			return &o, err
		}
		if id, ok := reflectObjectID(first); ok {
			if err != nil {
				return nil, err
			}
			return &Object{ID: id, Fields: make(map[string]Value)}, err
		}
		return nil, fmt.Errorf("%w: unsupported allocation result %s", ErrInvalidReconstruction, first.Type())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if err != nil {
			return nil, err
		}
		return &Object{ID: ObjectID(first.Int()), Fields: make(map[string]Value)}, err
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if err != nil {
			return nil, err
		}
		return &Object{ID: ObjectID(first.Uint()), Fields: make(map[string]Value)}, err
	default:
		return nil, fmt.Errorf("%w: unsupported allocation result %s", ErrInvalidReconstruction, first.Type())
	}
}

func reflectObjectID(v reflect.Value) (ObjectID, bool) {
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	f := v.FieldByName("ID")
	if !f.IsValid() {
		return 0, false
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ObjectID(f.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return ObjectID(f.Uint()), true
	default:
		return 0, false
	}
}

func callRelease(m reflect.Value, o *Object) {
	if o == nil || !m.IsValid() {
		return
	}
	defer func() { _ = recover() }()

	in := m.Type().In(0)
	var arg reflect.Value
	switch {
	case in == objectPtrType:
		arg = reflect.ValueOf(o)
	case in == objectType:
		arg = reflect.ValueOf(*o)
	case in.Kind() == reflect.Interface && objectPtrType.Implements(in):
		arg = reflect.ValueOf(o)
	case isNumeric(in.Kind()):
		arg = reflect.New(in).Elem()
		switch in.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			arg.SetInt(int64(o.ID))
		default:
			arg.SetUint(uint64(o.ID))
		}
	default:
		if reflect.TypeOf(o).AssignableTo(in) {
			arg = reflect.ValueOf(o)
		} else {
			return
		}
	}
	m.Call([]reflect.Value{arg})
}

func isNumeric(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}
