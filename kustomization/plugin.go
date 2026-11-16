package kustomization

import "strings"

const execAnnotation = "config.kubernetes.io/function"

// asMap normalizes the YAML map shapes produced by common decoders.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			if key, ok := k.(string); ok {
				out[key] = val
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// asString accepts plain strings and also rejects typed non-string entries.
func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// nestedString looks up key under the "metadata" / "annotations" mapping.
func nestedString(m map[string]any, key string) (string, bool) {
	meta, ok := asMap(m["metadata"])
	if !ok {
		return "", false
	}
	annotations, ok := asMap(meta["annotations"])
	if !ok {
		return "", false
	}
	value, ok := annotations[key].(string)
	return value, ok
}

// isExecEntry rejects inline plugin entries that can execute external
// programs: an explicit "exec" field, an exec KRM function annotation, or an
// apiVersion/kind that names an exec plugin.
func isExecEntry(entry map[string]any) (bool, string) {
	if _, ok := entry["exec"]; ok {
		return true, "entry contains an exec field"
	}
	if value, ok := nestedString(entry, execAnnotation); ok && strings.Contains(value, "exec") {
		return true, "entry declares an exec KRM function"
	}
	apiVersion, _ := entry["apiVersion"].(string)
	kind, _ := entry["kind"].(string)
	if strings.ToLower(apiVersion) == "exec" || strings.ToLower(kind) == "exec" {
		return true, "entry is an exec plugin"
	}
	return false, ""
}

// pathOf extracts the referenced path from an inline object, returning ok
// false when the object has no path field.
func pathOf(entry map[string]any) (string, bool) {
	raw, ok := entry["path"]
	if !ok {
		return "", false
	}
	p, ok := raw.(string)
	if !ok || strings.TrimSpace(p) == "" {
		return "", false
	}
	return p, true
}

// checkReference applies the local or remote policy to one reference string.
func checkReference(ref, field, baseDir string, idx *fileIndex, hosts map[string]bool) []Violation {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return []Violation{{Field: field, Value: ref, Reason: "empty reference"}}
	}
	if isRemoteReference(ref) {
		return checkRemote(ref, field, hosts)
	}
	if _, violations := idx.resolveLocal(baseDir, ref, field); violations != nil {
		return violations
	}
	return nil
}

// checkReferenceEntries walks a polymorphic entry list of the kind used by
// resources, bases, patches, generators and transformers. Inline objects are
// first screened as possible exec plugins and otherwise followed via "path".
func checkReferenceEntries(entries []any, field, baseDir string, idx *fileIndex, hosts map[string]bool, execCheck bool) []Violation {
	var violations []Violation
	for _, raw := range entries {
		switch entry := raw.(type) {
		case string:
			violations = append(violations, checkReference(entry, field, baseDir, idx, hosts)...)
		case map[string]any, map[any]any:
			m, _ := asMap(entry)
			if execCheck {
				if bad, reason := isExecEntry(m); bad {
					violations = append(violations, Violation{Field: field, Reason: reason})
					continue
				}
			}
			if p, ok := pathOf(m); ok {
				violations = append(violations, checkReference(p, field, baseDir, idx, hosts)...)
			}
		default:
			violations = append(violations, Violation{Field: field, Reason: "unsupported entry type"})
		}
	}
	return violations
}
