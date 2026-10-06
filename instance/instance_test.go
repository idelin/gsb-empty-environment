package instance

import (
	"strings"
	"testing"
)

func parse(t *testing.T, doc string) *Instance {
	t.Helper()
	inst, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return inst
}

func TestValidateOK(t *testing.T) {
	parse(t, `{
	  "variables": [{"name": "a", "domain": ["0", "1"]}],
	  "constraints": [{"name": "c", "vars": ["a"], "allowed": [["0"]]}]
	}`)
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]string{
		"duplicate variable": `{"variables": [{"name": "a", "domain": ["0"]}, {"name": "a", "domain": ["1"]}]}`,
		"empty domain":       `{"variables": [{"name": "a", "domain": []}]}`,
		"duplicate value":    `{"variables": [{"name": "a", "domain": ["0", "0"]}]}`,
		"unknown variable":   `{"variables": [{"name": "a", "domain": ["0"]}], "constraints": [{"name": "c", "vars": ["b"], "allowed": []}]}`,
		"duplicate constraint": `{"variables": [{"name": "a", "domain": ["0"]}], "constraints": [
		  {"name": "c", "vars": ["a"], "allowed": []},
		  {"name": "c", "vars": ["a"], "allowed": []}]}`,
		"bad arity": `{"variables": [{"name": "a", "domain": ["0"]}], "constraints": [
		  {"name": "c", "vars": ["a"], "allowed": [["0", "0"]]}]}`,
		"value outside domain": `{"variables": [{"name": "a", "domain": ["0"]}], "constraints": [
		  {"name": "c", "vars": ["a"], "allowed": [["1"]]}]}`,
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestSatisfies(t *testing.T) {
	inst := parse(t, `{
	  "variables": [
	    {"name": "a", "domain": ["0", "1"]},
	    {"name": "b", "domain": ["0", "1"]}
	  ],
	  "constraints": [
	    {"name": "eq", "vars": ["a", "b"], "allowed": [["0", "0"], ["1", "1"]]}
	  ]
	}`)
	c := Compile(inst)
	if !c.Satisfies(0, Assignment{"a": "1", "b": "1"}) {
		t.Error("a=1,b=1 should satisfy eq")
	}
	if c.Satisfies(0, Assignment{"a": "1", "b": "0"}) {
		t.Error("a=1,b=0 should not satisfy eq")
	}
}

func TestScopeDeclarationOrder(t *testing.T) {
	inst := parse(t, `{
	  "variables": [
	    {"name": "z", "domain": ["0"]},
	    {"name": "a", "domain": ["0"]},
	    {"name": "m", "domain": ["0"]}
	  ],
	  "constraints": [
	    {"name": "c1", "vars": ["m", "z"], "allowed": [["0", "0"]]}
	  ]
	}`)
	c := Compile(inst)
	scope := c.Scope([]int{0})
	if len(scope) != 2 || scope[0] != 0 || scope[1] != 2 {
		t.Errorf("scope = %v, want [0 2] (declaration order)", scope)
	}
}

func TestTupleKeyUnambiguous(t *testing.T) {
	if TupleKey([]string{"a", "b"}) == TupleKey([]string{"a b"}) {
		t.Error("tuple keys collide")
	}
	if !strings.Contains(TupleKey([]string{"x"}), `"x"`) {
		t.Error("tuple key should quote values")
	}
}
