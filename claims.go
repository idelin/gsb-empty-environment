package netsec

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// CWT claim keys used by Arm CCA attestation tokens.
const (
	ClaimKeyNonce                       uint64 = 10
	ClaimKeyPlatformHashAlgorithm       uint64 = 2402
	ClaimKeyRealmHashAlgorithm          uint64 = 44236
	ClaimKeyRealmPublicKey              uint64 = 44237
	ClaimKeyRealmInitialMeasurement     uint64 = 44238
	ClaimKeyRealmExtensibleMeasurements uint64 = 44239
	ClaimKeyRealmPublicKeyHashAlgorithm uint64 = 44240
	ClaimKeyRealmDelegatedToken         uint64 = 44241
	ClaimKeyPlatformToken               uint64 = 44234
	ClaimKeyCCATokenWrapper             uint64 = 399
)

const (
	claimNonce                       = "nonce"
	claimHashAlgorithm               = "hash-algorithm"
	claimRealmHashAlgorithm          = "realm-hash-algorithm"
	claimPlatformHashAlgorithm       = "platform-hash-algorithm"
	claimRealmPublicKey              = "realm-public-key"
	claimPublicKey                   = "public-key"
	claimRealmPublicKeyHashAlgorithm = "realm-public-key-hash-algorithm"
	claimPublicKeyHashAlgorithm      = "public-key-hash-algorithm"
	claimRealmInitialMeasurement     = "realm-initial-measurement"
	claimInitialMeasurement          = "initial-measurement"
	claimRealmExtensibleMeasurements = "realm-extensible-measurements"
	claimExtensibleMeasurements      = "extensible-measurements"
	claimWrapperPlatformToken        = "wrapper-platform-token"
	claimWrapperRealmToken           = "wrapper-realm-token"
)

var numericClaimNames = map[uint64]string{
	ClaimKeyNonce:                       claimNonce,
	239:                                 "cca-platform-sw-components",
	ClaimKeyPlatformHashAlgorithm:       claimPlatformHashAlgorithm,
	ClaimKeyRealmHashAlgorithm:          claimRealmHashAlgorithm,
	ClaimKeyRealmPublicKey:              claimRealmPublicKey,
	ClaimKeyRealmInitialMeasurement:     claimRealmInitialMeasurement,
	ClaimKeyRealmExtensibleMeasurements: claimRealmExtensibleMeasurements,
	ClaimKeyRealmPublicKeyHashAlgorithm: claimRealmPublicKeyHashAlgorithm,
	ClaimKeyRealmDelegatedToken:         claimWrapperRealmToken,
	ClaimKeyPlatformToken:               claimWrapperPlatformToken,
}

// Claims is a decoded claim map keyed by the textual CCA/EAT claim names.
// Numeric CWT claim keys are accepted as input and normalised internally.
type Claims map[string]any

type claimSet struct {
	values map[string]any
}

func newClaimSet(m map[any]any) (*claimSet, error) {
	cs := &claimSet{values: make(map[string]any, len(m))}
	for k, v := range m {
		name, ok := canonicalClaimKey(k)
		if !ok {
			name = fmt.Sprintf("unknown:%T:%v", k, k)
		}
		if _, exists := cs.values[name]; exists {
			return nil, fmt.Errorf("%w: duplicate or ambiguous claim %q", ErrMalformedToken, name)
		}
		cs.values[name] = v
	}
	return cs, nil
}

func canonicalClaimKey(k any) (string, bool) {
	switch x := k.(type) {
	case string:
		key := strings.ToLower(strings.TrimSpace(x))
		if numericString(key) {
			if n, err := strconv.ParseUint(key, 10, 64); err == nil {
				if name, ok := numericClaimNames[n]; ok {
					return name, true
				}
			}
		}
		switch key {
		case "eat_nonce", "nonce", "challenge",
			"cca-realm-challenge", "cca-platform-challenge",
			"realm-challenge", "platform-challenge",
			"realmchallenge", "platformchallenge",
			"realm-nonce", "platform-nonce":
			return claimNonce, true
		case "cca-realm-hash-algo-id", "cca-realm-hash-algm-id", "cca-realm-hash-alg-id",
			"cca-realm-hash-algorithm", "cca-realm-hash-alg", "cca-realm-hash-algo",
			"realm-hash-algo-id", "realm-hash-algorithm":
			return claimRealmHashAlgorithm, true
		case "hash-algo-id", "hash-alg-id", "hash-algorithm", "hash-alg",
			"hashalg", "hashalgid", "hashalgorithmid":
			return claimHashAlgorithm, true
		case "hashalgorithm":
			return claimHashAlgorithm, true
		case "cca-platform-hash-algo-id", "cca-platform-hash-algm-id", "arm-platform-hash-algo-id",
			"arm-platform-hash-algm-id", "platform-hash-algo-id", "platform-hash-algorithm",
			"cca-platform-hash-alg", "cca-platform-hash-algo":
			return claimPlatformHashAlgorithm, true
		case "cca-realm-public-key", "realm-public-key", "public-key", "publickey", "realmpublickey":
			return claimRealmPublicKey, true
		case "cca-realm-public-key-hash-algo-id", "cca-realm-public-key-hash-algm-id",
			"cca-realm-public-key-hash-alg-id", "realm-public-key-hash-algo-id",
			"realm-public-key-hash-algorithm", "publickeyhashalgid", "publickeyhashalgorithm",
			"realmpublickeyhashalgorithm", "cca-realm-public-key-hash-alg",
			"publickeyhashalg", "realmpublickeyhashalgid", "realmpublickeyhashalgorithmid",
			"realm-public-key-hash-alg":
			return claimRealmPublicKeyHashAlgorithm, true
		case "public-key-hash-algo-id", "public-key-hash-algorithm":
			return claimPublicKeyHashAlgorithm, true
		case "cca-realm-initial-measurement", "realm-initial-measurement", "initialmeasurement",
			"realminitialmeasurement":
			return claimRealmInitialMeasurement, true
		case "initial-measurement":
			return claimInitialMeasurement, true
		case "cca-realm-extensible-measurements", "realm-extensible-measurements", "extensiblemeasurements",
			"realextensiblemeasurements":
			return claimRealmExtensibleMeasurements, true
		case "extensible-measurements":
			return claimExtensibleMeasurements, true
		case "cca-platform-token", "platform-token", "cca-platform":
			return claimWrapperPlatformToken, true
		case "cca-realm-delegated-token", "cca-realm-token", "realm-token", "realm-delegated-token":
			return claimWrapperRealmToken, true
		case "implementationid", "cca-platform-implementationid", "cca-platform-implementation-id":
			return "cca-platform-implementation-id", true
		case "instanceid", "cca-platform-instanceid", "cca-platform-instance-id":
			return "cca-platform-instance-id", true
		case "cca-platform-config", "cca-platform-configuration":
			return "cca-platform-config", true
		case "cca-platform-lifecycle", "cca-platform-lifecycle-id":
			return "cca-platform-lifecycle", true
		case "cca-platform-profile", "cca-platform-profile-id":
			return "cca-platform-profile", true
		case "softwarecomponents", "cca-platform-softwarecomponents", "cca-platform-sw-components",
			"cca-platform-software-components":
			return "cca-platform-sw-components", true
		default:
			return key, true
		}
	case int:
		return canonicalNumericClaimKey(uint64(x))
	case int8:
		return canonicalNumericClaimKey(uint64(x))
	case int16:
		return canonicalNumericClaimKey(uint64(x))
	case int32:
		return canonicalNumericClaimKey(uint64(x))
	case int64:
		if x < 0 {
			return "", false
		}
		return canonicalNumericClaimKey(uint64(x))
	case uint:
		return canonicalNumericClaimKey(uint64(x))
	case uint8:
		return canonicalNumericClaimKey(uint64(x))
	case uint16:
		return canonicalNumericClaimKey(uint64(x))
	case uint32:
		return canonicalNumericClaimKey(uint64(x))
	case uint64:
		return canonicalNumericClaimKey(x)
	case float64:
		if x == float64(uint64(x)) && x >= 0 {
			return canonicalNumericClaimKey(uint64(x))
		}
	case json.Number:
		if n, err := strconv.ParseUint(string(x), 10, 64); err == nil {
			return canonicalNumericClaimKey(n)
		}
	}
	return "", false
}

func canonicalNumericClaimKey(n uint64) (string, bool) {
	if name, ok := numericClaimNames[n]; ok {
		return name, true
	}
	return fmt.Sprintf("unknown:uint64:%d", n), true
}

func (cs *claimSet) get(names ...string) (any, bool) {
	for _, name := range names {
		if v, ok := cs.values[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (cs *claimSet) has(name string) bool {
	_, ok := cs.values[name]
	return ok
}

func (cs *claimSet) realmHashAlgorithmValue() (any, bool) {
	return cs.get(claimRealmHashAlgorithm, claimHashAlgorithm)
}

func (cs *claimSet) publicKeyHashAlgorithmValue() (any, bool) {
	return cs.get(claimRealmPublicKeyHashAlgorithm, claimPublicKeyHashAlgorithm,
		claimHashAlgorithm, claimRealmHashAlgorithm)
}

func (cs *claimSet) platformHashAlgorithmValue() (any, bool) {
	return cs.get(claimPlatformHashAlgorithm, claimHashAlgorithm)
}

func (cs *claimSet) realmPublicKeyValue() (any, bool) {
	return cs.get(claimRealmPublicKey, claimPublicKey)
}

func (cs *claimSet) initialMeasurementValue() (any, bool) {
	return cs.get(claimRealmInitialMeasurement, claimInitialMeasurement)
}

func (cs *claimSet) extensibleMeasurementsValue() (any, bool) {
	return cs.get(claimRealmExtensibleMeasurements, claimExtensibleMeasurements)
}

func (cs *claimSet) realmScore() int {
	score := 0
	if cs.has(claimRealmPublicKey) || cs.has(claimPublicKey) {
		score += 4
	}
	if cs.has(claimRealmHashAlgorithm) || cs.has(claimHashAlgorithm) {
		score += 2
	}
	if cs.has(claimRealmPublicKeyHashAlgorithm) || cs.has(claimPublicKeyHashAlgorithm) {
		score += 2
	}
	if cs.has(claimRealmInitialMeasurement) || cs.has(claimInitialMeasurement) {
		score++
	}
	if cs.has(claimRealmExtensibleMeasurements) || cs.has(claimExtensibleMeasurements) {
		score++
	}
	return score
}

func (cs *claimSet) platformScore() int {
	score := 0
	if cs.has(claimPlatformHashAlgorithm) {
		score += 2
	}
	for _, name := range []string{
		"cca-platform-implementation-id",
		"cca-platform-instance-id",
		"cca-platform-lifecycle",
		"cca-platform-config",
		"cca-platform-sw-components",
		"cca-platform-profile",
	} {
		if cs.has(name) {
			score++
		}
	}
	return score
}

func claimsFromAny(v any) (map[any]any, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil token", ErrInvalidArgument)
	}
	switch x := v.(type) {
	case Claims:
		return anyToAnyMap(x), nil
	case map[string]any:
		return anyToAnyMap(x), nil
	case map[any]any:
		out := make(map[any]any, len(x))
		for k, value := range x {
			out[k] = value
		}
		return out, nil
	case json.RawMessage:
		return decodeJSONClaims(x)
	}

	rv := reflect.ValueOf(v)
	return claimsFromReflect(rv)
}

func anyToAnyMap(in map[string]any) map[any]any {
	out := make(map[any]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func claimsFromReflect(rv reflect.Value) (map[any]any, error) {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, fmt.Errorf("%w: nil token", ErrInvalidArgument)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct && rv.Kind() != reflect.Map {
		return nil, fmt.Errorf("%w: unsupported token value %s", ErrInvalidArgument, rv.Type())
	}

	switch rv.Kind() {
	case reflect.Map:
		out := make(map[any]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k, ok := reflectValueToAny(iter.Key())
			if !ok {
				return nil, fmt.Errorf("%w: unsupported map key %s", ErrInvalidArgument, iter.Key().Type())
			}
			value, ok := reflectValueToAny(iter.Value())
			if !ok {
				out[k] = nil
				continue
			}
			out[k] = value
		}
		return out, nil
	case reflect.Struct:
		return claimsFromStruct(rv)
	default:
		return nil, fmt.Errorf("%w: unsupported token value %s", ErrInvalidArgument, rv.Type())
	}
}

func claimsFromStruct(rv reflect.Value) (map[any]any, error) {
	rt := rv.Type()
	out := make(map[any]any)

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		fieldValue := rv.Field(i)

		if field.Anonymous {
			anon := fieldValue
			for anon.Kind() == reflect.Pointer || anon.Kind() == reflect.Interface {
				if anon.IsNil() {
					break
				}
				anon = anon.Elem()
			}
			if anon.IsValid() && anon.Kind() == reflect.Struct {
				anonClaims, err := claimsFromStruct(anon)
				if err != nil {
					return nil, err
				}
				for k, v := range anonClaims {
					out[k] = v
				}
				continue
			}
		}

		key, ok := fieldClaimKey(field)
		if !ok {
			continue
		}
		value, ok := reflectValueToAny(fieldValue)
		if !ok {
			continue
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("%w: duplicate struct claim %v", ErrMalformedToken, key)
		}
		out[key] = value
	}
	return out, nil
}

func fieldClaimKey(field reflect.StructField) (any, bool) {
	for _, tagName := range []string{"cbor", "json"} {
		tag := field.Tag.Get(tagName)
		if tag == "" {
			continue
		}
		part := strings.Split(tag, ",")[0]
		if part == "-" {
			return nil, false
		}
		if part != "" {
			return part, true
		}
	}
	if field.Name == "" {
		return nil, false
	}
	switch strings.ToLower(field.Name) {
	case "platformclaims", "platformtoken", "platform":
		return claimWrapperPlatformToken, true
	case "realmclaims", "realmtoken", "realm", "realmdelegatedtoken":
		return claimWrapperRealmToken, true
	}
	return field.Name, true
}

func reflectValueToAny(rv reflect.Value) (any, bool) {
	if !rv.IsValid() {
		return nil, false
	}
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}

	if rv.CanInterface() {
		if b, ok := rv.Interface().(interface{ Bytes() []byte }); ok {
			return append([]byte(nil), b.Bytes()...), true
		}
		if b, ok := rv.Interface().(interface{ GetBytes() []byte }); ok {
			return append([]byte(nil), b.GetBytes()...), true
		}
	}

	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.String:
		return rv.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint(), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				b[i] = byte(rv.Index(i).Uint())
			}
			return b, true
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			value, ok := reflectValueToAny(rv.Index(i))
			if !ok {
				value = nil
			}
			out = append(out, value)
		}
		return out, true
	case reflect.Map:
		out := make(map[any]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k, ok := reflectValueToAny(iter.Key())
			if !ok {
				continue
			}
			value, ok := reflectValueToAny(iter.Value())
			if !ok {
				value = nil
			}
			out[k] = value
		}
		return out, true
	case reflect.Struct:
		// A few attestation libraries wrap a byte slice in a one-field
		// struct (for example an EAT Nonce). Treat that as its bytes.
		if rv.NumField() == 1 {
			f := rv.Field(0)
			if f.CanInterface() && f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Uint8 {
				switch strings.ToLower(rv.Type().Field(0).Name) {
				case "value", "bytes", "data", "raw":
					return append([]byte(nil), f.Bytes()...), true
				}
			}
		}
		nested, err := claimsFromStruct(rv)
		if err != nil {
			return nil, false
		}
		return nested, true
	default:
		return nil, false
	}
}

func decodeJSONClaims(data []byte) (map[any]any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON claims: %v", ErrMalformedToken, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data after JSON claims", ErrMalformedToken)
	}
	v = normaliseJSONValue(v)
	m, ok := v.(map[any]any)
	if !ok {
		return nil, fmt.Errorf("%w: JSON token is not an object", ErrMalformedToken)
	}
	return m, nil
}

func normaliseJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[any]any, len(x))
		for k, value := range x {
			out[k] = normaliseJSONValue(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = normaliseJSONValue(value)
		}
		return out
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n
		}
		if n, err := strconv.ParseUint(string(x), 10, 64); err == nil {
			return n
		}
		return x
	default:
		return v
	}
}
