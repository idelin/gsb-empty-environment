package merge

import (
	"reflect"
	"strings"
	"testing"
)

// flatLeaves renders the result in the path-expanded model:
// "path = value @layerN <- (layerM: old), ...".
func flatLeaves(t *testing.T, r *Result) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, l := range r.Leaves() {
		out[l.Path] = leafString(l)
	}
	return out
}

func leafString(l Leaf) string {
	var b strings.Builder
	b.WriteString(render(l.Value))
	b.WriteString(" @layer")
	b.WriteString(itoa(l.Layer))
	for _, h := range l.History {
		b.WriteString(" <- (layer")
		b.WriteString(itoa(h.Layer))
		b.WriteString(": ")
		b.WriteString(render(h.Value))
		b.WriteString(")")
	}
	return b.String()
}

func render(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case int:
		return itoa(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []interface{}:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = render(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return "?"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func expectLeaves(t *testing.T, r *Result, want map[string]string) {
	t.Helper()
	got := flatLeaves(t, r)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("leaves mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestObjectsMergeRecursivelyAndScalarsOverride(t *testing.T) {
	r := Merge(
		map[string]interface{}{
			"server": map[string]interface{}{
				"host": "a.internal",
				"port": 80,
				"tls":  map[string]interface{}{"enabled": false},
			},
			"replicas": 1,
		},
		map[string]interface{}{
			"server": map[string]interface{}{
				"port": 8080,
				"tls":  map[string]interface{}{"enabled": true},
			},
			"replicas": 3,
		},
	)
	expectLeaves(t, r, map[string]string{
		"server.host":        "a.internal @layer0",
		"server.port":        "8080 @layer1 <- (layer0: 80)",
		"server.tls.enabled": "true @layer1 <- (layer0: false)",
		"replicas":           "3 @layer1 <- (layer0: 1)",
	})
}

func TestArraysReplaceWholesale(t *testing.T) {
	r := Merge(
		map[string]interface{}{"tags": []interface{}{"a", "b", "c"}},
		map[string]interface{}{"tags": []interface{}{"z"}},
	)
	expectLeaves(t, r, map[string]string{
		"tags": "[z] @layer1 <- (layer0: [a,b,c])",
	})
}

func TestDeleteRemovesKeyButNullKeepsIt(t *testing.T) {
	r := Merge(
		map[string]interface{}{"drop": "x", "keep": "y"},
		map[string]interface{}{"drop": Delete, "keep": nil},
	)
	expectLeaves(t, r, map[string]string{
		"keep": "null @layer1 <- (layer0: y)",
	})
	if _, ok := r.Merged["drop"]; ok {
		t.Fatal("deleted key must not survive in merged map")
	}
	if v, ok := r.Merged["keep"]; !ok || v != nil {
		t.Fatal("null must keep the key with a nil value")
	}
}

func TestTypeSwitchObjectToScalarDropsSubtree(t *testing.T) {
	r := Merge(
		map[string]interface{}{
			"db": map[string]interface{}{"host": "h", "port": 5432},
		},
		map[string]interface{}{"db": "sqlite:mem"},
	)
	expectLeaves(t, r, map[string]string{
		"db": "sqlite:mem @layer1",
	})

	exp := r.Explain("db.host")
	if exp.Exists || exp.Reason != ReasonDroppedByParent || exp.ByPath != "db" || exp.ByLayer != 1 {
		t.Fatalf("unexpected explanation: %+v", exp)
	}
}

func TestTypeSwitchScalarToObject(t *testing.T) {
	r := Merge(
		map[string]interface{}{"mode": "simple"},
		map[string]interface{}{"mode": map[string]interface{}{"kind": "adv", "level": 2}},
	)
	expectLeaves(t, r, map[string]string{
		"mode.kind":  "adv @layer1",
		"mode.level": "2 @layer1",
	})
	rems := r.Removals("mode")
	if len(rems) != 1 || rems[0].Op != OpReplacedByChild || rems[0].Value != "simple" {
		t.Fatalf("expected archived replacement of scalar, got %+v", rems)
	}
}

func TestTypeSwitchArrayToObjectAndBack(t *testing.T) {
	r := Merge(
		map[string]interface{}{"net": []interface{}{"a", "b"}},
		map[string]interface{}{"net": map[string]interface{}{"cidr": "10.0.0.0/8"}},
		map[string]interface{}{"net": []interface{}{"x"}},
	)
	expectLeaves(t, r, map[string]string{
		"net": "[x] @layer2",
	})
	if rems := r.Removals("net.cidr"); len(rems) != 1 || rems[0].Op != OpDroppedByParent {
		t.Fatalf("expected cidr dropped by parent, got %+v", rems)
	}
}

func TestRepeatedDeleteIsIdempotent(t *testing.T) {
	r := Merge(
		map[string]interface{}{"tmp": 1, "other": 2},
		map[string]interface{}{"tmp": Delete},
		map[string]interface{}{"tmp": Delete}, // deleting again must be a no-op
	)
	expectLeaves(t, r, map[string]string{
		"other": "2 @layer0",
	})
	exp := r.Explain("tmp")
	if exp.Exists || exp.Reason != ReasonDeleted || exp.ByLayer != 1 {
		t.Fatalf("unexpected explanation: %+v", exp)
	}
}

func TestDeleteThenResetStartsFreshChain(t *testing.T) {
	r := Merge(
		map[string]interface{}{"k": "v0"},
		map[string]interface{}{"k": "v1"},
		map[string]interface{}{"k": Delete},
		map[string]interface{}{"k": "v3"},
	)
	// After deletion the key is gone, so the re-set value has no
	// overridden chain.
	expectLeaves(t, r, map[string]string{
		"k": "v3 @layer3",
	})
}

func TestParentObjectReplacedExplainsDescendants(t *testing.T) {
	r := Merge(
		map[string]interface{}{
			"app": map[string]interface{}{
				"db": map[string]interface{}{"host": "h", "port": 1},
			},
		},
		map[string]interface{}{"app": nil}, // null replaces the whole subtree
	)
	expectLeaves(t, r, map[string]string{
		"app": "null @layer1",
	})

	exp := r.Explain("app.db.host")
	if exp.Exists || exp.Reason != ReasonDroppedByParent ||
		exp.ByPath != "app" || exp.ByLayer != 1 {
		t.Fatalf("unexpected explanation: %+v", exp)
	}
	if rems := r.Removals("app.db.host"); len(rems) != 1 ||
		rems[0].Op != OpDroppedByParent || rems[0].Cause != "app" ||
		rems[0].SourceLayer != 0 {
		t.Fatalf("unexpected removal archive: %+v", rems)
	}
}

func TestExplainNeverSetAndDeleted(t *testing.T) {
	r := Merge(
		map[string]interface{}{"gone": 1},
		map[string]interface{}{"gone": Delete},
	)

	never := r.Explain("missing.path")
	if never.Exists || never.Reason != ReasonNeverSet {
		t.Fatalf("unexpected explanation: %+v", never)
	}

	deleted := r.Explain("gone")
	if deleted.Exists || deleted.Reason != ReasonDeleted || deleted.ByLayer != 1 {
		t.Fatalf("unexpected explanation: %+v", deleted)
	}
	if !strings.Contains(deleted.String(), "deleted at layer 1") {
		t.Fatalf("unhelpful message: %q", deleted.String())
	}
}

func TestExplainObjectPath(t *testing.T) {
	r := Merge(map[string]interface{}{
		"a": map[string]interface{}{"b": 1, "c": 2},
	})
	exp := r.Explain("a")
	if !exp.Exists || exp.Leaf != nil || exp.ObjectLeaves != 2 {
		t.Fatalf("unexpected explanation: %+v", exp)
	}
}

func TestProvenanceChainAcrossManyLayers(t *testing.T) {
	r := Merge(
		map[string]interface{}{"image": "app:1"},
		map[string]interface{}{"image": "app:2"},
		map[string]interface{}{"image": "app:3"},
	)
	leaf, ok := r.Leaf("image")
	if !ok {
		t.Fatal("image must exist")
	}
	if leaf.Layer != 2 || leaf.Value != "app:3" {
		t.Fatalf("bad final source: %+v", leaf)
	}
	want := []Change{{Layer: 0, Value: "app:1"}, {Layer: 1, Value: "app:2"}}
	if !reflect.DeepEqual(leaf.History, want) {
		t.Fatalf("bad overridden chain: %+v", leaf.History)
	}
}
