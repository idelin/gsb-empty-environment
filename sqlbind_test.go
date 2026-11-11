package sqlbind

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBindScalars(t *testing.T) {
	got, values, err := Bind(
		"SELECT * FROM t WHERE a = :a AND b = :b",
		map[string]any{"a": 1, "b": "x"},
	)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := "SELECT * FROM t WHERE a = $1 AND b = $2"
	if got != want {
		t.Errorf("sql = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(values, []any{1, "x"}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindIgnoresCommentsAndStrings(t *testing.T) {
	query := `SELECT ':str', ":ident", E':esc\':still', col::int
/* :block /* :nested */ :still_block */
-- :line
FROM t WHERE a = :real AND b = $$ :dollar $$ AND c = $tag$ :tagged $tag$`
	got, values, err := Bind(query, map[string]any{"real": 7})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if strings.Count(got, ":") != strings.Count(query, ":")-1 {
		t.Errorf("only :real should be rewritten:\n%s", got)
	}
	if !strings.Contains(got, "a = $1") {
		t.Errorf("missing rebound placeholder:\n%s", got)
	}
	if !reflect.DeepEqual(values, []any{7}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindRepeatedNamedParam(t *testing.T) {
	got, values, err := Bind(
		"SELECT * FROM t WHERE a = :x OR b = :x OR c = :y",
		map[string]any{"x": 1, "y": 2},
	)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := "SELECT * FROM t WHERE a = $1 OR b = $1 OR c = $2"
	if got != want {
		t.Errorf("sql = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(values, []any{1, 2}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindSliceExpansion(t *testing.T) {
	got, values, err := Bind(
		"SELECT * FROM t WHERE id IN :ids AND name IN :names AND tag IN :tags OR other IN :ids",
		map[string]any{
			"ids":   []int{1, 2, 3},
			"names": []string{"a"},
			"tags":  []int{},
		},
	)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := "SELECT * FROM t WHERE id IN ($1,$2,$3) AND name IN ($4) AND tag IN (NULL) OR other IN ($1,$2,$3)"
	if got != want {
		t.Errorf("sql = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(values, []any{1, 2, 3, "a"}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindByteSliceIsScalar(t *testing.T) {
	blob := []byte{0xDE, 0xAD}
	got, values, err := Bind("SELECT :blob", map[string]any{"blob": blob})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got != "SELECT $1" {
		t.Errorf("sql = %q", got)
	}
	if !reflect.DeepEqual(values, []any{blob}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindContinuesAfterExistingPositional(t *testing.T) {
	got, values, err := Bind(
		"SELECT * FROM t WHERE a = $2 AND b = :x",
		map[string]any{"x": 9},
	)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := "SELECT * FROM t WHERE a = $2 AND b = $3"
	if got != want {
		t.Errorf("sql = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(values, []any{9}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindNeverInterpolatesValues(t *testing.T) {
	hostile := "x'); DROP TABLE users; --"
	got, values, err := Bind("SELECT * FROM t WHERE a = :a", map[string]any{"a": hostile})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if strings.Contains(got, hostile) || strings.Contains(got, "DROP") {
		t.Errorf("value leaked into sql: %q", got)
	}
	if !reflect.DeepEqual(values, []any{hostile}) {
		t.Errorf("values = %v", values)
	}
}

func TestBindUnclosedStatements(t *testing.T) {
	cases := map[string]string{
		"string literal":      "SELECT 'abc",
		"escaped string":      "SELECT E'a\\'",
		"quoted identifier":   `SELECT "col`,
		"block comment":       "SELECT 1 /* outer /* inner */ still open",
		"dollar-quoted empty": "SELECT $$ body",
		"dollar-quoted tag":   "SELECT $fn$ body $other$",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Bind(query, nil)
			var unclosed *UnclosedError
			if !errors.As(err, &unclosed) {
				t.Fatalf("err = %v, want *UnclosedError", err)
			}
		})
	}
}

func TestBindMissingArg(t *testing.T) {
	_, _, err := Bind("SELECT :nope", map[string]any{})
	var missing *MissingArgError
	if !errors.As(err, &missing) || missing.Name != "nope" {
		t.Fatalf("err = %v, want *MissingArgError for :nope", err)
	}
}
