package relalg

import "testing"

const sampleSQL = "SELECT u.name FROM users u JOIN orders o ON u.id = o.uid WHERE u.age > 18"

func TestParseSelect(t *testing.T) {
	stmt, err := ParseSelect(sampleSQL)
	if err != nil {
		t.Fatalf("ParseSelect: %v", err)
	}
	if len(stmt.Projection) != 1 {
		t.Fatalf("projection len = %d, want 1", len(stmt.Projection))
	}
	col, ok := stmt.Projection[0].(*ColumnRef)
	if !ok || col.Table != "u" || col.Name != "name" {
		t.Fatalf("projection[0] = %v, want u.name", stmt.Projection[0])
	}
	if stmt.From.Name != "users" || stmt.From.Alias != "u" {
		t.Fatalf("from = %s AS %s, want users AS u", stmt.From.Name, stmt.From.Alias)
	}
	if len(stmt.From.Joins) != 1 {
		t.Fatalf("joins len = %d, want 1", len(stmt.From.Joins))
	}
	j := stmt.From.Joins[0]
	if j.Kind != "INNER" || j.Table.Name != "orders" || j.Table.Alias != "o" {
		t.Fatalf("join = %s %s AS %s, want INNER orders AS o", j.Kind, j.Table.Name, j.Table.Alias)
	}
	if got := j.On.String(); got != "u.id = o.uid" {
		t.Fatalf("join on = %q, want %q", got, "u.id = o.uid")
	}
	if got := stmt.Where.String(); got != "u.age > 18" {
		t.Fatalf("where = %q, want %q", got, "u.age > 18")
	}
}

func TestParsePrecedence(t *testing.T) {
	stmt, err := ParseSelect("SELECT a.x FROM t a WHERE a.x = 1 OR a.y = 2 AND a.z = 3")
	if err != nil {
		t.Fatalf("ParseSelect: %v", err)
	}
	want := "a.x = 1 OR a.y = 2 AND a.z = 3"
	if got := stmt.Where.String(); got != want {
		t.Fatalf("where = %q, want %q", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, sql := range []string{
		"SELECT FROM t",
		"SELECT a FROM",
		"SELECT a FROM t WHERE",
		"SELECT a FROM t JOIN u",
	} {
		if _, err := ParseSelect(sql); err == nil {
			t.Errorf("ParseSelect(%q) succeeded, want error", sql)
		}
	}
}
