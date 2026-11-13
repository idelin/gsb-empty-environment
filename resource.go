package netsec

import "reflect"

// NewResource builds a Resource from a rendered object represented as
// nested map[string]any / []any values (for example
// unstructured.Unstructured.Object). It extracts kind, namespace and name
// from metadata and flattens every remaining string field.
func NewResource(object map[string]any) Resource {
	resource := Resource{
		Kind:   stringAt(object, "kind"),
		Fields: Flatten(object),
	}
	if metadata, ok := object["metadata"].(map[string]any); ok {
		resource.Namespace = stringAt(metadata, "namespace")
		resource.Name = stringAt(metadata, "name")
	}
	return resource
}

func stringAt(object map[string]any, key string) string {
	value, ok := object[key]
	if !ok {
		return ""
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.String {
		return reflected.String()
	}
	return ""
}
