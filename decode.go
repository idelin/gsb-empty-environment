package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
)

type wireEnvelope struct {
	Type    TypeID                     `json:"type"`
	Version uint32                     `json:"version"`
	Data    map[string]json.RawMessage `json:"data"`
}

type decoder struct {
	registry  *Registry
	data      json.RawMessage
	maxDepth  int
	active    map[string]bool
	validated map[string]bool
}

// Decode validates and decodes a self-describing cache payload. The returned
// value is a pointer to the concrete type registered for the envelope's
// type/version pair; no attacker-selected Go type is ever instantiated.
func (r *Registry) Decode(payload []byte) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrInvalidEnvelope)
	}
	maxDepth, maxBytes := r.limits()
	if len(payload) > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds limit %d", ErrPayloadTooLarge, len(payload), maxBytes)
	}
	// The standard library JSON scanner also has its own depth ceiling. Scan
	// bytes first so arbitrarily deep input is classified as ErrMaxDepth
	// instead of a generic scanner error. The envelope object adds one level.
	if err := checkRawDepth(payload, maxDepth+1); err != nil {
		return nil, err
	}

	envelope, err := parseObject(payload)
	if err != nil {
		return nil, err
	}
	if err := requireOnlyKeys(envelope, "type", "version", "data"); err != nil {
		return nil, err
	}
	for _, key := range []string{"type", "version", "data"} {
		if _, ok := envelope[key]; !ok {
			return nil, fmt.Errorf("%w: missing envelope field %q", ErrInvalidEnvelope, key)
		}
	}

	typeID, err := parseTypeID(envelope["type"])
	if err != nil {
		return nil, err
	}
	version, err := parseVersion(envelope["version"])
	if err != nil {
		return nil, err
	}
	data := json.RawMessage(bytes.TrimSpace(envelope["data"]))
	if !isObject(data) {
		return nil, fmt.Errorf("%w: data must be a JSON object", ErrInvalidEnvelope)
	}

	spec := r.lookup(typeID)
	if spec == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, typeID)
	}
	if version != spec.version {
		return nil, fmt.Errorf("%w: type %q has version %d, payload has %d", ErrVersionMismatch, typeID, spec.version, version)
	}

	if err := scanJSONDepth(data, maxDepth); err != nil {
		return nil, err
	}

	d := &decoder{
		registry:  r,
		data:      data,
		maxDepth:  maxDepth,
		active:    make(map[string]bool),
		validated: make(map[string]bool),
	}
	if err := d.validateReferences(data, "#/data", 1); err != nil {
		return nil, err
	}

	value, err := d.decodeObject(spec, data, "#/data", 1)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// DecodeAs is a typed helper for Decode. It rejects a successful decode whose
// concrete type is not *T.
func DecodeAs[T any](r *Registry, payload []byte) (*T, error) {
	value, err := r.Decode(payload)
	if err != nil {
		return nil, err
	}
	typed, ok := value.(*T)
	if !ok {
		return nil, fmt.Errorf("%w: got %T, want %T", ErrTypeMismatch, value, (*T)(nil))
	}
	return typed, nil
}

// Encode produces a valid self-describing envelope for a registered type. It
// is intended for trusted local values, not as a decoder-side security check.
func (r *Registry) Encode(value any) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrInvalidSpec)
	}
	if value == nil {
		return nil, fmt.Errorf("%w: nil value", ErrUnregisteredType)
	}

	rawValue := reflect.ValueOf(value)
	var structType reflect.Type
	var target any
	if rawValue.Kind() == reflect.Pointer {
		if rawValue.IsNil() {
			return nil, fmt.Errorf("%w: nil pointer", ErrUnregisteredType)
		}
		structType = rawValue.Type().Elem()
		target = value
	} else if rawValue.Kind() == reflect.Struct {
		structType = rawValue.Type()
		copyPtr := reflect.New(structType)
		copyPtr.Elem().Set(rawValue)
		target = copyPtr.Interface()
	} else {
		return nil, fmt.Errorf("%w: %v", ErrUnregisteredType, rawValue.Type())
	}

	spec := r.specForType(structType)
	if spec == nil {
		return nil, fmt.Errorf("%w: %v", ErrUnregisteredType, structType)
	}
	if err := validateDecodedValue(target, spec); err != nil {
		return nil, err
	}

	rv := reflect.ValueOf(target).Elem()
	data := make(map[string]json.RawMessage, len(spec.order))
	for _, name := range spec.order {
		field := spec.fields[name]
		fv := rv.Field(field.index)
		if !field.spec.Required && fv.IsZero() {
			continue
		}
		encoded, err := marshalFieldValue(fv)
		if err != nil {
			return nil, fmt.Errorf("%w: field %q: %v", ErrValidation, name, err)
		}
		data[name] = encoded
	}

	payload, err := json.Marshal(wireEnvelope{
		Type:    spec.id,
		Version: spec.version,
		Data:    data,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSpec, err)
	}
	return payload, nil
}

func marshalFieldValue(value reflect.Value) (json.RawMessage, error) {
	if value.Kind() == reflect.Slice && value.IsNil() {
		return json.RawMessage("[]"), nil
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, fmt.Errorf("required pointer field is nil")
	}
	encoded, err := json.Marshal(value.Interface())
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func (d *decoder) decodeObject(spec *compiledSpec, raw json.RawMessage, path string, depth int) (any, error) {
	if depth > d.maxDepth {
		return nil, fmt.Errorf("%w: at %s", ErrMaxDepth, path)
	}
	if d.active[path] {
		return nil, fmt.Errorf("%w: at %s", ErrCycle, path)
	}
	d.active[path] = true
	defer delete(d.active, path)

	obj, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	for key := range obj {
		if _, ok := spec.fields[key]; !ok {
			return nil, fmt.Errorf("%w: type %q at %s: %q", ErrFieldNotAllowed, spec.id, path, key)
		}
	}
	for name, field := range spec.fields {
		if field.spec.Required {
			if _, ok := obj[name]; !ok {
				return nil, fmt.Errorf("%w: type %q at %s: %q", ErrMissingField, spec.id, path, name)
			}
		}
	}

	target, factoryErr := invokeFactory(spec.newFn)
	if factoryErr != nil {
		return nil, fmt.Errorf("%w: type %q: %v", ErrInvalidSpec, spec.id, factoryErr)
	}
	if target == nil {
		return nil, fmt.Errorf("%w: type %q: New returned nil", ErrInvalidSpec, spec.id)
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Type().Elem() != spec.typ {
		return nil, fmt.Errorf("%w: type %q: New returned %T", ErrInvalidSpec, spec.id, target)
	}
	elem := rv.Elem()

	for _, name := range spec.order {
		field := spec.fields[name]
		fieldRaw, present := obj[name]
		if !present {
			continue
		}
		fieldPath := path + "/" + escapePointer(name)
		decoded, err := d.decodeFieldValue(field.typ, fieldRaw, fieldPath, depth)
		if err != nil {
			return nil, err
		}
		dst := elem.Field(field.index)
		if !decoded.Type().AssignableTo(dst.Type()) {
			return nil, fmt.Errorf("%w: type %q field %q: cannot assign %v to %v", ErrInvalidSpec, spec.id, name, decoded.Type(), dst.Type())
		}
		dst.Set(decoded)
	}

	if err := validateDecodedValue(target, spec); err != nil {
		return nil, err
	}
	return target, nil
}

func (d *decoder) decodeFieldValue(typ reflect.Type, raw json.RawMessage, path string, depth int) (reflect.Value, error) {
	resolved, resolvedPath, resolvedDepth, err := d.resolveReferenceChain(raw, path, depth)
	if err != nil {
		return reflect.Value{}, err
	}
	valueDepth := childDepth(resolved, resolvedDepth)
	if valueDepth > d.maxDepth {
		return reflect.Value{}, fmt.Errorf("%w: at %s", ErrMaxDepth, resolvedPath)
	}
	return d.decodeIntoType(typ, resolved, resolvedPath, valueDepth)
}

func (d *decoder) decodeIntoType(typ reflect.Type, raw json.RawMessage, path string, depth int) (reflect.Value, error) {
	if depth > d.maxDepth {
		return reflect.Value{}, fmt.Errorf("%w: at %s", ErrMaxDepth, path)
	}
	if isNull(raw) {
		return reflect.Value{}, fmt.Errorf("%w: null is not allowed at %s", ErrInvalidEnvelope, path)
	}

	switch typ.Kind() {
	case reflect.Pointer:
		elemType := typ.Elem()
		if elemType.Kind() == reflect.Struct {
			spec := d.registry.specForType(elemType)
			if spec == nil {
				return reflect.Value{}, fmt.Errorf("%w: %v at %s", ErrUnregisteredFieldType, elemType, path)
			}
			value, err := d.decodeObject(spec, raw, path, depth)
			if err != nil {
				return reflect.Value{}, err
			}
			return reflect.ValueOf(value), nil
		}
		value, err := d.decodeIntoType(elemType, raw, path, depth)
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(elemType)
		ptr.Elem().Set(value)
		return ptr, nil

	case reflect.Struct:
		spec := d.registry.specForType(typ)
		if spec == nil {
			return reflect.Value{}, fmt.Errorf("%w: %v at %s", ErrUnregisteredFieldType, typ, path)
		}
		value, err := d.decodeObject(spec, raw, path, depth)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(value).Elem(), nil

	case reflect.Slice, reflect.Array:
		if containsStruct(typ.Elem()) {
			return d.decodeStructCollection(typ, raw, path, depth)
		}
		return decodeScalar(typ, raw, path)

	default:
		return decodeScalar(typ, raw, path)
	}
}

func decodeScalar(typ reflect.Type, raw json.RawMessage, path string) (reflect.Value, error) {
	dst := reflect.New(typ)
	if err := json.Unmarshal(raw, dst.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: %v at %s", ErrInvalidEnvelope, err, path)
	}
	return dst.Elem(), nil
}

func (d *decoder) decodeStructCollection(typ reflect.Type, raw json.RawMessage, path string, depth int) (reflect.Value, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: %v at %s", ErrInvalidEnvelope, err, path)
	}

	elemType := typ.Elem()
	var out reflect.Value
	if typ.Kind() == reflect.Slice {
		out = reflect.MakeSlice(typ, len(items), len(items))
	} else {
		if len(items) != typ.Len() {
			return reflect.Value{}, fmt.Errorf("%w: array length %d, want %d at %s", ErrInvalidEnvelope, len(items), typ.Len(), path)
		}
		out = reflect.New(typ).Elem()
	}
	for i, item := range items {
		itemPath := path + "/" + strconv.Itoa(i)
		value, err := d.decodeIntoType(elemType, item, itemPath, childDepth(item, depth))
		if err != nil {
			return reflect.Value{}, err
		}
		out.Index(i).Set(value)
	}
	return out, nil
}

func containsStruct(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		return true
	case reflect.Slice, reflect.Array:
		return containsStruct(t.Elem())
	default:
		return false
	}
}

// childDepth counts JSON containers, not scalar field values. A reference hop
// has already been counted by resolveReferenceChain.
func childDepth(raw json.RawMessage, parentDepth int) int {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return parentDepth + 1
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return parentDepth + 1
	}
	return parentDepth
}

func (d *decoder) resolveReferenceChain(raw json.RawMessage, path string, depth int) (json.RawMessage, string, int, error) {
	seen := make(map[string]bool)
	for {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return nil, "", 0, fmt.Errorf("%w: empty value at %s", ErrInvalidEnvelope, path)
		}
		if !isObject(trimmed) {
			return trimmed, path, depth, nil
		}
		obj, err := parseObject(trimmed)
		if err != nil {
			return nil, "", 0, err
		}
		refRaw, ok := obj["$ref"]
		if !ok {
			return trimmed, path, depth, nil
		}
		if len(obj) != 1 {
			return nil, "", 0, fmt.Errorf("%w: $ref object at %s has extra fields", ErrInvalidReference, path)
		}
		ref, err := parseString(refRaw)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%w: %v at %s", ErrInvalidReference, err, path)
		}
		target, targetPath, err := resolvePointer(d.data, ref)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%w: %v at %s", ErrInvalidReference, err, path)
		}
		if d.active[targetPath] {
			return nil, "", 0, fmt.Errorf("%w: reference to active object %s", ErrCycle, targetPath)
		}
		if seen[targetPath] {
			return nil, "", 0, fmt.Errorf("%w: reference chain revisits %s", ErrCycle, targetPath)
		}
		seen[targetPath] = true
		depth++
		if depth > d.maxDepth {
			return nil, "", 0, fmt.Errorf("%w: reference chain at %s", ErrMaxDepth, targetPath)
		}
		raw, path = target, targetPath
	}
}

func (d *decoder) validateReferences(raw json.RawMessage, path string, depth int) error {
	if depth > d.maxDepth {
		return fmt.Errorf("%w: at %s", ErrMaxDepth, path)
	}
	if d.validated[path] {
		return nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty value at %s", ErrInvalidEnvelope, path)
	}

	switch trimmed[0] {
	case '{':
		obj, err := parseObject(trimmed)
		if err != nil {
			return err
		}
		if refRaw, ok := obj["$ref"]; ok {
			if len(obj) != 1 {
				return fmt.Errorf("%w: $ref object at %s has extra fields", ErrInvalidReference, path)
			}
			ref, err := parseString(refRaw)
			if err != nil {
				return fmt.Errorf("%w: %v at %s", ErrInvalidReference, err, path)
			}
			target, targetPath, err := resolvePointer(d.data, ref)
			if err != nil {
				return fmt.Errorf("%w: %v at %s", ErrInvalidReference, err, path)
			}
			if d.active[targetPath] {
				return fmt.Errorf("%w: reference to active object %s", ErrCycle, targetPath)
			}
			if d.validated[targetPath] {
				return nil
			}
			d.active[targetPath] = true
			if err := d.validateReferences(target, targetPath, depth+1); err != nil {
				return err
			}
			delete(d.active, targetPath)
			d.validated[targetPath] = true
			return nil
		}
		for key, value := range obj {
			if err := d.validateReferences(value, path+"/"+escapePointer(key), childDepth(value, depth)); err != nil {
				return err
			}
		}
		d.validated[path] = true
		return nil

	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("%w: %v at %s", ErrInvalidEnvelope, err, path)
		}
		for i, item := range items {
			if err := d.validateReferences(item, path+"/"+strconv.Itoa(i), childDepth(item, depth)); err != nil {
				return err
			}
		}
		d.validated[path] = true
		return nil

	default:
		return nil
	}
}

func validateDecodedValue(target any, spec *compiledSpec) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: type %q validation panicked: %v", ErrValidation, spec.id, recovered)
		}
	}()

	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Type().Elem() != spec.typ {
		return fmt.Errorf("%w: type %q: unexpected value %T", ErrInvalidSpec, spec.id, target)
	}
	elem := rv.Elem()

	for _, name := range spec.order {
		field := spec.fields[name]
		value := elem.Field(field.index)
		if field.spec.IntRange != nil {
			if err := checkIntRange(value, field.spec.IntRange); err != nil {
				return fmt.Errorf("%w: type %q field %q: %v", ErrValidation, spec.id, name, err)
			}
		}
		if field.spec.LenRange != nil {
			if err := checkLenRange(value, field.spec.LenRange); err != nil {
				return fmt.Errorf("%w: type %q field %q: %v", ErrValidation, spec.id, name, err)
			}
		}
	}

	if spec.validate != nil {
		if err := spec.validate(target); err != nil {
			return fmt.Errorf("%w: type %q: %v", ErrValidation, spec.id, err)
		}
	}
	return nil
}

func checkIntRange(value reflect.Value, bounds *IntRange) error {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := value.Int()
		if n < bounds.Min || n > bounds.Max {
			return fmt.Errorf("value %d outside [%d,%d]", n, bounds.Min, bounds.Max)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n := value.Uint()
		if n > math.MaxInt64 {
			return fmt.Errorf("value %d outside [%d,%d]", n, bounds.Min, bounds.Max)
		}
		signed := int64(n)
		if signed < bounds.Min || signed > bounds.Max {
			return fmt.Errorf("value %d outside [%d,%d]", n, bounds.Min, bounds.Max)
		}
	default:
		return fmt.Errorf("%v is not an integer", value.Type())
	}
	return nil
}

func checkLenRange(value reflect.Value, bounds *LenRange) error {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String, reflect.Slice, reflect.Array:
	default:
		return fmt.Errorf("%v has no length", value.Type())
	}
	n := value.Len()
	if n < bounds.Min || (bounds.Max != 0 && n > bounds.Max) {
		return fmt.Errorf("length %d outside [%d,%d]", n, bounds.Min, bounds.Max)
	}
	return nil
}

func parseObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("%w: expected JSON object", ErrInvalidEnvelope)
	}

	obj := make(map[string]json.RawMessage)
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("%w: object key is not a string", ErrInvalidEnvelope)
		}
		if _, duplicate := obj[key]; duplicate {
			return nil, fmt.Errorf("%w: key %q", ErrDuplicateField, key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
		}
		obj[key] = value
	}
	token, err = dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return nil, fmt.Errorf("%w: malformed object", ErrInvalidEnvelope)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing JSON value", ErrInvalidEnvelope)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	return obj, nil
}

func scanJSONDepth(raw []byte, maxDepth int) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '{', '[':
			depth++
			if depth > maxDepth {
				return fmt.Errorf("%w: deeper than %d", ErrMaxDepth, maxDepth)
			}
		case '}', ']':
			depth--
		}
	}
}

// checkRawDepth is an allocation-free, string-aware container depth scan. It
// is deliberately independent of encoding/json so a maliciously deep payload
// cannot get past the depth limit by exceeding the JSON scanner's own ceiling.
func checkRawDepth(raw []byte, maxDepth int) error {
	depth := 0
	inString := false
	escaped := false
	for _, b := range raw {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				return fmt.Errorf("%w: deeper than %d", ErrMaxDepth, maxDepth-1)
			}
		case '}', ']':
			depth--
		}
	}
	return nil
}

func parseTypeID(raw json.RawMessage) (TypeID, error) {
	s, err := parseString(raw)
	if err != nil {
		return "", fmt.Errorf("%w: type: %v", ErrInvalidEnvelope, err)
	}
	if s == "" || len(s) > maxTypeIDLength {
		return "", fmt.Errorf("%w: invalid type identifier length", ErrInvalidEnvelope)
	}
	return TypeID(s), nil
}

func parseString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("expected JSON string")
	}
	return s, nil
}

func parseVersion(raw json.RawMessage) (uint32, error) {
	text := string(bytes.TrimSpace(raw))
	if text == "" {
		return 0, fmt.Errorf("%w: empty version", ErrInvalidEnvelope)
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("%w: version must be an unsigned integer", ErrInvalidEnvelope)
		}
	}
	value, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: version: %v", ErrInvalidEnvelope, err)
	}
	return uint32(value), nil
}

func requireOnlyKeys(obj map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range obj {
		if !set[key] {
			return fmt.Errorf("%w: unexpected envelope key %q", ErrInvalidEnvelope, key)
		}
	}
	return nil
}

func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapePointer(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '~' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("truncated '~' escape")
		}
		switch s[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid '~%c' escape", s[i+1])
		}
		i++
	}
	return b.String(), nil
}

func parsePointer(ref string) ([]string, error) {
	if !strings.HasPrefix(ref, "#/data") {
		return nil, fmt.Errorf("reference %q must start with #/data", ref)
	}
	rest := strings.TrimPrefix(ref, "#/data")
	if rest == "" {
		return nil, nil
	}
	if !strings.HasPrefix(rest, "/") {
		return nil, fmt.Errorf("reference %q has malformed path", ref)
	}

	rawParts := strings.Split(rest[1:], "/")
	parts := make([]string, len(rawParts))
	for i, part := range rawParts {
		decoded, err := unescapePointer(part)
		if err != nil {
			return nil, err
		}
		parts[i] = decoded
	}
	return parts, nil
}

func resolvePointer(data json.RawMessage, ref string) (json.RawMessage, string, error) {
	parts, err := parsePointer(ref)
	if err != nil {
		return nil, "", err
	}

	current := json.RawMessage(bytes.TrimSpace(data))
	path := "#/data"
	for _, part := range parts {
		trimmed := json.RawMessage(bytes.TrimSpace(current))
		switch {
		case isObject(trimmed):
			obj, err := parseObject(trimmed)
			if err != nil {
				return nil, "", err
			}
			value, ok := obj[part]
			if !ok {
				return nil, "", fmt.Errorf("path segment %q does not exist", part)
			}
			current = value

		case len(trimmed) > 0 && trimmed[0] == '[':
			var items []json.RawMessage
			if err := json.Unmarshal(trimmed, &items); err != nil {
				return nil, "", err
			}
			index, err := parseArrayIndex(part)
			if err != nil || index >= len(items) {
				return nil, "", fmt.Errorf("invalid array index %q", part)
			}
			current = items[index]

		default:
			return nil, "", fmt.Errorf("cannot descend into non-container at %s", path)
		}
		path += "/" + escapePointer(part)
	}
	return current, path, nil
}

func parseArrayIndex(s string) (int, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("invalid index")
	}
	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
