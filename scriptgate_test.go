package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateQueryAllowsStoredScripts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"plain stored script": `{
			"query": {"term": {"user": "alice"}},
			"script_fields": {
				"greeting": {"script": {"id": "say-hello", "params": {"name": "alice"}}}
			}
		}`,
		"stored script without params": `{
			"script": {"id": "say-hello"}
		}`,
		"deeply nested scripted_metric": `{
			"aggs": {
				"outer": {
					"aggs": {
						"profit": {
							"scripted_metric": {
								"init_script":    {"id": "init"},
								"map_script":     {"id": "map", "params": {"x": 1}},
								"combine_script": {"id": "combine"},
								"reduce_script":  {"id": "reduce", "params": {"k": "v"}}
							}
						}
					}
				}
			}
		}`,
		"bucket_script and bucket_selector stored": `{
			"aggs": {
				"ratio": {
					"bucket_script": {
						"buckets_path": {"a": "sum_a", "b": "sum_b"},
						"script": {"id": "ratio-script", "params": {"scale": 100}}
					}
				},
				"big_only": {
					"bucket_selector": {
						"buckets_path": {"v": "sum_a"},
						"script": {"id": "threshold"}
					}
				}
			}
		}`,
		"runtime_mappings stored": `{
			"runtime_mappings": {
				"day_of_week": {
					"type": "keyword",
					"script": {"id": "dow", "params": {"tz": "Asia/Shanghai"}}
				},
				"counter": {"type": "long"}
			}
		}`,
		"script_score wrapper with stored script": `{
			"query": {
				"function_score": {
					"query": {"match_all": {}},
					"script_score": {
						"script": {"id": "score-fn", "params": {"w": 2}}
					}
				}
			}
		}`,
		"no scripts at all": `{
			"query": {"bool": {"must": [{"term": {"a": 1}}]}},
			"aggs": {"by_host": {"terms": {"field": "host"}}}
		}`,
		"params may contain script-like keys": `{
			"script": {"id": "s", "params": {"source": "ignored", "lang": "painless", "nested": {"inline": "x"}}}
		}`,
	}

	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateQuery([]byte(body)); err != nil {
				t.Fatalf("expected allow, got %v", err)
			}
		})
	}
}

func TestValidateQueryRejectsInlineScripts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"string script": `{
			"script_fields": {"f": {"script": "doc['price'].value * 2"}}
		}`,
		"source field": `{
			"script": {"source": "doc['price'].value", "params": {"x": 1}}
		}`,
		"inline field": `{
			"script": {"inline": "1+1", "lang": "painless"}
		}`,
		"lang only forbidden": `{
			"script": {"id": "s", "lang": "painless"}
		}`,
		"missing id": `{
			"script": {"params": {"x": 1}}
		}`,
		"empty id": `{
			"script": {"id": ""}
		}`,
		"non-string id": `{
			"script": {"id": 123}
		}`,
		"empty script object": `{"script": {}}`,
		"null script":         `{"script": null}`,
		"unexpected field":    `{"script": {"id": "s", "bogus": 1}}`,
		"deeply buried string script in bool query": `{
			"query": {"bool": {"filter": [
				{"nested": {"path": "events", "query": {
					"bool": {"must": [
						{"script": {"source": "malicious payload"}}
					]}
				}}}
			]}}
		}`,
		"scripted_metric inline reduce": `{
			"aggs": {"p": {"scripted_metric": {
				"init_script":    {"id": "init"},
				"map_script":     {"id": "map"},
				"combine_script": {"id": "combine"},
				"reduce_script":  {"source": "states.toString()"}
			}}}
		}`,
		"scripted_metric string map": `{
			"aggs": {"p": {"scripted_metric": {"map_script": "1+1"}}}
		}`,
		"bucket_script inline": `{
			"aggs": {"r": {"bucket_script": {
				"buckets_path": {"a": "a"},
				"script": {"source": "params.a"}
			}}}
		}`,
		"bucket_selector string": `{
			"aggs": {"r": {"bucket_selector": {
				"buckets_path": {"v": "v"},
				"script": "params.v > 0"
			}}}
		}`,
		"runtime_mappings inline": `{
			"runtime_mappings": {
				"f": {"type": "long", "script": {"source": "emit(1)", "lang": "painless"}}
			}
		}`,
		"runtime_mappings string": `{
			"runtime_mappings": {"f": {"type": "long", "script": "emit(1)"}}
		}`,
		"script_score direct inline": `{
			"query": {"function_score": {"script_score": {"source": "_score * 2"}}}
		}`,
		"script_score direct string": `{
			"query": {"function_score": {"script_score": "_score"}}
		}`,
		"script_score wrapper inline": `{
			"query": {"function_score": {"script_score": {"script": {"inline": "x"}}}}
		}`,
		"script in array": `{
			"query": {"bool": {"must": [
				{"term": {"a": 1}},
				{"script": {"source": "sneaky"}}
			]}}
		}`,
	}

	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := ValidateQueryString(body)
			if err == nil {
				t.Fatalf("expected rejection, body was allowed")
			}
			if !errors.Is(err, ErrInlineScript) && !errors.Is(err, ErrUnsafeScript) {
				t.Fatalf("expected script gate error, got %v", err)
			}
		})
	}
}

func TestValidateQueryRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"top level duplicate": `{"query": {"match_all": {}}, "query": {"term": {"x": 1}}}`,
		"nested duplicate": `{
			"query": {"bool": {"must": [{"term": {"a": 1}}], "must": [{"term": {"b": 2}}]}}
		}`,
		"duplicate inside array object": `{
			"query": {"bool": {"must": [{"term": {"a": 1, "a": 2}}]}}
		}`,
		"duplicate runtime field key": `{
			"runtime_mappings": {"f": {"type": "long", "type": "keyword"}}
		}`,
		"duplicate after nested container": `{
			"aggs": {"x": {"terms": {"field": "h"}}, "x": {"terms": {"field": "k"}}}
		}`,
	}

	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := ValidateQueryString(body)
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("expected ErrDuplicateKey, got %v", err)
			}
		})
	}
}

func TestValidateQueryRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty body":      ``,
		"truncated":       `{"query": {`,
		"trailing value":  `{"a": 1} {"b": 2}`,
		"trailing tokens": `{"a": 1} garbage`,
	}

	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateQueryString(body); err == nil {
				t.Fatalf("expected error for malformed JSON")
			}
		})
	}
}

func TestErrorPathPinpointsLocation(t *testing.T) {
	t.Parallel()

	err := ValidateQueryString(`{
		"aggs": {"p": {"scripted_metric": {"reduce_script": {"source": "x"}}}}
	}`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInlineScript) {
		t.Fatalf("expected ErrInlineScript, got %v", err)
	}
	if !strings.Contains(err.Error(), "reduce_script") {
		t.Fatalf("error should mention offending field path, got %v", err)
	}
}
