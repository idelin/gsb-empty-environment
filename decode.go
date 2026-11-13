package netsec

import (
	"encoding/json"
	"fmt"
)

// DecodeKustomization converts a generic mapping produced by a YAML/JSON
// decoder (for example yaml.Unmarshal into map[string]any) into the typed
// Kustomization view. Unknown fields are ignored and type mismatches produce
// an error rather than silently dropping security-relevant entries.
//
// Generators and transformers accept both string entries (plugin config file
// paths) and inline mappings. Inline mappings are retained as PluginEntry.Spec
// with their JSON encoding in RawSpec so exec declarations remain visible.
func DecodeKustomization(raw map[string]any) (*Kustomization, error) {
	if raw == nil {
		return nil, fmt.Errorf("kustomization mapping is nil")
	}
	kustomization := &Kustomization{}
	var err error

	if kustomization.Resources, err = decodeStringList(raw, "resources"); err != nil {
		return nil, err
	}
	if kustomization.Bases, err = decodeStringList(raw, "bases"); err != nil {
		return nil, err
	}
	if kustomization.Patches, err = decodePatches(raw["patches"]); err != nil {
		return nil, err
	}
	if kustomization.Generators, err = decodePluginEntries(raw["generators"]); err != nil {
		return nil, err
	}
	if kustomization.Transformers, err = decodePluginEntries(raw["transformers"]); err != nil {
		return nil, err
	}
	if kustomization.HelmCharts, err = decodeHelmCharts(raw["helmCharts"]); err != nil {
		return nil, err
	}
	if kustomization.ConfigMapGenerators, err = decodeGeneratorArgsList(raw["configMapGenerator"]); err != nil {
		return nil, err
	}
	if kustomization.SecretGenerators, err = decodeGeneratorArgsList(raw["secretGenerator"]); err != nil {
		return nil, err
	}
	return kustomization, nil
}

func decodeStringList(raw map[string]any, field string) ([]string, error) {
	value, ok := raw[field]
	if !ok || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list", field)
	}
	result := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", field, index)
		}
		result = append(result, text)
	}
	return result, nil
}

func decodePatches(value any) ([]Patch, error) {
	items, ok := omitNil(value)
	if !ok {
		return nil, fmt.Errorf("patches must be a list")
	}
	result := make([]Patch, 0, len(items))
	for index, item := range items {
		switch typed := item.(type) {
		case string:
			// A bare string is the legacy patchesStrategicMerge shape.
			result = append(result, Patch{Path: typed})
		case map[string]any:
			patch := Patch{
				Path:  asString(typed["path"]),
				Patch: asString(typed["patch"]),
			}
			result = append(result, patch)
		default:
			return nil, fmt.Errorf("patches[%d] must be a string or mapping", index)
		}
	}
	return result, nil
}

func decodePluginEntries(value any) ([]PluginEntry, error) {
	items, ok := omitNil(value)
	if !ok {
		return nil, fmt.Errorf("generators/transformers must be a list")
	}
	result := make([]PluginEntry, 0, len(items))
	for index, item := range items {
		switch typed := item.(type) {
		case string:
			result = append(result, PluginEntry{Path: typed})
		case map[string]any:
			encoded, err := json.Marshal(convertMaps(typed))
			if err != nil {
				return nil, fmt.Errorf("generators/transformers[%d] is not encodable: %w", index, err)
			}
			result = append(result, PluginEntry{
				Spec:    convertMaps(typed),
				RawSpec: encoded,
			})
		default:
			return nil, fmt.Errorf("generators/transformers[%d] must be a string or mapping", index)
		}
	}
	return result, nil
}

func decodeHelmCharts(value any) ([]HelmChart, error) {
	items, ok := omitNil(value)
	if !ok {
		return nil, fmt.Errorf("helmCharts must be a list")
	}
	result := make([]HelmChart, 0, len(items))
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("helmCharts[%d] must be a mapping", index)
		}
		result = append(result, HelmChart{
			Name:    asString(mapping["name"]),
			Repo:    asString(mapping["repo"]),
			Version: asString(mapping["version"]),
		})
	}
	return result, nil
}

func decodeGeneratorArgsList(value any) ([]GeneratorArgs, error) {
	items, ok := omitNil(value)
	if !ok {
		return nil, fmt.Errorf("configMapGenerator/secretGenerator must be a list")
	}
	result := make([]GeneratorArgs, 0, len(items))
	for index, item := range items {
		mapping, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("generator entry %d must be a mapping", index)
		}
		files, err := decodeStringList(mapping, "files")
		if err != nil {
			return nil, err
		}
		result = append(result, GeneratorArgs{
			Name:  asString(mapping["name"]),
			Env:   asString(mapping["env"]),
			Files: files,
		})
	}
	return result, nil
}

// omitNil returns the list form of value, treating an absent field as empty.
func omitNil(value any) ([]any, bool) {
	if value == nil {
		return nil, true
	}
	items, ok := value.([]any)
	return items, ok
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

// convertMaps normalizes the map key type produced by different YAML
// libraries (map[string]any versus map[any]any) so json.Marshal succeeds.
func convertMaps(value any) map[string]any {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	converted := make(map[string]any, len(mapping))
	for key, item := range mapping {
		converted[key] = normalize(item)
	}
	return converted
}

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = normalize(item)
		}
		return converted
	case map[any]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[fmt.Sprint(key)] = normalize(item)
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, item := range typed {
			converted[index] = normalize(item)
		}
		return converted
	default:
		return value
	}
}
