package netsec

import (
	"fmt"
	"reflect"
	"strings"
)

// ServerHello contains the pre_shared_key-related portion of a TLS 1.3
// ServerHello.
type ServerHello struct {
	SelectedIdentityPresent bool
	SelectedIdentity        any
}

// ClientHello contains the pre_shared_key-related portion of a TLS 1.3
// ClientHello.
type ClientHello struct {
	PSKIdentities any
	PSKBinders    any
}

// ServerHelloMsg is an alternative spelling of ServerHello.
type ServerHelloMsg = ServerHello

// ClientHelloMsg is an alternative spelling of ClientHello.
type ClientHelloMsg = ClientHello

// ValidateServerHelloSelectedIdentity validates the selected_identity from a
// ServerHello against the identities and binders offered by a ClientHello.
func ValidateServerHelloSelectedIdentity(serverHello, clientHello any) error {
	return ValidateSelectedIdentity(serverHello, clientHello)
}

// ValidateServerHello is an alias for ValidateServerHelloSelectedIdentity.
func ValidateServerHello(serverHello, clientHello any) error {
	return ValidateServerHelloSelectedIdentity(serverHello, clientHello)
}

// CheckServerHello is an alias for ValidateServerHelloSelectedIdentity.
func CheckServerHello(serverHello, clientHello any) error {
	return ValidateServerHelloSelectedIdentity(serverHello, clientHello)
}

// CheckServerHelloIdentity is an alias for
// ValidateServerHelloSelectedIdentity.
func CheckServerHelloIdentity(serverHello, clientHello any) error {
	return ValidateServerHelloSelectedIdentity(serverHello, clientHello)
}

// ValidateServerHelloIdentity is an alias for
// ValidateServerHelloSelectedIdentity.
func ValidateServerHelloIdentity(serverHello, clientHello any) error {
	return ValidateServerHelloSelectedIdentity(serverHello, clientHello)
}

// ValidateServerHelloPSK is an alias for
// ValidateServerHelloSelectedIdentity.
func ValidateServerHelloPSK(serverHello, clientHello any) error {
	return ValidateServerHelloSelectedIdentity(serverHello, clientHello)
}

func parseSelectionArgs(args []any) (selectionInput, error) {
	if len(args) == 0 {
		return selectionInput{}, fmt.Errorf("netsec: selected_identity is required")
	}

	var input selectionInput
	selected, ok := asInt(args[0])
	if ok {
		input.selected = selected
	} else if err := consumeValue(args[0], "", &input); err != nil {
		return selectionInput{}, err
	}

	for _, arg := range args[1:] {
		if err := consumeValue(arg, "", &input); err != nil {
			return selectionInput{}, err
		}
	}

	if !input.identitiesParsed {
		// A missing identity list is equivalent to an empty list. The range
		// check below will reject every index, including zero.
		input.identityCount = 0
	}
	return input, nil
}

func consumeValue(value any, hint string, input *selectionInput) error {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case PSKOffer:
		consumeIdentities(v.Identities, input)
		if v.Binders != nil {
			input.binders = cloneByteSlices(v.Binders)
			input.bindersPresent = true
		}
		if v.Binding != nil {
			input.expectedBinder = append([]byte(nil), v.Binding...)
			input.expectedBinderSet = true
		}
		return nil
	case *PSKOffer:
		if v == nil {
			return nil
		}
		return consumeValue(*v, hint, input)
	case []PSKIdentity:
		consumeIdentities(v, input)
		return nil
	case []*PSKIdentity:
		identities := make([]PSKIdentity, 0, len(v))
		for _, identity := range v {
			if identity != nil {
				identities = append(identities, *identity)
			} else {
				identities = append(identities, PSKIdentity{})
			}
		}
		consumeIdentities(identities, input)
		return nil
	case [][]byte:
		return consumeByteSlices(v, hint, input)
	case []PSKBinder:
		binders := make([][]byte, len(v))
		for i := range v {
			binders[i] = []byte(v[i])
		}
		return consumeByteSlices(binders, "binders", input)
	case []string:
		identities := make([]PSKIdentity, len(v))
		for i := range v {
			identities[i].Identity = []byte(v[i])
		}
		consumeIdentities(identities, input)
		return nil
	case string:
		if input.identitiesParsed {
			input.expectedBinder = []byte(v)
			input.expectedBinderSet = true
			return nil
		}
		input.identityCount = 1
		input.identitiesParsed = true
		return nil
	case []byte:
		input.expectedBinder = append([]byte(nil), v...)
		input.expectedBinderSet = true
		return nil
	case PSKBinder:
		input.expectedBinder = append([]byte(nil), v...)
		input.expectedBinderSet = true
		return nil
	case ServerHello:
		return consumeServerHello(reflect.ValueOf(v), input)
	case *ServerHello:
		if v == nil {
			return nil
		}
		return consumeServerHello(reflect.ValueOf(*v), input)
	case ClientHello:
		return consumeClientHello(reflect.ValueOf(v), input)
	case *ClientHello:
		if v == nil {
			return nil
		}
		return consumeClientHello(reflect.ValueOf(*v), input)
	}

	if n, ok := asInt(value); ok {
		if !input.identitiesParsed {
			input.identityCount = int(n)
			input.identitiesParsed = true
			return nil
		}
		return nil
	}

	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Struct:
		return consumeStruct(rv, input)
	case reflect.Slice, reflect.Array:
		return consumeReflectSlice(rv, hint, input)
	default:
		return nil
	}
}

func consumeStruct(value reflect.Value, input *selectionInput) error {
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := normalizeName(field.Name)
		fieldValue := value.Field(i)

		switch {
		case name == "selectedidentity" || name == "selectedidentityindex" || name == "index":
			if n, ok := asInt(fieldValue.Interface()); ok {
				input.selected = n
			}
		case name == "identitycount" || name == "identitiescount" || name == "length" || name == "len":
			if n, ok := asInt(fieldValue.Interface()); ok {
				input.identityCount = int(n)
				input.identitiesParsed = true
			}
		case strings.Contains(name, "binder") && strings.Contains(name, "expected"):
			if data, ok := asBytes(fieldValue); ok {
				input.expectedBinder = data
				input.expectedBinderSet = true
			}
		case name == "binders" || name == "pskbinders" || name == "pskbinderentries":
			if binders, ok := asByteSlices(fieldValue); ok {
				input.binders = binders
				input.bindersPresent = true
			} else if data, ok := asBytes(fieldValue); ok {
				input.expectedBinder = data
				input.expectedBinderSet = true
			}
		case name == "binding" || name == "binder" || name == "selectedbinder" || name == "bindervalue" || name == "expectedbinder" || name == "selectedidentitybinding":
			if data, ok := asBytes(fieldValue); ok {
				input.expectedBinder = data
				input.expectedBinderSet = true
			}
		case strings.Contains(name, "identities") || strings.Contains(name, "identitylist") || strings.Contains(name, "pre shared identities"):
			if identities, bindings, ok := asIdentities(fieldValue); ok {
				consumeIdentityInfo(identities, bindings, input)
			}
		default:
			if field.Anonymous {
				if err := consumeValue(fieldValue.Interface(), "", input); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func consumeServerHello(value reflect.Value, input *selectionInput) error {
	return consumeStruct(value, input)
}

func consumeClientHello(value reflect.Value, input *selectionInput) error {
	return consumeStruct(value, input)
}

func consumeReflectSlice(value reflect.Value, hint string, input *selectionInput) error {
	if value.Kind() == reflect.Slice && value.IsNil() {
		return nil
	}
	if binders, ok := asByteSlices(value); ok {
		return consumeByteSlices(binders, hint, input)
	}
	if identities, bindings, ok := asIdentities(value); ok {
		consumeIdentityInfo(identities, bindings, input)
		return nil
	}
	return nil
}

func consumeByteSlices(values [][]byte, hint string, input *selectionInput) error {
	hint = normalizeName(hint)
	if strings.Contains(hint, "binder") || input.identitiesParsed {
		if !input.bindersPresent {
			input.binders = cloneByteSlices(values)
			input.bindersPresent = true
			return nil
		}
	}

	identities := make([]PSKIdentity, len(values))
	for i := range values {
		identities[i].Identity = append([]byte(nil), values[i]...)
	}
	consumeIdentities(identities, input)
	return nil
}

func consumeIdentities(identities []PSKIdentity, input *selectionInput) {
	input.identityCount = len(identities)
	input.identitiesParsed = true
}

func consumeIdentityInfo(identities []PSKIdentity, bindings [][]byte, input *selectionInput) {
	consumeIdentities(identities, input)
	input.identityBindings = bindings
}

func asInt(value any) (int64, bool) {
	if value == nil {
		return 0, false
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(rv.Uint()), true
	default:
		return 0, false
	}
}

func asBytes(value reflect.Value) ([]byte, bool) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Slice || value.Type().Elem().Kind() != reflect.Uint8 {
		return nil, false
	}
	if value.IsNil() {
		return nil, false
	}
	return append([]byte(nil), value.Bytes()...), true
}

func asByteSlices(value reflect.Value) ([][]byte, bool) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, false
	}
	elem := value.Type().Elem()
	if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.Uint8 {
		return nil, false
	}

	out := make([][]byte, value.Len())
	for i := 0; i < value.Len(); i++ {
		data, ok := asBytes(value.Index(i))
		if !ok {
			out[i] = nil
			continue
		}
		out[i] = data
	}
	return out, true
}

func asIdentities(value reflect.Value) ([]PSKIdentity, [][]byte, bool) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil, nil, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, nil, false
	}
	elem := value.Type().Elem()
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct && elem.Kind() != reflect.Interface {
		return nil, nil, false
	}

	out := make([]PSKIdentity, value.Len())
	bindings := make([][]byte, value.Len())
	for i := 0; i < value.Len(); i++ {
		item := value.Index(i)
		for item.Kind() == reflect.Pointer || item.Kind() == reflect.Interface {
			if item.IsNil() {
				item = reflect.Value{}
				break
			}
			item = item.Elem()
		}
		if !item.IsValid() || item.Kind() != reflect.Struct {
			continue
		}
		out[i], bindings[i] = identityFromStruct(item)
	}
	return out, bindings, true
}

func identityFromStruct(value reflect.Value) (PSKIdentity, []byte) {
	var identity PSKIdentity
	var binding []byte
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := normalizeName(field.Name)
		switch {
		case name == "identity" || name == "label" || name == "bytes":
			if data, ok := asBytes(value.Field(i)); ok {
				identity.Identity = data
			}
		case name == "binding" || name == "binder" || name == "selectedbinding":
			if data, ok := asBytes(value.Field(i)); ok {
				binding = data
			}
		case name == "obfuscatedticketage" || name == "ticketage":
			if n, ok := asInt(value.Field(i).Interface()); ok {
				identity.ObfuscatedTicketAge = uint32(n)
			}
		}
	}
	return identity, binding
}

func cloneByteSlices(values [][]byte) [][]byte {
	out := make([][]byte, len(values))
	for i := range values {
		out[i] = append([]byte(nil), values[i]...)
	}
	return out
}

func normalizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
