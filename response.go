package netsec

import "strings"

// FilterResponse removes every field, highlight, and aggregation that is not
// part of the plan's effective allow-list. It is intentionally independent of
// the backend: even a malicious or buggy backend response cannot reintroduce a
// denied field.
func (a *Authorizer) FilterResponse(plan *Plan, response map[string]any) map[string]any {
	if plan == nil || response == nil {
		return nil
	}
	out := cloneMap(response)
	if hits, ok := out["hits"]; ok {
		out["hits"] = filterHitsContainer(plan, hits)
	}
	for _, key := range []string{"aggregations", "aggs"} {
		raw, ok := out[key]
		if !ok {
			continue
		}
		out[key] = filterAggregationContainer(plan, raw)
	}
	return out
}

// FilterResponseCurrent is the fail-closed response variant for gateways that
// must not continue a request after the catalog generation changed. In-flight
// requests that intentionally keep their old snapshot should use FilterResponse
// with the plan they were started with.
func (a *Authorizer) FilterResponseCurrent(plan *Plan, response map[string]any) (map[string]any, error) {
	if a == nil || a.catalog == nil {
		return nil, ErrStalePlan
	}
	if plan == nil || plan.Generation != a.catalog.Generation() {
		return nil, ErrStalePlan
	}
	return a.FilterResponse(plan, response), nil
}

func filterHitsContainer(plan *Plan, value any) any {
	container, ok := asMap(value)
	if !ok {
		return value
	}
	out := cloneMap(container)
	if rawHits, ok := out["hits"]; ok {
		items, ok := asAnySlice(rawHits)
		if !ok {
			return out
		}
		filtered := make([]any, 0, len(items))
		for _, item := range items {
			hit, ok := asMap(item)
			if !ok {
				continue
			}
			filtered = append(filtered, filterHit(plan, hit))
		}
		out["hits"] = filtered
	}
	return out
}

func filterHit(plan *Plan, hit map[string]any) map[string]any {
	out := cloneMap(hit)
	if source, ok := out["_source"]; ok {
		if object, ok := asMap(source); ok {
			out["_source"] = filterObject(plan, "", object)
		} else {
			delete(out, "_source")
		}
	}
	if fields, ok := out["fields"]; ok {
		if object, ok := asMap(fields); ok {
			out["fields"] = filterObject(plan, "", object)
		} else {
			delete(out, "fields")
		}
	}
	if highlight, ok := out["highlight"]; ok {
		if object, ok := asMap(highlight); ok {
			filtered := make(map[string]any)
			for field, value := range object {
				if plan.FieldAllowed(field) {
					filtered[field] = cloneValue(value)
				}
			}
			if len(filtered) == 0 {
				delete(out, "highlight")
			} else {
				out["highlight"] = filtered
			}
		} else {
			delete(out, "highlight")
		}
	}
	if ignored, ok := out["_ignored"]; ok {
		if values, ok := asAnySlice(ignored); ok {
			filtered := make([]any, 0, len(values))
			for _, value := range values {
				field, ok := value.(string)
				if ok && plan.FieldAllowed(field) {
					filtered = append(filtered, value)
				}
			}
			out["_ignored"] = filtered
		}
	}
	if plan.SortSanitized {
		delete(out, "sort")
	}
	if inner, ok := out["inner_hits"]; ok {
		if object, ok := asMap(inner); ok {
			filtered := make(map[string]any)
			for name, value := range object {
				filtered[name] = filterHitsContainer(plan, value)
			}
			out["inner_hits"] = filtered
		}
	}
	return out
}

// filterObject filters a nested _source/fields object. A container is kept
// only when the container itself is authorized or it still contains an
// authorized descendant.
func filterObject(plan *Plan, prefix string, object map[string]any) map[string]any {
	out := make(map[string]any)
	for key, value := range object {
		path := joinField(prefix, key)
		if !plan.MayInclude(path) {
			continue
		}
		filtered, keep := filterObjectValue(plan, path, value)
		if keep {
			out[key] = filtered
		}
	}
	return out
}

func filterObjectValue(plan *Plan, path string, value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if !plan.MayInclude(path) {
			return nil, false
		}
		filtered := filterObject(plan, path, typed)
		if len(filtered) == 0 && !plan.FieldAllowed(path) {
			return nil, false
		}
		return filtered, true
	case []any:
		if !plan.MayInclude(path) && !plan.FieldAllowed(path) {
			return nil, false
		}
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			filtered, keep := filterObjectValue(plan, path, item)
			if keep {
				out = append(out, filtered)
			}
		}
		if len(out) == 0 && !plan.FieldAllowed(path) {
			return nil, false
		}
		return out, true
	default:
		if plan.FieldAllowed(path) {
			return value, true
		}
		return nil, false
	}
}

func filterAggregationContainer(plan *Plan, value any) any {
	container, ok := asMap(value)
	if !ok {
		return value
	}
	out := make(map[string]any)
	for name, result := range container {
		if _, removed := plan.removedAggs[name]; removed {
			continue
		}
		node, ok := plan.Aggs[name]
		if !ok {
			continue
		}
		out[name] = filterAggregationResult(plan, result, node)
	}
	return out
}

func filterAggregationResult(plan *Plan, value any, node *AggPlan) any {
	container, ok := asMap(value)
	if !ok {
		return value
	}
	out := make(map[string]any)
	for key, item := range container {
		if _, removed := plan.removedAggs[key]; removed && !isStandardAggregationResultKey(key) {
			continue
		}
		if child, ok := node.Children[key]; ok {
			out[key] = filterAggregationResult(plan, item, child)
			continue
		}
		switch key {
		case "buckets":
			out[key] = filterBuckets(plan, item, node)
		case "hits":
			out[key] = filterHitsContainer(plan, item)
		default:
			if looksLikeUnexpectedAggregation(key, item) {
				continue
			}
			out[key] = cloneValue(item)
		}
	}
	return out
}

func filterBuckets(plan *Plan, value any, node *AggPlan) any {
	if bucketObject, ok := asMap(value); ok {
		out := make(map[string]any, len(bucketObject))
		for name, bucketValue := range bucketObject {
			bucket, ok := asMap(bucketValue)
			if !ok {
				out[name] = cloneValue(bucketValue)
				continue
			}
			out[name] = filterBucketObject(plan, bucket, node)
		}
		return out
	}
	buckets, ok := asAnySlice(value)
	if !ok {
		return value
	}
	out := make([]any, 0, len(buckets))
	for _, item := range buckets {
		bucket, ok := asMap(item)
		if !ok {
			continue
		}
		out = append(out, filterBucketObject(plan, bucket, node))
	}
	return out
}

func filterBucketObject(plan *Plan, bucket map[string]any, node *AggPlan) map[string]any {
	filtered := make(map[string]any)
	for key, childValue := range bucket {
		if _, removed := plan.removedAggs[key]; removed && !isStandardAggregationResultKey(key) {
			continue
		}
		if child, ok := node.Children[key]; ok {
			filtered[key] = filterAggregationResult(plan, childValue, child)
			continue
		}
		if looksLikeUnexpectedAggregation(key, childValue) {
			continue
		}
		filtered[key] = cloneValue(childValue)
	}
	return filtered
}

func looksLikeUnexpectedAggregation(key string, value any) bool {
	if isStandardAggregationResultKey(key) {
		return false
	}
	object, ok := asMap(value)
	if !ok {
		return false
	}
	for _, marker := range []string{"buckets", "doc_count", "doc_count_error_upper_bound", "sum_other_doc_count"} {
		if _, ok := object[marker]; ok {
			return true
		}
	}
	return false
}

func isStandardAggregationResultKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "key", "key_as_string", "doc_count", "doc_count_error_upper_bound",
		"sum_other_doc_count", "from", "from_as_string", "to", "to_as_string",
		"value", "value_as_string", "values", "count", "sum", "min", "max",
		"avg", "std_deviation", "std_deviation_bounds", "percentiles", "hits",
		"score", "top", "bottom", "interval", "background_count":
		return true
	default:
		return false
	}
}
