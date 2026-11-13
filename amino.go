package netsec

import (
	"reflect"
	"strings"
)

// AminoRegistry resolves a protobuf type URL to its registered Amino name.
// A false result means the message type has no Amino registration.
type AminoRegistry interface {
	AminoName(typeURL string) (string, bool)
}

// AminoRegistryFunc adapts a lookup function to AminoRegistry.
type AminoRegistryFunc func(typeURL string) (string, bool)

// AminoName implements AminoRegistry.
func (f AminoRegistryFunc) AminoName(typeURL string) (string, bool) {
	return f(typeURL)
}

// MapAminoRegistry is the common type URL -> Amino name registry.
type MapAminoRegistry map[string]string

// AminoName implements AminoRegistry.
func (m MapAminoRegistry) AminoName(typeURL string) (string, bool) {
	name, ok := aminoLookupCandidates(m, typeURL)
	if !ok {
		name, ok = aminoLookupReverse(m, typeURL)
	}
	return name, ok && strings.TrimSpace(name) != ""
}

// AminoRegistryMap is an alias for MapAminoRegistry.
type AminoRegistryMap = MapAminoRegistry

// LookupAminoName resolves a type URL in a caller supplied registry. In
// addition to AminoRegistry implementations, it accepts:
//
//   - map[string]string: type URL -> Amino name;
//   - map[string]bool: true marks a registered type (the type URL is used as
//     its display name);
//   - map[string]struct{}: key presence marks registration;
//   - []string: exact registered type URLs.
//
// Type URLs are looked up both with and without a leading slash.
func LookupAminoName(registry any, typeURL string) (string, bool) {
	if registry == nil || typeURL == "" {
		return "", false
	}
	if registryValue, ok := registry.(AminoRegistry); ok {
		if registryValue == nil {
			return "", false
		}
		name, found := registryValue.AminoName(typeURL)
		return name, found && strings.TrimSpace(name) != ""
	}

	if typed, ok := registry.(map[string]string); ok {
		name, found := aminoLookupCandidates(typed, typeURL)
		if !found {
			name, found = aminoLookupReverse(typed, typeURL)
		}
		return name, found && strings.TrimSpace(name) != ""
	}
	if typed, ok := registry.(MapAminoRegistry); ok {
		name, found := aminoLookupCandidates(typed, typeURL)
		if !found {
			name, found = aminoLookupReverse(typed, typeURL)
		}
		return name, found && strings.TrimSpace(name) != ""
	}
	if typed, ok := registry.(map[string]bool); ok {
		_, found := aminoLookupCandidates(typed, typeURL)
		return typeURL, found
	}
	if typed, ok := registry.(map[string]struct{}); ok {
		_, found := aminoLookupCandidates(typed, typeURL)
		return typeURL, found
	}
	if typed, ok := registry.([]string); ok {
		for _, candidate := range typeURLCandidates(typeURL) {
			for _, registered := range typed {
				if strings.TrimSpace(registered) == candidate {
					return candidate, true
				}
			}
		}
	}

	return lookupAminoByReflection(registry, typeURL)
}

func aminoLookupCandidates[V any](registry map[string]V, typeURL string) (V, bool) {
	var zero V
	for _, candidate := range typeURLCandidates(typeURL) {
		if value, ok := registry[candidate]; ok {
			return value, true
		}
	}
	return zero, false
}

func aminoLookupReverse(registry map[string]string, typeURL string) (string, bool) {
	for aminoName, registeredTypeURL := range registry {
		if strings.TrimSpace(aminoName) == "" {
			continue
		}
		for _, candidate := range typeURLCandidates(typeURL) {
			for _, registeredCandidate := range typeURLCandidates(registeredTypeURL) {
				if candidate == registeredCandidate {
					return aminoName, true
				}
			}
		}
	}
	return "", false
}

func typeURLCandidates(typeURL string) []string {
	candidates := []string{typeURL}
	if strings.HasPrefix(typeURL, "/") {
		candidates = append(candidates, strings.TrimPrefix(typeURL, "/"))
	} else {
		candidates = append(candidates, "/"+typeURL)
	}
	return candidates
}

func lookupAminoByReflection(registry any, typeURL string) (string, bool) {
	value := reflect.ValueOf(registry)
	if !value.IsValid() {
		return "", false
	}
	switch value.Kind() {
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return "", false
		}
		for _, candidate := range typeURLCandidates(typeURL) {
			key := reflect.ValueOf(candidate).Convert(value.Type().Key())
			mapped := value.MapIndex(key)
			if !mapped.IsValid() {
				continue
			}
			switch mapped.Kind() {
			case reflect.String:
				name := mapped.String()
				if strings.TrimSpace(name) == "" {
					return "", false
				}
				return name, true
			case reflect.Bool:
				return typeURL, mapped.Bool()
			case reflect.Struct:
				return typeURL, true
			case reflect.Interface, reflect.Pointer:
				return typeURL, !mapped.IsZero()
			default:
				return typeURL, true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			item := value.Index(i)
			if item.Kind() == reflect.String {
				for _, candidate := range typeURLCandidates(typeURL) {
					if item.String() == candidate {
						return candidate, true
					}
				}
			}
		}
	case reflect.Func:
		functionType := value.Type()
		if functionType.NumIn() != 1 ||
			functionType.In(0).Kind() != reflect.String ||
			(functionType.NumOut() < 1 || functionType.NumOut() > 2) ||
			functionType.Out(0).Kind() != reflect.String {
			return "", false
		}
		if functionType.NumOut() == 2 && functionType.Out(1).Kind() != reflect.Bool {
			return "", false
		}
		results := value.Call([]reflect.Value{reflect.ValueOf(typeURL)})
		if len(results) >= 1 && results[0].Kind() == reflect.String {
			name := results[0].String()
			if len(results) >= 2 && results[1].Kind() == reflect.Bool && !results[1].Bool() {
				return "", false
			}
			return name, strings.TrimSpace(name) != ""
		}
	}
	return "", false
}
