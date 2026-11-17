package netsec

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
)

// Security defaults restored when a bound field is deleted.
const (
	defaultServiceAccount      = "default"
	defaultRunAsNonRoot        = true
	defaultReadOnlyRootFS      = true
	defaultAllowPrivEscalation = false
	defaultPrivileged          = false
)

// legacyServiceAccountPath is the deprecated alias for the identity field.
const legacyServiceAccountPath = "/spec/serviceAccount"

// NormalizeTemplate produces the canonical snapshot that an approval binds:
// the legacy service-account alias is migrated, the image is parsed and
// digest-pinned, defaults are materialized and every ordered collection is
// sorted and de-duplicated.
func NormalizeTemplate(t WorkloadTemplate) (NormalizedTemplate, error) {
	clone := cloneTemplate(t)

	raw, err := toMap(clone)
	if err != nil {
		return NormalizedTemplate{}, err
	}

	if err := migrateLegacyIdentity(raw); err != nil {
		return NormalizedTemplate{}, err
	}

	if err := normalizeSecurity(raw); err != nil {
		return NormalizedTemplate{}, err
	}

	if err := normalizeCollections(raw); err != nil {
		return NormalizedTemplate{}, err
	}

	var canonical WorkloadTemplate
	if err := fromMap(raw, &canonical); err != nil {
		return NormalizedTemplate{}, err
	}

	image, err := ParseImage(canonical.Spec.Image)
	if err != nil {
		return NormalizedTemplate{}, err
	}

	canonicalRaw, err := toMap(canonical)
	if err != nil {
		return NormalizedTemplate{}, err
	}
	sum, err := canonicalJSON(canonicalRaw)
	if err != nil {
		return NormalizedTemplate{}, err
	}
	fingerprint := "sha256:" + hex.EncodeToString(sum)

	return NormalizedTemplate{
		Template:    canonical,
		Raw:         canonicalRaw,
		Image:       image,
		Fingerprint: fingerprint,
	}, nil
}

// migrateLegacyIdentity moves the deprecated /spec/serviceAccount alias into
// /spec/identity/serviceAccountName. A conflicting pair is rejected rather
// than silently choosing one identity.
func migrateLegacyIdentity(root map[string]any) error {
	spec, ok := root["spec"].(map[string]any)
	if !ok {
		return errors.Join(errApproval, errors.New("template has no spec object"))
	}
	alias, hasAlias := spec["serviceAccount"]
	if !hasAlias {
		return nil
	}
	aliasName, ok := alias.(string)
	if !ok {
		return errors.Join(errApproval, errors.New("spec.serviceAccount must be a string"))
	}

	identity, _ := spec["identity"].(map[string]any)
	if identity == nil {
		identity = map[string]any{}
		spec["identity"] = identity
	}
	if current, _ := identity["serviceAccountName"].(string); current != "" && current != aliasName {
		return errors.Join(errAliasConflict, errors.New("alias="+aliasName+" identity="+current))
	}
	identity["serviceAccountName"] = aliasName
	delete(spec, "serviceAccount")
	return nil
}

// normalizeSecurity materializes every security default so that a later
// "remove" operation restores an explicit, reviewable value.
func normalizeSecurity(root map[string]any) error {
	spec := root["spec"].(map[string]any)

	identity, _ := spec["identity"].(map[string]any)
	if identity == nil {
		identity = map[string]any{}
		spec["identity"] = identity
	}
	name, present := identity["serviceAccountName"]
	if !present || name == nil {
		identity["serviceAccountName"] = defaultServiceAccount
	} else if text, ok := name.(string); ok && text == "" {
		identity["serviceAccountName"] = defaultServiceAccount
	}

	security, _ := spec["security"].(map[string]any)
	if security == nil {
		security = map[string]any{}
		spec["security"] = security
	}
	setBoolDefault := func(key string, def bool) {
		if _, ok := security[key]; !ok || security[key] == nil {
			security[key] = def
		}
	}
	setBoolDefault("runAsNonRoot", defaultRunAsNonRoot)
	setBoolDefault("readOnlyRootFS", defaultReadOnlyRootFS)
	setBoolDefault("allowPrivilegeEscalation", defaultAllowPrivEscalation)
	setBoolDefault("privileged", defaultPrivileged)
	return nil
}

// normalizeCollections makes ordering canonical so equivalent templates have
// identical fingerprints.
func normalizeCollections(root map[string]any) error {
	spec := root["spec"].(map[string]any)

	if identity, ok := spec["identity"].(map[string]any); ok {
		identity["groupNames"] = normalizeStringSlice(identity["groupNames"])
		if len(identity["groupNames"].([]string)) == 0 {
			delete(identity, "groupNames")
		}
	}

	if rawEnv, ok := spec["env"]; ok {
		entries, err := toStringSliceMaps(rawEnv)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i]["name"].(string) < entries[j]["name"].(string)
		})
		spec["env"] = entries
	}

	if caps, ok := spec["capabilities"]; ok {
		normalized := normalizeStringSlice(caps)
		if len(normalized) == 0 {
			delete(spec, "capabilities")
		} else {
			spec["capabilities"] = normalized
		}
	}
	return nil
}

func normalizeStringSlice(v any) []string {
	items, _ := v.([]any)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func toStringSliceMaps(v any) ([]map[string]any, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, errors.Join(errApproval, errors.New("expected an array"))
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errors.Join(errApproval, errors.New("env entries must be objects"))
		}
		out = append(out, m)
	}
	return out, nil
}

func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errors.Join(errApproval, err)
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&out); err != nil {
		return nil, errors.Join(errApproval, err)
	}
	return out, nil
}

func fromMap(m map[string]any, target any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return errors.Join(errApproval, err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return errors.Join(errApproval, err)
	}
	return nil
}

func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, errors.Join(errApproval, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return nil, errors.Join(errApproval, err)
	}
	return compact.Bytes(), nil
}

func cloneTemplate(t WorkloadTemplate) WorkloadTemplate {
	return deepClone(t)
}

func deepClone[T any](v T) T {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func deepEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}
