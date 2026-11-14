package netsec

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "logs-a",
		Fields: []string{"public.id", "public.msg", "private.secret", "private.ssn"},
		Policy: Policy{AllowedFields: []string{"public.*"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.AddIndex(Index{
		Name:   "logs-b",
		Fields: []string{"public.id", "public.msg", "private.secret", "private.ssn"},
		Policy: Policy{AllowedFields: []string{"public.id"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateAlias("logs", "logs-a", "logs-b"); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestAliasIntersectionAppliesToWildcardFieldsHighlightAndAggs(t *testing.T) {
	catalog := testCatalog(t)
	authorizer := NewAuthorizer(catalog)

	request := map[string]any{
		"_source": []any{"*"},
		"fields":  []any{"*"},
		"highlight": map[string]any{
			"fields": map[string]any{"*": map[string]any{}},
		},
		"aggs": map[string]any{
			"by_public": map[string]any{"terms": map[string]any{"field": "public.id"}},
			"by_msg":    map[string]any{"terms": map[string]any{"field": "public.msg"}},
			"by_secret": map[string]any{"terms": map[string]any{"field": "private.secret"}},
		},
	}

	plan, sanitized, err := authorizer.Authorize("logs", request)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, ok := request["aggs"].(map[string]any)["by_secret"]; !ok {
		t.Fatal("Authorize mutated the caller's request")
	}
	if got := toStringsOrNil(request["_source"]); !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("Authorize mutated _source: %v", got)
	}
	if got, want := plan.AuthorizedFields(), []string{"public.id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("authorized fields = %v, want %v", got, want)
	}
	if got := toStringsOrNil(sanitized["_source"]); !reflect.DeepEqual(got, []string{"public.id"}) {
		t.Fatalf("_source = %v", got)
	}

	highlight := sanitized["highlight"].(map[string]any)["fields"].(map[string]any)
	if _, ok := highlight["public.id"]; !ok {
		t.Fatalf("authorized highlight field missing: %v", highlight)
	}
	if _, ok := highlight["public.msg"]; ok {
		t.Fatalf("unauthorized highlight field survived: %v", highlight)
	}

	aggs := sanitized["aggs"].(map[string]any)
	if _, ok := aggs["by_public"]; !ok {
		t.Fatalf("authorized aggregation missing: %v", aggs)
	}
	if _, ok := aggs["by_msg"]; ok {
		t.Fatalf("aggregation on field excluded by one target survived: %v", aggs)
	}
	if _, ok := aggs["by_secret"]; ok {
		t.Fatalf("private aggregation survived: %v", aggs)
	}

	response := map[string]any{
		"hits": map[string]any{
			"hits": []any{
				map[string]any{
					"_index": "logs-a",
					"_source": map[string]any{
						"public": map[string]any{
							"id":  "1",
							"msg": "private-ish",
						},
						"private": map[string]any{"secret": "leak", "ssn": "000-00-0000"},
					},
					"fields": map[string]any{
						"public.id":  []any{"1"},
						"public.msg": []any{"private-ish"},
					},
					"highlight": map[string]any{
						"public.id":  []any{"<em>1</em>"},
						"public.msg": []any{"<em>leak</em>"},
					},
				},
			},
		},
		"aggregations": map[string]any{
			"by_public": map[string]any{
				"buckets": []any{
					map[string]any{
						"key":       "1",
						"doc_count": 1,
						"by_secret": map[string]any{
							"buckets": []any{map[string]any{"key": "leak", "doc_count": 1}},
						},
					},
				},
			},
			"by_secret": map[string]any{
				"buckets": []any{map[string]any{"key": "leak", "doc_count": 1}},
			},
		},
	}
	filtered := authorizer.FilterResponse(plan, response)
	assertNoFieldPath(t, filtered, "private")
	assertNoFieldPath(t, filtered, "public.msg")
	assertFieldPath(t, filtered, "public.id")

	hits := filtered["hits"].(map[string]any)["hits"].([]any)
	hit := hits[0].(map[string]any)
	if _, ok := hit["highlight"].(map[string]any)["public.msg"]; ok {
		t.Fatal("unauthorized highlight field survived response filtering")
	}
	aggsOut := filtered["aggregations"].(map[string]any)
	if _, ok := aggsOut["by_secret"]; ok {
		t.Fatal("unauthorized aggregation survived response filtering")
	}
	byPublic := aggsOut["by_public"].(map[string]any)
	bucket := byPublic["buckets"].([]any)[0].(map[string]any)
	if _, ok := bucket["by_secret"]; ok {
		t.Fatal("unauthorized nested aggregation survived response filtering")
	}
	if bucket["key"] != "1" {
		t.Fatalf("authorized bucket key lost: %v", bucket)
	}
}

func TestScriptRuntimeAndAggregationScriptReadsAreBounded(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "secure",
		Fields: []string{"public.id", "public.label", "private.secret", "profile", "profile.name", "profile.ssn"},
		Policy: Policy{AllowedFields: []string{"public.*", "profile", "profile.name"}},
	}); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"script_fields": map[string]any{
			"public.safe": map[string]any{
				"script": map[string]any{"source": "doc['public.label'].value"},
			},
			"public.leak": map[string]any{
				"script": map[string]any{"source": "doc['private.secret'].value"},
			},
			"public.dynamic": map[string]any{
				"script": map[string]any{"source": "doc[fieldName].value"},
			},
			"public.parent_leak": map[string]any{
				"script": map[string]any{"source": "params._source.profile"},
			},
			"public.parent_ok": map[string]any{
				"script": map[string]any{"source": "params._source.profile.name"},
			},
		},
		"runtime_mappings": map[string]any{
			"public.runtime": map[string]any{
				"type":   "keyword",
				"script": map[string]any{"source": "params._source.public.label"},
			},
			"public.runtime_leak": map[string]any{
				"type":   "keyword",
				"script": map[string]any{"source": "params['_source']['private']['secret']"},
			},
			"private.runtime": map[string]any{
				"type":   "keyword",
				"script": map[string]any{"source": "params._source.public.label"},
			},
		},
		"aggs": map[string]any{
			"by_public": map[string]any{
				"terms": map[string]any{"field": "public.id"},
				"aggs": map[string]any{
					"by_secret": map[string]any{
						"terms": map[string]any{"field": "private.secret"},
					},
				},
			},
			"scripted": map[string]any{
				"terms": map[string]any{
					"script": map[string]any{"source": "doc['private.secret'].value"},
				},
			},
			"scripted_metric": map[string]any{
				"scripted_metric": map[string]any{
					"init_script": "state.values = []",
					"map_script":  "state.values.add(doc['private.secret'].value)",
				},
			},
		},
	}

	permissive := NewAuthorizer(catalog)
	plan, sanitized, err := permissive.Authorize("secure", request)
	if err != nil {
		t.Fatalf("permissive authorize: %v", err)
	}
	if len(plan.Violations) == 0 {
		t.Fatal("expected recorded policy violations")
	}
	scriptFields := sanitized["script_fields"].(map[string]any)
	if _, ok := scriptFields["public.safe"]; !ok {
		t.Fatalf("safe script field removed: %v", scriptFields)
	}
	if _, ok := scriptFields["public.leak"]; ok {
		t.Fatalf("script field reading private data survived: %v", scriptFields)
	}
	if _, ok := scriptFields["public.dynamic"]; ok {
		t.Fatalf("unbounded dynamic script survived: %v", scriptFields)
	}
	if _, ok := scriptFields["public.parent_leak"]; ok {
		t.Fatalf("script reading a parent object with restricted children survived: %v", scriptFields)
	}
	if _, ok := scriptFields["public.parent_ok"]; !ok {
		t.Fatalf("script reading an allowed leaf was removed: %v", scriptFields)
	}
	runtimeMappings := sanitized["runtime_mappings"].(map[string]any)
	if _, ok := runtimeMappings["public.runtime"]; !ok {
		t.Fatalf("safe runtime field removed: %v", runtimeMappings)
	}
	if _, ok := runtimeMappings["public.runtime_leak"]; ok {
		t.Fatalf("runtime field reading private data survived: %v", runtimeMappings)
	}
	if _, ok := runtimeMappings["private.runtime"]; ok {
		t.Fatalf("runtime field expanded the allow-list: %v", runtimeMappings)
	}
	aggs := sanitized["aggs"].(map[string]any)
	byPublic := aggs["by_public"].(map[string]any)
	if _, ok := byPublic["aggs"]; ok {
		t.Fatalf("private sub-aggregation survived: %v", byPublic)
	}
	if _, ok := aggs["scripted"]; ok {
		t.Fatalf("aggregation script reading private data survived: %v", aggs)
	}
	if _, ok := aggs["scripted_metric"]; ok {
		t.Fatalf("scripted_metric reading private data survived: %v", aggs)
	}

	strict, err := NewAuthorizerWithConfig(catalog, Config{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, strictBody, strictErr := strict.Authorize("secure", request)
	if !errors.Is(strictErr, ErrUnauthorized) {
		t.Fatalf("strict error = %v, want ErrUnauthorized", strictErr)
	}
	if _, ok := strictBody["aggs"].(map[string]any)["scripted"]; ok {
		t.Fatal("strict response leaked a denied aggregation")
	}
}

func TestAliasLevelPolicyPropagatesThroughResolution(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "backing",
		Fields: []string{"public.id", "public.msg", "private.secret"},
		Policy: Policy{AllowedFields: []string{"public.*"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.PutAlias(Alias{
		Name:    "restricted",
		Indices: []string{"backing"},
		Policy:  &Policy{AllowedFields: []string{"public.id"}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer(catalog)

	aliasPlan, aliasRequest, err := authorizer.Authorize("restricted", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if got := aliasPlan.AuthorizedFields(); !reflect.DeepEqual(got, []string{"public.id"}) {
		t.Fatalf("alias fields = %v", got)
	}
	if got := toStringsOrNil(aliasRequest["_source"]); !reflect.DeepEqual(got, []string{"public.id"}) {
		t.Fatalf("alias _source = %v", got)
	}

	directPlan, _, err := authorizer.Authorize("backing", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := directPlan.AuthorizedFields(), []string{"public.id", "public.msg"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("direct index fields = %v, want %v", got, want)
	}
}

func TestRecursivePolicyAllowsWholeSubtreeScriptRead(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "deep",
		Fields: []string{"public.branch", "public.branch.leaf", "private.secret"},
		Policy: Policy{AllowedFields: []string{"public.**"}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer(catalog)
	plan, sanitized, err := authorizer.Authorize("deep", map[string]any{
		"script_fields": map[string]any{
			"public.read": map[string]any{
				"script": map[string]any{"source": "params._source.public.branch"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sanitized["script_fields"].(map[string]any)["public.read"]; !ok {
		t.Fatalf("whole-subtree script read was removed; violations=%v", plan.Violations)
	}
}

func TestScriptFieldExtraction(t *testing.T) {
	tests := []struct {
		script string
		fields []string
		safe   bool
	}{
		{"doc['private.secret'].value", []string{"private.secret"}, true},
		{"doc.containsKey('private.secret')", []string{"private.secret"}, true},
		{"params._source.private.secret", []string{"private.secret"}, true},
		{"params['_source']['private']['secret']", []string{"private.secret"}, true},
		{"field('private.secret')", []string{"private.secret"}, true},
		{"doc[fieldName].value", nil, false},
		{"doc['private' + '.secret'].value", nil, false},
		{"doc.containsKey('private' + '.secret')", nil, false},
		{"params['_source'].get('private')", nil, false},
		{"params._source", nil, false},
		{"params['_source']", nil, false},
	}
	for _, test := range tests {
		fields, safe := extractScriptFields(test.script)
		if safe != test.safe {
			t.Fatalf("%q: safe = %v, want %v", test.script, safe, test.safe)
		}
		if test.safe && !reflect.DeepEqual(fields, test.fields) {
			t.Fatalf("%q: fields = %v, want %v", test.script, fields, test.fields)
		}
	}
}

func TestAliasRetargetAndPermissionChangeInvalidatePlans(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "old",
		Fields: []string{"public.old", "private.secret"},
		Policy: Policy{AllowedFields: []string{"public.old"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.AddIndex(Index{
		Name:   "new",
		Fields: []string{"public.new", "public.extra", "private.secret"},
		Policy: Policy{AllowedFields: []string{"public.new"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateAlias("current", "old"); err != nil {
		t.Fatal(err)
	}

	authorizer := NewAuthorizer(catalog)
	firstPlan, _, err := authorizer.Authorize("current", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstPlan.AuthorizedFields(); !reflect.DeepEqual(got, []string{"public.old"}) {
		t.Fatalf("initial fields = %v", got)
	}
	firstGeneration := firstPlan.Generation
	if stats := authorizer.CacheStats(); stats.Entries != 1 || stats.Generation != firstGeneration {
		t.Fatalf("cache stats = %+v", stats)
	}

	if err := catalog.RetargetAlias("current", "new"); err != nil {
		t.Fatal(err)
	}
	if stats := authorizer.CacheStats(); stats.Generation != catalog.Generation() || stats.Entries != 0 {
		t.Fatalf("cache retained old generation entries: %+v", stats)
	}
	retargeted, _, err := authorizer.Authorize("current", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if retargeted.Generation <= firstGeneration {
		t.Fatalf("generation did not advance: %d -> %d", firstGeneration, retargeted.Generation)
	}
	if firstPlan.ValidAgainst(catalog) {
		t.Fatal("old plan is still considered current after retarget")
	}
	if got := retargeted.AuthorizedFields(); !reflect.DeepEqual(got, []string{"public.new"}) {
		t.Fatalf("retargeted fields = %v", got)
	}
	if stats := authorizer.CacheStats(); stats.Generation != retargeted.Generation {
		t.Fatalf("cache was not reset on generation change: %+v", stats)
	}

	if err := catalog.SetIndexPolicy("new", Policy{AllowedFields: []string{"public.new", "public.extra"}}); err != nil {
		t.Fatal(err)
	}
	changed, _, err := authorizer.Authorize("current", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Generation <= retargeted.Generation {
		t.Fatalf("policy generation did not advance: %d -> %d", retargeted.Generation, changed.Generation)
	}
	if got, want := changed.AuthorizedFields(), []string{"public.extra", "public.new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields after policy change = %v, want %v", got, want)
	}
	if retargeted.ValidAgainst(catalog) {
		t.Fatal("plan compiled before policy change is still considered current")
	}
	if _, err := authorizer.FilterResponseCurrent(retargeted, map[string]any{"hits": map[string]any{"hits": []any{}}}); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("FilterResponseCurrent stale error = %v, want ErrStalePlan", err)
	}
}

func TestAliasWriteTargetUsesGeneration(t *testing.T) {
	catalog := NewCatalog()
	for _, index := range []Index{
		{Name: "write-a", Fields: []string{"public.a", "private.secret"}, Policy: Policy{AllowedFields: []string{"public.a"}}},
		{Name: "write-b", Fields: []string{"public.b", "private.secret"}, Policy: Policy{AllowedFields: []string{"public.b"}}},
	} {
		if err := catalog.AddIndex(index); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.PutAlias(Alias{Name: "events", Indices: []string{"write-a", "write-b"}, WriteIndex: "write-a"}); err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer(catalog)
	first, err := authorizer.ResolveWrite("events")
	if err != nil {
		t.Fatal(err)
	}
	if first.Index != "write-a" || !reflect.DeepEqual(first.AllowedFields, []string{"public.a"}) {
		t.Fatalf("initial write target = %+v", first)
	}

	if err := catalog.SetAliasWriteIndex("events", "write-b"); err != nil {
		t.Fatal(err)
	}
	second, err := authorizer.ResolveWrite("events")
	if err != nil {
		t.Fatal(err)
	}
	if second.Index != "write-b" || !reflect.DeepEqual(second.AllowedFields, []string{"public.b"}) {
		t.Fatalf("new write target = %+v", second)
	}
	if second.Generation <= first.Generation || second.AliasGeneration <= first.AliasGeneration {
		t.Fatalf("write generation did not advance: %+v -> %+v", first, second)
	}
	if first.ValidAgainst(catalog) {
		t.Fatal("old write target is still current after alias write-index change")
	}
	if !second.ValidAgainst(catalog) {
		t.Fatal("new write target is not current")
	}

	_, document, err := authorizer.AuthorizeDocument("events", map[string]any{
		"public":  map[string]any{"b": "kept"},
		"private": map[string]any{"secret": "dropped"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := document["private"]; ok {
		t.Fatalf("write document leaked private data: %v", document)
	}
}

func TestParentAndRuntimeFieldsDoNotGrantChildren(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.AddIndex(Index{
		Name:   "profile-index",
		Fields: []string{"profile", "profile.name", "profile.ssn", "public.id"},
		Policy: Policy{AllowedFields: []string{"profile", "profile.name", "public.id"}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer(catalog)
	plan, sanitized, err := authorizer.Authorize("profile-index", map[string]any{
		"_source": []any{"profile", "*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := toStringsOrNil(sanitized["_source"]), []string{"profile.name", "public.id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("_source = %v, want %v", got, want)
	}

	response := map[string]any{
		"hits": map[string]any{
			"hits": []any{
				map[string]any{
					"_source": map[string]any{
						"profile": map[string]any{
							"name": "Ada",
							"ssn":  "000-00-0000",
						},
						"public": map[string]any{"id": "42"},
					},
				},
			},
		},
	}
	filtered := authorizer.FilterResponse(plan, response)
	assertNoFieldPath(t, filtered, "profile.ssn")
	assertNoFieldPath(t, filtered, "private")
	assertFieldPath(t, filtered, "profile.name")
	assertFieldPath(t, filtered, "public.id")

	strictParent := NewCatalog()
	if err := strictParent.AddIndex(Index{
		Name:   "parent-only",
		Fields: []string{"profile", "profile.name", "profile.ssn"},
		Policy: Policy{AllowedFields: []string{"profile"}},
	}); err != nil {
		t.Fatal(err)
	}
	parentPlan, _, err := NewAuthorizer(strictParent).Authorize("parent-only", map[string]any{"_source": "*"})
	if err != nil {
		t.Fatal(err)
	}
	parentFiltered := NewAuthorizer(strictParent).FilterResponse(parentPlan, map[string]any{
		"hits": map[string]any{
			"hits": []any{map[string]any{"_source": map[string]any{
				"profile": map[string]any{"name": "Ada", "ssn": "000-00-0000"},
			}}},
		},
	})
	assertNoFieldPath(t, parentFiltered, "profile.ssn")
	assertNoFieldPath(t, parentFiltered, "profile.name")
}

func TestJSONGatewayPathFiltersOnlyAuthorizedFields(t *testing.T) {
	catalog := testCatalog(t)
	authorizer := NewAuthorizer(catalog)
	plan, requestJSON, err := authorizer.AuthorizeJSON("logs", []byte(`{
		"_source": ["*"],
		"fields": ["public.*", "private.*"],
		"highlight": {"fields": {"*": {}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	if got := toStringsOrNil(request["_source"]); !reflect.DeepEqual(got, []string{"public.id"}) {
		t.Fatalf("JSON _source = %v", got)
	}

	responseJSON, err := json.Marshal(map[string]any{
		"hits": map[string]any{"hits": []any{
			map[string]any{"_source": map[string]any{
				"public":  map[string]any{"id": "1", "msg": "hidden"},
				"private": map[string]any{"secret": "leak"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	filteredJSON, err := authorizer.FilterResponseJSON(plan, responseJSON)
	if err != nil {
		t.Fatal(err)
	}
	var filtered map[string]any
	if err := json.Unmarshal(filteredJSON, &filtered); err != nil {
		t.Fatal(err)
	}
	assertNoFieldPath(t, filtered, "private")
	assertNoFieldPath(t, filtered, "public.msg")
	assertFieldPath(t, filtered, "public.id")
}

func toStringsOrNil(value any) []string {
	values, ok := toStrings(value)
	if !ok {
		return nil
	}
	return values
}

func assertNoFieldPath(t *testing.T, value any, path string) {
	t.Helper()
	if _, ok := lookupPath(value, path); ok {
		t.Fatalf("unauthorized field %q is present in %#v", path, value)
	}
}

func assertFieldPath(t *testing.T, value any, path string) {
	t.Helper()
	if _, ok := lookupPath(value, path); !ok {
		t.Fatalf("authorized field %q is missing from %#v", path, value)
	}
}

func lookupPath(value any, path string) (any, bool) {
	return lookupParts(value, strings.Split(path, "."))
}

func lookupParts(value any, parts []string) (any, bool) {
	if len(parts) == 0 {
		return value, true
	}
	switch typed := value.(type) {
	case map[string]any:
		if literal, ok := typed[strings.Join(parts, ".")]; ok {
			return literal, true
		}
		next, ok := typed[parts[0]]
		if !ok {
			for _, item := range typed {
				if found, ok := lookupParts(item, parts); ok {
					return found, true
				}
			}
			return nil, false
		}
		return lookupParts(next, parts[1:])
	case []any:
		for _, item := range typed {
			if found, ok := lookupParts(item, parts); ok {
				return found, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}
