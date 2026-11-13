package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type envVar struct {
	key   string
	value string
	full  string
}

type mountValue struct {
	source      string
	destination string
	mountType   string
	options     []string
	raw         map[string]any
}

func (m mountValue) values() []string {
	out := make([]string, 0, 3+len(m.options))
	if m.source != "" {
		out = append(out, m.source)
	}
	if m.destination != "" {
		out = append(out, m.destination)
	}
	if m.mountType != "" {
		out = append(out, m.mountType)
	}
	out = append(out, m.options...)
	return out
}

type deviceValue struct {
	path       string
	deviceType string
	source     string
	raw        map[string]any
}

func (d deviceValue) values() []string {
	out := make([]string, 0, 3)
	if d.path != "" {
		out = append(out, d.path)
	}
	if d.deviceType != "" {
		out = append(out, d.deviceType)
	}
	if d.source != "" {
		out = append(out, d.source)
	}
	return out
}

type pathClaim struct {
	field string
	value string
}

type normalizedPayload struct {
	raw            map[string]any
	containerID    string
	root           string
	args           []string
	env            []envVar
	mounts         []mountValue
	devices        []deviceValue
	privileged     bool
	privilegedSeen bool
	capabilities   []string
	paths          []pathClaim
}

func normalizePayload(payload any) (*normalizedPayload, error) {
	raw, err := payloadObject(payload)
	if err != nil {
		return nil, err
	}

	p := &normalizedPayload{raw: raw}
	if value, ok := lookup(raw, "container_id", "containerId", "container.id", "request.container_id", "id"); ok {
		p.containerID, _ = asString(value)
	}
	if value, ok := lookup(raw, "root", "root_path", "rootPath", "container_root", "containerRoot", "rootfs", "rootfs_path"); ok {
		p.root, _ = asString(value)
	}
	if value, ok := lookup(raw, "args", "arg", "arguments", "command", "cmd", "process.args", "spec.process.args", "runtime_spec.process.args", "runtimespec.process.args", "oci.process.args"); ok {
		p.args = stringsOf(value)
	}
	if value, ok := lookup(raw, "env", "environment", "process.env", "process.environment", "spec.process.env", "runtime_spec.process.env", "runtimespec.process.env", "oci.process.env"); ok {
		p.env = envOf(value)
	}
	if value, ok := lookup(raw, "mounts", "rootfs.mounts", "spec.mounts", "runtime_spec.mounts", "runtimespec.mounts", "oci.mounts"); ok {
		p.mounts = mountsOf(value)
	}
	if value, ok := lookup(raw, "privileged", "process.privileged", "spec.process.privileged", "runtime_spec.process.privileged", "runtimespec.process.privileged", "oci.process.privileged"); ok {
		privileged, valid := boolOf(value)
		if !valid {
			return nil, errors.New("privileged must be a boolean")
		}
		p.privileged, p.privilegedSeen = privileged, true
	}
	if value, ok := lookup(raw, "capabilities", "caps", "process.capabilities", "process.caps", "linux.capabilities", "spec.process.capabilities", "runtime_spec.process.capabilities", "runtimespec.process.capabilities", "oci.process.capabilities"); ok {
		p.capabilities = capabilitiesOf(value)
	}
	if value, ok := lookup(raw, "devices", "linux.devices", "spec.linux.devices", "runtime_spec.linux.devices", "runtimespec.linux.devices", "resources.devices", "oci.linux.devices"); ok {
		p.devices = devicesOf(value)
	}

	p.paths = pathClaims(raw)
	return p, nil
}

func payloadObject(payload any) (map[string]any, error) {
	if payload == nil {
		return map[string]any{}, nil
	}

	switch value := payload.(type) {
	case map[string]any:
		return cloneObject(value), nil
	case map[string]string:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[k] = v
		}
		return out, nil
	case json.RawMessage:
		return decodeJSONObject(value)
	case []byte:
		return decodeJSONObject(value)
	case string:
		trimmed := strings.TrimSpace(value)
		if strings.HasPrefix(trimmed, "{") {
			return decodeJSONObject([]byte(value))
		}
		return map[string]any{"path": value}, nil
	case Request:
		return payloadObject(value.Payload)
	case *Request:
		if value == nil {
			return map[string]any{}, nil
		}
		return payloadObject(value.Payload)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return map[string]any{}, nil
	}
	return decodeJSONObject(encoded)
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("payload must be a JSON object")
	}
	return object, nil
}

func cloneObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneObject(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	default:
		return value
	}
}

func lookup(object map[string]any, aliases ...string) (any, bool) {
	for _, alias := range aliases {
		path := strings.Split(alias, ".")
		if value, ok := lookupPath(object, path); ok {
			return value, true
		}
	}
	for _, alias := range aliases {
		path := strings.Split(alias, ".")
		if len(path) > 1 {
			for _, wrapper := range []string{"spec", "runtime_spec", "runtimespec", "oci"} {
				if nestedValue, ok := objectValue(object, wrapper); ok {
					nested, ok := nestedValue.(map[string]any)
					if !ok {
						continue
					}
					if value, ok := lookupPath(nested, path); ok {
						return value, true
					}
				}
			}
		}
	}
	return nil, false
}

func lookupPath(object map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	if len(path) == 1 {
		return objectValue(object, path[0])
	}
	first, ok := objectValue(object, path[0])
	if !ok {
		return nil, false
	}
	next, ok := first.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookupPath(next, path[1:])
}

func objectValue(object map[string]any, name string) (any, bool) {
	normalized := normalizeName(name)
	for key, value := range object {
		if normalizeName(key) == normalized {
			return value, true
		}
	}
	return nil, false
}

func asString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	default:
		return "", false
	}
}

func stringsOf(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := asString(item); ok {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		if nested, ok := objectValue(v, "args"); ok {
			return stringsOf(nested)
		}
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		out := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			if s, ok := asString(rv.Index(i).Interface()); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func envOf(value any) []envVar {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		out := make([]envVar, 0, len(v))
		for _, pair := range v {
			out = append(out, parseEnvPair(pair))
		}
		return out
	case []any:
		out := make([]envVar, 0, len(v))
		for _, item := range v {
			switch pair := item.(type) {
			case string:
				out = append(out, parseEnvPair(pair))
			case map[string]any:
				key, _ := asString(firstValue(pair, "key", "name"))
				val, _ := asString(firstValue(pair, "value", "val"))
				out = append(out, makeEnv(key, val))
			}
		}
		return out
	case map[string]string:
		out := make([]envVar, 0, len(v))
		for key, val := range v {
			out = append(out, makeEnv(key, val))
		}
		return out
	case map[string]any:
		out := make([]envVar, 0, len(v))
		for key, item := range v {
			if val, ok := asString(item); ok {
				out = append(out, makeEnv(key, val))
				continue
			}
			out = append(out, envOf(item)...)
		}
		return out
	case string:
		return []envVar{parseEnvPair(v)}
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		out := make([]envVar, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, envOf(rv.Index(i).Interface())...)
		}
		return out
	}
	return nil
}

func parseEnvPair(pair string) envVar {
	key, value, found := strings.Cut(pair, "=")
	if !found {
		return makeEnv(pair, "")
	}
	return makeEnv(key, value)
}

func makeEnv(key, value string) envVar {
	return envVar{key: key, value: value, full: key + "=" + value}
}

func firstValue(object map[string]any, names ...string) any {
	for _, name := range names {
		if value, ok := objectValue(object, name); ok {
			return value
		}
	}
	return nil
}

func anyStringMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mountsOf(value any) []mountValue {
	switch v := value.(type) {
	case nil:
		return nil
	case []Mount:
		out := make([]mountValue, 0, len(v))
		for _, mount := range v {
			out = append(out, mountValueFrom(mount.Source, mount.Destination, mount.Type, mount.Options, nil))
		}
		return out
	case Mount:
		return []mountValue{mountValueFrom(v.Source, v.Destination, v.Type, v.Options, nil)}
	case []any:
		out := make([]mountValue, 0, len(v))
		for _, item := range v {
			out = append(out, mountsOf(item)...)
		}
		return out
	case map[string]any:
		if nested := firstValue(v, "mounts"); nested != nil {
			return mountsOf(nested)
		}
		source, _ := asString(firstValue(v, "source", "src"))
		destination, _ := asString(firstValue(v, "destination", "target", "dst"))
		mountType, _ := asString(firstValue(v, "type"))
		options := stringsOf(firstValue(v, "options", "opts"))
		return []mountValue{mountValueFrom(source, destination, mountType, options, v)}
	case map[string]string:
		return mountsOf(anyStringMap(v))
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		out := make([]mountValue, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, mountsOf(rv.Index(i).Interface())...)
		}
		return out
	}
	return nil
}

func mountValueFrom(source, destination, mountType string, options []string, raw map[string]any) mountValue {
	return mountValue{
		source:      source,
		destination: destination,
		mountType:   mountType,
		options:     options,
		raw:         raw,
	}
}

func devicesOf(value any) []deviceValue {
	switch v := value.(type) {
	case nil:
		return nil
	case []Device:
		out := make([]deviceValue, 0, len(v))
		for _, device := range v {
			out = append(out, deviceValueFrom(device.Path, device.Type, device.Source, nil))
		}
		return out
	case Device:
		return []deviceValue{deviceValueFrom(v.Path, v.Type, v.Source, nil)}
	case []any:
		out := make([]deviceValue, 0, len(v))
		for _, item := range v {
			out = append(out, devicesOf(item)...)
		}
		return out
	case map[string]any:
		if nested := firstValue(v, "devices"); nested != nil {
			return devicesOf(nested)
		}
		path, _ := asString(firstValue(v, "path", "device", "name"))
		deviceType, _ := asString(firstValue(v, "type"))
		source, _ := asString(firstValue(v, "source", "src"))
		return []deviceValue{deviceValueFrom(path, deviceType, source, v)}
	case map[string]string:
		return devicesOf(anyStringMap(v))
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		out := make([]deviceValue, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, devicesOf(rv.Index(i).Interface())...)
		}
		return out
	}
	return nil
}

func deviceValueFrom(path, deviceType, source string, raw map[string]any) deviceValue {
	return deviceValue{path: path, deviceType: deviceType, source: source, raw: raw}
}

func boolOf(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return parsed, true
		}
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "yes", "on":
			return true, true
		case "no", "off":
			return false, true
		}
	case json.Number:
		number, err := v.Int64()
		if err == nil {
			return number != 0, true
		}
	case float64:
		return v != 0, true
	case float32:
		return v != 0, true
	case int:
		return v != 0, true
	case int64:
		return v != 0, true
	case int32:
		return v != 0, true
	case uint64:
		return v != 0, true
	case uint32:
		return v != 0, true
	}
	return false, false
}

func capabilitiesOf(value any) []string {
	var out []string
	var add func(any)
	add = func(item any) {
		switch v := item.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, v)
			}
		case []string:
			for _, s := range v {
				add(s)
			}
		case []any:
			for _, s := range v {
				add(s)
			}
		case map[string]any:
			for key, nested := range v {
				switch normalizeName(key) {
				case "add", "bounding", "effective", "permitted", "inheritable", "ambient":
					add(nested)
				default:
					if enabled, ok := boolOf(nested); ok && enabled {
						add(key)
					}
				}
			}
		case map[string]string:
			add(anyStringMap(v))
		}
	}
	add(value)
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func pathClaims(raw map[string]any) []pathClaim {
	var claims []pathClaim
	add := func(field string, aliases ...string) {
		for _, alias := range aliases {
			value, ok := lookup(raw, alias)
			if !ok {
				continue
			}
			if path, ok := asString(value); ok && path != "" {
				claims = append(claims, pathClaim{field: field, value: path})
			}
			return
		}
	}
	add("path", "path", "file_path", "filename", "file", "copy.path", "copyfile.path")
	add("destination", "destination", "destination_path", "dest", "target", "to", "dst", "copy.destination", "copyfile.destination")
	if len(claims) == 0 {
		add("source", "source", "src", "from", "copy.source", "copyfile.source")
	}
	return claims
}

func scanObjectKeys(value any, forbidden []string, depth int) bool {
	if depth > 32 || len(forbidden) == 0 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for key, nested := range v {
			if matchesAnyKey(forbidden, key) {
				return true
			}
			if scanObjectKeys(nested, forbidden, depth+1) {
				return true
			}
		}
	case []any:
		for _, nested := range v {
			if scanObjectKeys(nested, forbidden, depth+1) {
				return true
			}
		}
	}
	return false
}

func matchesAnyKey(patterns []string, key string) bool {
	for _, pattern := range patterns {
		if keyMatches(pattern, key) || keyMatches(pattern, normalizeName(key)) {
			return true
		}
	}
	return false
}
