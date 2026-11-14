package netsec

import (
	"sort"
	"strings"
)

type validator struct {
	plan          *Plan
	violations    []Violation
	aggs          map[string]*AggPlan
	removedAggs   map[string]struct{}
	sortSanitized bool
	collectAggs   bool
}

func (v *validator) addViolation(kind, field, context, detail string) {
	v.violations = append(v.violations, Violation{
		Kind:    kind,
		Field:   field,
		Context: context,
		Detail:  detail,
	})
}

func (v *validator) sanitize(request map[string]any) {
	if v.aggs == nil {
		v.aggs = make(map[string]*AggPlan)
	}
	if v.removedAggs == nil {
		v.removedAggs = make(map[string]struct{})
	}
	v.sanitizeSource(request)
	v.sanitizePlainFields(request, "fields", true)
	v.sanitizePlainFields(request, "docvalue_fields", true)
	v.sanitizePlainFields(request, "stored_fields", false)
	v.sanitizeScriptFields(request)
	v.sanitizeRuntimeMappings(request)
	v.sanitizeHighlight(request)
	v.sanitizeSort(request)

	if v.collectAggs {
		for _, key := range []string{"aggs", "aggregations"} {
			raw, ok := request[key]
			if !ok {
				continue
			}
			container, ok := asMap(raw)
			if !ok {
				v.addViolation("malformed", "", key, "aggregations must be an object")
				delete(request, key)
				continue
			}
			plans := v.sanitizeAggContainer(container, key)
			if len(plans) == 0 {
				delete(request, key)
				continue
			}
			mergeAggPlans(v.aggs, plans)
		}
	}
}

type sourceSpec struct {
	enabled  bool
	includes []string
	excludes []string
}

func (v *validator) sanitizeSource(request map[string]any) {
	raw, present := request["_source"]
	spec := parseSourceSpec(raw, present)
	if !spec.enabled {
		request["_source"] = false
		return
	}
	v.checkSelection("_source", spec.includes)
	fields := v.plan.selectFields(spec.includes, spec.excludes)
	if len(fields) == 0 {
		request["_source"] = false
		return
	}
	request["_source"] = fields
}

func parseSourceSpec(raw any, present bool) sourceSpec {
	if !present || raw == nil {
		return sourceSpec{enabled: true}
	}
	switch typed := raw.(type) {
	case bool:
		return sourceSpec{enabled: typed}
	case string:
		if typed == "_none_" || strings.TrimSpace(typed) == "" {
			return sourceSpec{enabled: false}
		}
		return sourceSpec{enabled: true, includes: []string{typed}}
	case []any:
		values, _ := toStrings(typed)
		return sourceSpec{enabled: true, includes: values}
	case []string:
		return sourceSpec{enabled: true, includes: append([]string(nil), typed...)}
	case map[string]any:
		spec := sourceSpec{enabled: true}
		for _, key := range []string{"includes", "include"} {
			if value, ok := typed[key]; ok {
				if values, ok := toStrings(value); ok {
					spec.includes = append(spec.includes, values...)
				}
			}
		}
		for _, key := range []string{"excludes", "exclude"} {
			if value, ok := typed[key]; ok {
				if values, ok := toStrings(value); ok {
					spec.excludes = append(spec.excludes, values...)
				}
			}
		}
		return spec
	default:
		return sourceSpec{enabled: true}
	}
}

func (v *validator) checkSelection(context string, patterns []string) {
	for _, pattern := range patterns {
		if !isConcreteField(pattern) {
			continue
		}
		if pattern == "_none_" || pattern == "_source" {
			continue
		}
		if v.plan.FieldAllowed(pattern) || v.plan.MayInclude(pattern) {
			continue
		}
		v.addViolation("field", pattern, context, "field is not authorized")
	}
}

func (v *validator) sanitizePlainFields(request map[string]any, key string, allowObjects bool) {
	raw, ok := request[key]
	if !ok {
		return
	}
	if key == "stored_fields" {
		if text, ok := raw.(string); ok && text == "_none_" {
			request[key] = "_none_"
			return
		}
	}
	values := v.sanitizeFieldList(raw, key, allowObjects)
	if key == "stored_fields" {
		if len(values) == 0 {
			request[key] = "_none_"
			return
		}
	}
	request[key] = values
}

func (v *validator) sanitizeFieldList(raw any, context string, allowObjects bool) []any {
	var patterns []string
	switch typed := raw.(type) {
	case string:
		patterns = []string{typed}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				patterns = append(patterns, text)
			}
		}
	case []string:
		patterns = append(patterns, typed...)
	}
	v.checkSelection(context, patterns)

	out := make([]any, 0, len(patterns))
	appendPattern := func(pattern string) {
		if pattern == "_none_" {
			out = append(out, pattern)
			return
		}
		for _, field := range v.plan.selectFields([]string{pattern}, nil) {
			out = append(out, field)
		}
	}

	switch typed := raw.(type) {
	case string:
		appendPattern(typed)
	case []any:
		for _, item := range typed {
			switch typedItem := item.(type) {
			case string:
				appendPattern(typedItem)
			case map[string]any:
				if !allowObjects {
					v.addViolation("malformed", "", context, "object entry is not supported")
					continue
				}
				v.appendFieldObject(&out, typedItem, context)
			default:
				v.addViolation("malformed", "", context, "field entry must be a string or object")
			}
		}
	case []string:
		for _, item := range typed {
			appendPattern(item)
		}
	case map[string]any:
		if !allowObjects {
			v.addViolation("malformed", "", context, "object entry is not supported")
			return nil
		}
		v.appendFieldObject(&out, typed, context)
	default:
		v.addViolation("malformed", "", context, "field selection must be a string or array")
	}

	seen := make(map[string]struct{}, len(out))
	deduped := make([]any, 0, len(out))
	for _, item := range out {
		switch typed := item.(type) {
		case string:
			if _, ok := seen[typed]; ok {
				continue
			}
			seen[typed] = struct{}{}
		}
		deduped = append(deduped, item)
	}
	return deduped
}

func (v *validator) appendFieldObject(out *[]any, object map[string]any, context string) {
	field, ok := object["field"].(string)
	if !ok || strings.TrimSpace(field) == "" {
		return
	}
	v.checkSelection(context, []string{field})
	for _, selected := range v.plan.selectFields([]string{field}, nil) {
		clone := cloneMap(object)
		clone["field"] = selected
		*out = append(*out, clone)
	}
}

// selectFields intersects requested patterns with the effective allow-list.
// It never turns a request wildcard into new grants.
func (p *Plan) selectFields(includes, excludes []string) []string {
	selected := make(map[string]struct{})
	for _, field := range p.allowed {
		if len(includes) > 0 {
			matched := false
			for _, pattern := range includes {
				if requestFieldMatches(pattern, field) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		excluded := false
		for _, pattern := range excludes {
			if requestFieldMatches(pattern, field) {
				excluded = true
				break
			}
		}
		if !excluded {
			selected[field] = struct{}{}
		}
	}
	for _, pattern := range includes {
		if !isConcreteField(pattern) {
			continue
		}
		if !p.FieldAllowed(pattern) {
			continue
		}
		excluded := false
		for _, exclude := range excludes {
			if requestFieldMatches(exclude, pattern) {
				excluded = true
				break
			}
		}
		if !excluded {
			selected[pattern] = struct{}{}
		}
	}

	out := make([]string, 0, len(selected))
	for field := range selected {
		hasSelectedChild := false
		for other := range selected {
			if fieldIsAncestor(field, other) {
				hasSelectedChild = true
				break
			}
		}
		if !hasSelectedChild {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func (v *validator) sanitizeScriptFields(request map[string]any) {
	raw, ok := request["script_fields"]
	if !ok {
		return
	}
	fields, ok := asMap(raw)
	if !ok {
		v.addViolation("malformed", "", "script_fields", "script_fields must be an object")
		delete(request, "script_fields")
		return
	}
	for _, name := range sortedMapKeys(fields) {
		definition := fields[name]
		context := "script_fields." + name
		if !v.plan.FieldAllowed(name) {
			v.addViolation("field", name, context, "script field name is not authorized")
			delete(fields, name)
			continue
		}
		source, ok, detail := scriptDefinition(definition)
		if !ok {
			v.addViolation("script", name, context, detail)
			delete(fields, name)
			continue
		}
		referenced, safe := extractScriptFields(source)
		if !safe {
			v.addViolation("script", name, context, "script field access cannot be statically bounded")
			delete(fields, name)
			continue
		}
		bad := false
		for _, field := range referenced {
			if !v.plan.ScriptFieldAllowed(field) {
				v.addViolation("script", field, context, "script reads an unauthorized field")
				bad = true
			}
		}
		if bad {
			delete(fields, name)
		}
	}
	if len(fields) == 0 {
		delete(request, "script_fields")
	}
}

func scriptDefinition(value any) (string, bool, string) {
	if object, ok := asMap(value); ok {
		if script, ok := object["script"]; ok {
			return scriptSource(script)
		}
		return scriptSource(object)
	}
	return scriptSource(value)
}

func (v *validator) sanitizeRuntimeMappings(request map[string]any) {
	raw, ok := request["runtime_mappings"]
	if !ok {
		return
	}
	mappings, ok := asMap(raw)
	if !ok {
		v.addViolation("malformed", "", "runtime_mappings", "runtime_mappings must be an object")
		delete(request, "runtime_mappings")
		return
	}
	for _, name := range sortedMapKeys(mappings) {
		definition := mappings[name]
		context := "runtime_mappings." + name
		if !v.plan.FieldAllowed(name) {
			v.addViolation("runtime", name, context, "runtime field name is not authorized")
			delete(mappings, name)
			continue
		}
		object, ok := asMap(definition)
		if !ok {
			v.addViolation("malformed", name, context, "runtime field definition must be an object")
			delete(mappings, name)
			continue
		}
		script, hasScript := object["script"]
		if !hasScript {
			continue
		}
		source, ok, detail := scriptSource(script)
		if !ok {
			v.addViolation("script", name, context, detail)
			delete(mappings, name)
			continue
		}
		referenced, safe := extractScriptFields(source)
		if !safe {
			v.addViolation("script", name, context, "runtime script cannot be statically bounded")
			delete(mappings, name)
			continue
		}
		bad := false
		for _, field := range referenced {
			if !v.plan.ScriptFieldAllowed(field) {
				v.addViolation("script", field, context, "runtime field reads an unauthorized field")
				bad = true
			}
		}
		if bad {
			delete(mappings, name)
		}
	}
	if len(mappings) == 0 {
		delete(request, "runtime_mappings")
	}
}

func (v *validator) sanitizeHighlight(request map[string]any) {
	raw, ok := request["highlight"]
	if !ok {
		return
	}
	highlight, ok := asMap(raw)
	if !ok {
		v.addViolation("malformed", "", "highlight", "highlight must be an object")
		delete(request, "highlight")
		return
	}
	rawFields, ok := highlight["fields"]
	if !ok {
		return
	}

	fields := make(map[string]any)
	switch typed := rawFields.(type) {
	case map[string]any:
		for _, pattern := range sortedMapKeys(typed) {
			options := typed[pattern]
			v.checkSelection("highlight.fields", []string{pattern})
			for _, field := range v.plan.selectFields([]string{pattern}, nil) {
				fields[field] = cloneValue(options)
			}
		}
	case []any:
		for _, item := range typed {
			pattern, ok := item.(string)
			if !ok {
				v.addViolation("malformed", "", "highlight.fields", "highlight field must be a string")
				continue
			}
			v.checkSelection("highlight.fields", []string{pattern})
			for _, field := range v.plan.selectFields([]string{pattern}, nil) {
				fields[field] = map[string]any{}
			}
		}
	case string:
		v.checkSelection("highlight.fields", []string{typed})
		for _, field := range v.plan.selectFields([]string{typed}, nil) {
			fields[field] = map[string]any{}
		}
	default:
		v.addViolation("malformed", "", "highlight.fields", "highlight fields must be an object or array")
	}
	if len(fields) == 0 {
		delete(highlight, "fields")
	} else {
		highlight["fields"] = fields
	}
}

func (v *validator) sanitizeSort(request map[string]any) {
	raw, ok := request["sort"]
	if !ok {
		return
	}
	items, ok := asAnySlice(raw)
	if !ok {
		items = []any{raw}
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if isSortMetadata(typed) || v.plan.FieldAllowed(typed) {
				out = append(out, typed)
				continue
			}
			v.addViolation("sort", typed, "sort", "sort field is not authorized")
			v.sortSanitized = true
		case map[string]any:
			if keep := v.sanitizeSortObject(typed); keep != nil {
				out = append(out, keep)
			} else {
				v.sortSanitized = true
			}
		default:
			v.addViolation("malformed", "", "sort", "sort entry must be a string or object")
			v.sortSanitized = true
		}
	}
	if len(out) == 0 {
		delete(request, "sort")
		return
	}
	request["sort"] = out
}

func (v *validator) sanitizeSortObject(object map[string]any) map[string]any {
	clone := cloneMap(object)
	for _, key := range sortedMapKeys(object) {
		if isSortMetadata(key) {
			continue
		}
		if key == "_script" {
			source, ok, detail := scriptDefinition(object[key])
			if !ok {
				v.addViolation("script", "_script", "sort", detail)
				return nil
			}
			referenced, safe := extractScriptFields(source)
			if !safe {
				v.addViolation("script", "_script", "sort", "sort script cannot be statically bounded")
				return nil
			}
			for _, field := range referenced {
				if !v.plan.ScriptFieldAllowed(field) {
					v.addViolation("script", field, "sort", "sort script reads an unauthorized field")
					return nil
				}
			}
			continue
		}
		if !v.plan.FieldAllowed(key) {
			v.addViolation("sort", key, "sort", "sort field is not authorized")
			return nil
		}
	}
	return clone
}

func isSortMetadata(field string) bool {
	return field == "_score" || field == "_doc"
}

func (v *validator) sanitizeAggContainer(container map[string]any, context string) map[string]*AggPlan {
	plans := make(map[string]*AggPlan)
	for _, name := range sortedMapKeys(container) {
		body, ok := asMap(container[name])
		if !ok {
			v.addViolation("malformed", "", context+"."+name, "aggregation body must be an object")
			v.removedAggs[name] = struct{}{}
			delete(container, name)
			continue
		}
		node, ok := v.sanitizeAggBody(name, body, context+"."+name)
		if !ok {
			v.removedAggs[name] = struct{}{}
			delete(container, name)
			continue
		}
		container[name] = body
		plans[name] = node
	}
	return plans
}

func (v *validator) sanitizeAggBody(name string, body map[string]any, context string) (*AggPlan, bool) {
	if !v.scanAggBody(body, context) {
		return nil, false
	}
	clone := cloneMap(body)
	v.sanitizeEmbeddedHits(clone, context)
	node := &AggPlan{Name: name, Children: make(map[string]*AggPlan)}
	seenBody := clone

	for _, key := range []string{"aggs", "aggregations"} {
		raw, ok := seenBody[key]
		if !ok {
			continue
		}
		children, ok := asMap(raw)
		if !ok {
			v.addViolation("malformed", "", context+"."+key, "sub-aggregations must be an object")
			delete(seenBody, key)
			continue
		}
		childPlans := v.sanitizeAggContainer(children, context+"."+key)
		if len(childPlans) == 0 {
			delete(seenBody, key)
			continue
		}
		seenBody[key] = children
		mergeAggPlans(node.Children, childPlans)
	}

	for key := range body {
		delete(body, key)
	}
	for key, value := range seenBody {
		body[key] = value
	}
	return node, true
}

func (v *validator) scanAggBody(value any, context string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedMapKeys(typed) {
			item := typed[key]
			switch key {
			case "aggs", "aggregations":
				continue
			case "_source", "fields", "docvalue_fields", "stored_fields", "script_fields", "highlight", "runtime_mappings":
				// These are output selections. sanitizeEmbeddedHits applies the
				// normal request sanitizer to top_hits options, including
				// script field validation.
				continue
			case "script":
				if !v.validateScript(item, context+"."+key) {
					return false
				}
				continue
			case "field":
				switch fields := item.(type) {
				case string:
					if !v.plan.FieldAllowed(fields) {
						v.addViolation("aggregation", fields, context, "aggregation field is not authorized")
						return false
					}
				case []any:
					for _, field := range fields {
						text, ok := field.(string)
						if !ok {
							v.addViolation("malformed", "", context, "field array must contain strings")
							return false
						}
						if !v.plan.FieldAllowed(text) {
							v.addViolation("aggregation", text, context, "aggregation field is not authorized")
							return false
						}
					}
				case []string:
					for _, field := range fields {
						if !v.plan.FieldAllowed(field) {
							v.addViolation("aggregation", field, context, "aggregation field is not authorized")
							return false
						}
					}
				default:
					v.addViolation("malformed", "", context, "aggregation field must be a string or array")
					return false
				}
				continue
			case "nested", "reverse_nested", "children":
				if object, ok := asMap(item); ok {
					if pathValue, ok := object["path"].(string); ok {
						if !v.plan.FieldAllowed(pathValue) && !v.plan.MayInclude(pathValue) {
							v.addViolation("aggregation", pathValue, context+"."+key, "nested path is not authorized")
							return false
						}
					}
				}
			}
			if strings.HasSuffix(key, "_script") {
				if !v.validateScript(item, context+"."+key) {
					return false
				}
				continue
			}
			if !v.scanAggBody(item, context+"."+key) {
				return false
			}
		}
		return true
	case []any:
		for _, item := range typed {
			if !v.scanAggBody(item, context) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func (v *validator) validateScript(value any, context string) bool {
	source, ok, detail := scriptSource(value)
	if !ok {
		v.addViolation("script", "", context, detail)
		return false
	}
	referenced, safe := extractScriptFields(source)
	if !safe {
		v.addViolation("script", "", context, "script cannot be statically bounded")
		return false
	}
	for _, field := range referenced {
		if !v.plan.ScriptFieldAllowed(field) {
			v.addViolation("script", field, context, "script reads an unauthorized field")
			return false
		}
	}
	return true
}

func (v *validator) sanitizeEmbeddedHits(value any, context string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedMapKeys(typed) {
			if key == "aggs" || key == "aggregations" {
				continue
			}
			if key == "top_hits" {
				if options, ok := asMap(typed[key]); ok {
					previous := v.collectAggs
					v.collectAggs = false
					v.sanitize(options)
					v.collectAggs = previous
				}
				continue
			}
			v.sanitizeEmbeddedHits(typed[key], context+"."+key)
		}
	case []any:
		for _, item := range typed {
			v.sanitizeEmbeddedHits(item, context)
		}
	}
}
