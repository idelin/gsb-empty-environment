package canonicalkey

import (
	"fmt"
	"sort"
	"strconv"
)

const canonicalFormatVersion = "1"

type encoder struct {
	buf    []byte
	limits Limits
	depth  int
	err    error
}

func CanonicalRequest(request Request, limits Limits) ([]byte, error) {
	limits = limits.withDefaults()
	if request.Tenant == "" {
		return nil, fmt.Errorf("%w: tenant is required", ErrInvalidRequest)
	}
	if request.ParameterType == "" {
		return nil, fmt.Errorf("%w: parameter_type is required", ErrInvalidRequest)
	}
	if !request.ConfigVersion.Present {
		return nil, ErrMissingConfig
	}
	if request.ConfigVersion.Null {
		return nil, ErrNullConfig
	}
	if request.ConfigVersion.Value == "" {
		return nil, ErrEmptyConfig
	}
	if err := validateUTF8(request.Tenant); err != nil {
		return nil, err
	}
	if err := validateUTF8(request.ParameterType); err != nil {
		return nil, err
	}
	if err := validateUTF8(request.ConfigVersion.Value); err != nil {
		return nil, err
	}

	enc := encoder{limits: limits}
	enc.writeRaw('K')
	enc.writeRaw(canonicalFormatVersion[0])
	enc.writeTaggedString('T', request.Tenant)
	enc.writeTaggedString('Y', request.ParameterType)
	enc.writeTaggedString('V', request.ConfigVersion.Value)
	enc.writeRaw('P')
	if !request.Params.Present {
		enc.writeRaw('X')
	} else if request.Params.Null {
		if err := enc.writeValue(NullValue{}); err != nil {
			return nil, err
		}
	} else {
		if err := enc.writeValue(request.Params.Value); err != nil {
			return nil, err
		}
	}
	if enc.err != nil {
		return nil, enc.err
	}
	return enc.buf, nil
}

func (enc *encoder) writeRaw(value byte) {
	if enc.err != nil {
		return
	}
	if len(enc.buf)+1 > enc.limits.MaxCanonicalBytes {
		enc.err = ErrBudgetExceeded
		return
	}
	enc.buf = append(enc.buf, value)
}

func (enc *encoder) writeBytes(value []byte) {
	if enc.err != nil {
		return
	}
	if len(enc.buf)+len(value) > enc.limits.MaxCanonicalBytes {
		enc.err = ErrBudgetExceeded
		return
	}
	enc.buf = append(enc.buf, value...)
}

func (enc *encoder) writeCount(count int) {
	enc.writeBytes([]byte(strconv.Itoa(count)))
	enc.writeRaw(':')
}

func (enc *encoder) writeTaggedString(tag byte, value string) {
	if enc.err != nil {
		return
	}
	if err := validateUTF8(value); err != nil {
		enc.err = err
		return
	}
	enc.writeRaw(tag)
	enc.writeCount(len(value))
	enc.writeBytes([]byte(value))
}

func (enc *encoder) writeValue(value Value) error {
	if enc.err != nil {
		return enc.err
	}
	switch v := value.(type) {
	case NullValue:
		enc.writeRaw('Z')
	case bool:
		enc.writeRaw('B')
		if v {
			enc.writeRaw('1')
		} else {
			enc.writeRaw('0')
		}
	case string:
		enc.writeTaggedString('S', v)
	case Number:
		return enc.writeNumber(string(v))
	case Array:
		return enc.writeArray(v)
	case Object:
		return enc.writeObject(v)
	case Ref:
		return fmt.Errorf("%w: %q", ErrDanglingReference, v.Node)
	case Graph:
		return enc.writeGraph(v)
	case nil:
		return fmt.Errorf("%w: use NullValue for an explicit null", ErrInvalidValue)
	default:
		return fmt.Errorf("%w: %T", ErrInvalidValue, value)
	}
	return enc.err
}

func (enc *encoder) writeNumber(raw string) error {
	number, err := canonicalNumber(raw, enc.limits.MaxCanonicalBytes)
	if err != nil {
		enc.err = err
		return err
	}
	enc.writeRaw('N')
	enc.writeCount(len(number))
	enc.writeBytes([]byte(number))
	return enc.err
}

func (enc *encoder) enterContainer() error {
	enc.depth++
	if enc.depth > enc.limits.MaxDepth {
		enc.err = ErrDepthExceeded
		return enc.err
	}
	return nil
}

func (enc *encoder) exitContainer() {
	enc.depth--
}

func (enc *encoder) writeArray(values Array) error {
	if err := enc.enterContainer(); err != nil {
		return err
	}
	defer enc.exitContainer()
	enc.writeRaw('A')
	enc.writeCount(len(values))
	for _, value := range values {
		if err := enc.writeValue(value); err != nil {
			return err
		}
	}
	return enc.err
}

func (enc *encoder) writeObject(object Object) error {
	if err := enc.enterContainer(); err != nil {
		return err
	}
	defer enc.exitContainer()
	keys := make([]string, 0, len(object))
	for key := range object {
		if err := validateUTF8(key); err != nil {
			enc.err = err
			return err
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	enc.writeRaw('M')
	enc.writeCount(len(keys))
	for _, key := range keys {
		enc.writeTaggedString('S', key)
		if err := enc.writeValue(object[key]); err != nil {
			return err
		}
	}
	return enc.err
}

func (enc *encoder) writeGraph(graph Graph) error {
	if err := enc.enterContainer(); err != nil {
		return err
	}
	defer enc.exitContainer()
	if graph.Nodes == nil {
		return fmt.Errorf("%w: missing nodes", ErrInvalidGraph)
	}
	reachable, err := collectGraphNodes(graph, enc.limits, enc.depth)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	enc.writeRaw('G')
	enc.writeTaggedString('S', graph.Root.Node)
	enc.writeCount(len(ids))
	for _, id := range ids {
		enc.writeTaggedString('S', id)
		if err := enc.writeScopedValue(graph.Nodes[id], graph); err != nil {
			return err
		}
	}
	return enc.err
}

func (enc *encoder) writeScopedValue(value Value, scope Graph) error {
	if enc.err != nil {
		return enc.err
	}
	switch v := value.(type) {
	case Ref:
		if _, ok := scope.Nodes[v.Node]; !ok {
			return fmt.Errorf("%w: %q", ErrDanglingReference, v.Node)
		}
		enc.writeTaggedString('R', v.Node)
	case Array:
		if err := enc.enterContainer(); err != nil {
			return err
		}
		defer enc.exitContainer()
		enc.writeRaw('A')
		enc.writeCount(len(v))
		for _, child := range v {
			if err := enc.writeScopedValue(child, scope); err != nil {
				return err
			}
		}
	case Object:
		if err := enc.enterContainer(); err != nil {
			return err
		}
		defer enc.exitContainer()
		keys := make([]string, 0, len(v))
		for key := range v {
			if err := validateUTF8(key); err != nil {
				enc.err = err
				return err
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		enc.writeRaw('M')
		enc.writeCount(len(keys))
		for _, key := range keys {
			enc.writeTaggedString('S', key)
			if err := enc.writeScopedValue(v[key], scope); err != nil {
				return err
			}
		}
	case Graph:
		return enc.writeGraph(v)
	default:
		return enc.writeValue(value)
	}
	return enc.err
}
