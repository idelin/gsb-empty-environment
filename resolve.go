package netsec

import (
	"context"
	"fmt"
	"reflect"
)

// KeyResolver selects a verification key. kid and alg are taken only from the
// protected header. A missing protected kid is passed as the empty string even
// if the unprotected header contains a kid.
type KeyResolver interface {
	ResolveKey(ctx context.Context, kid, alg string) (key any, err error)
}

// Resolver is an alias for KeyResolver.
type Resolver = KeyResolver

// KeyResolverFunc adapts a context-aware resolver function.
type KeyResolverFunc func(ctx context.Context, kid, alg string) (any, error)

// ResolveKey implements KeyResolver.
func (f KeyResolverFunc) ResolveKey(ctx context.Context, kid, alg string) (any, error) {
	return f(ctx, kid, alg)
}

// ResolverFunc is an alias for KeyResolverFunc.
type ResolverFunc = KeyResolverFunc

// KeyFunc adapts a resolver that ignores context. Arguments are the protected
// kid and alg, in that order.
type KeyFunc func(kid, alg string) (any, error)

// ResolveKey implements KeyResolver.
func (f KeyFunc) ResolveKey(_ context.Context, kid, alg string) (any, error) {
	return f(kid, alg)
}

// KeyLookupFunc adapts a kid-only lookup function.
type KeyLookupFunc func(kid string) (any, error)

// ResolveKey implements KeyResolver.
func (f KeyLookupFunc) ResolveKey(_ context.Context, kid, _ string) (any, error) {
	return f(kid)
}

// LookupFunc is an alias for KeyLookupFunc.
type LookupFunc = KeyLookupFunc

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	headerType  = reflect.TypeOf(Header{})
	headerPtrTy = reflect.TypeOf((*Header)(nil))
)

func resolveSigningKey(ctx context.Context, source any, header Header) (any, error) {
	if source == nil {
		return nil, ErrKeyNotFound
	}
	sourceValue := reflect.ValueOf(source)
	if sourceValue.Kind() == reflect.Func && sourceValue.IsNil() {
		return nil, ErrKeyNotFound
	}

	switch resolver := source.(type) {
	case KeyResolver:
		key, err := resolver.ResolveKey(ctx, header.KeyID, header.Algorithm)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, ErrKeyNotFound
		}
		return key, nil
	case func(context.Context, string, string) (any, error):
		key, err := resolver(ctx, header.KeyID, header.Algorithm)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	case func(string, string) (any, error):
		key, err := resolver(header.KeyID, header.Algorithm)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	case func(string) (any, error):
		key, err := resolver(header.KeyID)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	case func(context.Context, string) (any, error):
		key, err := resolver(ctx, header.KeyID)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	case func(Header) (any, error):
		key, err := resolver(header)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	case func(context.Context, Header) (any, error):
		key, err := resolver(ctx, header)
		if err != nil {
			return nil, err
		}
		return requireKey(key)
	}

	switch sourceValue.Kind() {
	case reflect.Map:
		return lookupMapKey(sourceValue, header.KeyID)
	case reflect.Func:
		return callResolver(ctx, sourceValue, header)
	default:
		return source, nil
	}
}

func requireKey(key any) (any, error) {
	if key == nil {
		return nil, ErrKeyNotFound
	}
	return key, nil
}

func lookupMapKey(value reflect.Value, kid string) (any, error) {
	keyType := value.Type().Key()
	if keyType.Kind() != reflect.String {
		return nil, fmt.Errorf("%w: key map must have string keys", ErrInvalidKey)
	}

	lookup := reflect.ValueOf(kid)
	if !lookup.Type().AssignableTo(keyType) {
		lookup = lookup.Convert(keyType)
	}
	key := value.MapIndex(lookup)
	if !key.IsValid() {
		return nil, fmt.Errorf("%w: kid %q", ErrKeyNotFound, kid)
	}
	result := key.Interface()
	return requireKey(result)
}

func callResolver(ctx context.Context, fn reflect.Value, header Header) (any, error) {
	functionType := fn.Type()
	if functionType.IsVariadic() {
		return nil, fmt.Errorf("%w: variadic resolver functions are not supported", ErrInvalidKey)
	}
	if functionType.NumIn() > 3 {
		return nil, fmt.Errorf("%w: resolver has too many arguments", ErrInvalidKey)
	}

	args := make([]reflect.Value, 0, functionType.NumIn())
	stringsSeen := 0
	for index := 0; index < functionType.NumIn(); index++ {
		param := functionType.In(index)
		switch {
		case param == contextType:
			args = append(args, reflect.ValueOf(ctx))
		case param == headerType:
			args = append(args, reflect.ValueOf(header))
		case param == headerPtrTy:
			copy := header
			args = append(args, reflect.ValueOf(&copy))
		case param.Kind() == reflect.String:
			var value string
			switch stringsSeen {
			case 0:
				value = header.KeyID
			case 1:
				value = header.Algorithm
			default:
				return nil, fmt.Errorf("%w: resolver has too many string arguments", ErrInvalidKey)
			}
			stringsSeen++
			argument := reflect.ValueOf(value)
			if !argument.Type().AssignableTo(param) {
				if !argument.Type().ConvertibleTo(param) {
					return nil, fmt.Errorf("%w: resolver argument %s cannot accept a string", ErrInvalidKey, param)
				}
				argument = argument.Convert(param)
			}
			args = append(args, argument)
		case param.Kind() == reflect.Interface:
			argument := reflect.ValueOf(header)
			if !argument.Type().AssignableTo(param) {
				return nil, fmt.Errorf("%w: resolver argument %s cannot accept a header", ErrInvalidKey, param)
			}
			args = append(args, argument)
		default:
			return nil, fmt.Errorf("%w: unsupported resolver argument %s", ErrInvalidKey, param)
		}
	}

	results := fn.Call(args)
	if len(results) == 0 || len(results) > 2 {
		return nil, fmt.Errorf("%w: resolver must return a key and optional error", ErrInvalidKey)
	}
	if len(results) == 2 {
		if !results[1].Type().Implements(errorType) {
			return nil, fmt.Errorf("%w: resolver second result must be error", ErrInvalidKey)
		}
		if isNilable(results[1].Kind()) && !results[1].IsNil() {
			return nil, results[1].Interface().(error)
		}
		if !isNilable(results[1].Kind()) {
			return nil, results[1].Interface().(error)
		}
	}

	key := results[0]
	if isNilValue(key) {
		return nil, ErrKeyNotFound
	}
	return key.Interface(), nil
}

func isNilValue(value reflect.Value) bool {
	if !isNilable(value.Kind()) {
		return false
	}
	return value.IsNil()
}

func isNilable(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
