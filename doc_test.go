package enumkit

import (
	"errors"
	"strings"
	"testing"
)

const sampleDoc = `{
  "spec": {
    "mode": "RED",
    "items": [
      {"mode": "RED"},
      {"mode": "LIGHT"}
    ]
  }
}`

func TestDecodeJSONFieldPaths(t *testing.T) {
	r := newColorEnum(t)
	vals, errs := DecodeJSON([]byte(sampleDoc), "v3", []Field{{Path: "spec.items.*.mode", Reg: r}})
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if vals["spec.items[0].mode"].ID != 1 {
		t.Fatalf("item0 = %+v", vals["spec.items[0].mode"])
	}
	// GREEN-less LIGHT at v3 is unknown and passes through.
	if vals["spec.items[1].mode"].Known() || vals["spec.items[1].mode"].Raw != "LIGHT" {
		t.Fatalf("item1 = %+v", vals["spec.items[1].mode"])
	}
}

func TestMigrateJSONWithAmbiguityPath(t *testing.T) {
	r := New("lamp")
	must(t, r.Define(4, "AMBER"), "def4")
	must(t, r.Define(5, "VIOLET"), "def5")
	must(t, r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 4}},
	}), "v1")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 5}},
	}), "v2")
	_, errs := MigrateJSON([]byte(sampleDoc), "v1", "v2", []Field{{Path: "spec.items.*.mode", Reg: r}})
	var pe *PathError
	var ae *AmbiguityError
	found := false
	for _, e := range errs {
		if errors.As(e, &pe) && errors.As(pe, &ae) {
			if pe.Path == "spec.items[1].mode" && strings.Contains(ae.Error(), "LIGHT") && len(ae.Chain) > 0 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected PathError at spec.items[1].mode wrapping AmbiguityError with chain, got %#v", errs)
	}
}

func TestMigrateJSONRewritesCanonical(t *testing.T) {
	r := newColorEnum(t)
	doc := `{"spec":{"mode":"RED","items":[{"mode":"GREEN"},{"mode":"MYSTERY"}]}}`
	out, errs := MigrateJSON([]byte(doc), "v1", "v3", []Field{{Path: "spec.mode", Reg: r}, {Path: "spec.items.*.mode", Reg: r}})
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	got := string(out)
	if !strings.Contains(got, `"CRIMSON"`) || !strings.Contains(got, `"EMERALD"`) {
		t.Fatalf("canonical names missing: %s", got)
	}
	if !strings.Contains(got, `"MYSTERY"`) {
		t.Fatalf("unknown token not passed through: %s", got)
	}
}
