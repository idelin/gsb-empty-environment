package condbuilder

import (
	"strings"
	"testing"
)

func testWhitelist(t *testing.T) *Whitelist {
	t.Helper()
	wl, err := NewWhitelist("id", "users.name", "email", "status", "created_at", "secret")
	if err != nil {
		t.Fatalf("NewWhitelist: %v", err)
	}
	return wl
}

func TestWhitelistRejectsMaliciousIdentifiers(t *testing.T) {
	bad := []string{
		"",
		"1id",
		"name--",
		"name; DROP TABLE users",
		"name ASC, secret",
		"`name`",
		"a.b.c",
		"name) UNION SELECT password FROM users--",
	}
	for _, field := range bad {
		if _, err := NewWhitelist(field); err == nil {
			t.Errorf("NewWhitelist(%q) expected error, got nil", field)
		}
	}

	wl := testWhitelist(t)
	// External values must never be accepted as identifiers at render time.
	injected := []string{
		"id; DROP TABLE users",
		"id DESC, secret",
		"(SELECT password FROM users)",
		"id--",
		" id ",
		"ID",
	}
	for _, field := range injected {
		if _, err := wl.render(field); err == nil {
			t.Errorf("render(%q) expected rejection", field)
		}
	}
}

func TestLikeEscaping(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"100%":           `100\%`,
		"a_b":            `a\_b`,
		`back\slash`:     `back\\slash`,
		`%_\`:            `\%\_\\`,
		"; DROP TABLE x": "; DROP TABLE x",
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}

	f := Contains("email", "a_b%")
	sql, args, err := f.render(testWhitelist(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(sql, "ESCAPE '\\'") {
		t.Errorf("LIKE predicate missing ESCAPE clause: %s", sql)
	}
	if got := args[0].(string); got != `%a\_b\%%` {
		t.Errorf("bound LIKE arg = %q", got)
	}
}

func TestSelectBuildsParameterizedSQL(t *testing.T) {
	wl := testWhitelist(t)
	sel, err := NewSelect(wl, "users", []string{"id", "users.name", "email"})
	if err != nil {
		t.Fatalf("NewSelect: %v", err)
	}
	sel.
		Where(Contains("email", "o'reilly")).
		Where(In("status", []any{"active", "pending"})).
		OrderBy(ByDesc("created_at"), By("id")).
		Limit(10)
	sql, args, err := sel.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, want := range []string{
		"SELECT `id`, `users`.`name`, `email` FROM `users`",
		"WHERE `email` LIKE ? ESCAPE '\\' AND `status` IN (?, ?)",
		"ORDER BY `created_at` DESC, `id` ASC",
		"LIMIT 10",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL missing %q\nfull: %s", want, sql)
		}
	}
	// User text lives only in bind args, never in the SQL text.
	if strings.Contains(sql, "o'reilly") || strings.Contains(sql, "active") {
		t.Errorf("external value leaked into SQL text: %s", sql)
	}
	wantArgs := []any{"%o'reilly%", "active", "pending"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("arg[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}
}

func TestEmptyAndNilBatchesRejected(t *testing.T) {
	wl := testWhitelist(t)
	sel, _ := NewSelect(wl, "users", []string{"id"})

	if _, _, err := sel.Where(In("status", []any{})).Build(); err == nil {
		t.Error("empty IN batch must be rejected")
	}
	sel2, err := NewSelect(wl, "users", []string{"id"})
	if err != nil {
		t.Fatalf("NewSelect: %v", err)
	}
	if _, _, err := sel2.Where(In("status", []any{nil})).Build(); err == nil {
		t.Error("nil IN element must be rejected")
	}
}

func TestOrderByInjectionRejected(t *testing.T) {
	wl := testWhitelist(t)

	injectedOrders := []Order{
		{Field: "id; DROP TABLE users", Dir: Asc},
		{Field: "id), (password", Dir: Asc},
		{Field: "id", Dir: "ASC; DROP TABLE users"},
		{Field: "id", Dir: "DOWN"},
	}
	for _, o := range injectedOrders {
		if _, err := o.render(wl); err == nil {
			t.Errorf("order %+v expected rejection", o)
		}
	}

	// Non-whitelisted sort field on the builder itself.
	proj, err := NewSelect(wl, "users", []string{"id"})
	if err != nil {
		t.Fatalf("NewSelect: %v", err)
	}
	if _, _, err := proj.
		OrderBy(Order{Field: "password", Dir: Asc}).Build(); err == nil {
		t.Error("ORDER BY on non-whitelisted field must fail")
	}
}

func TestStructuralValidation(t *testing.T) {
	good := []string{
		"SELECT `id` FROM `users` WHERE `id` = ?",
		"SELECT `id`, `name` FROM `users` WHERE `status` IN (?, ?)",
		"(`a` LIKE ? ESCAPE '\\') UNION (`b` LIKE ? ESCAPE '\\')",
	}
	for _, q := range good {
		if err := ValidateStructure(q); err != nil {
			t.Errorf("ValidateStructure(%q) unexpected error: %v", q, err)
		}
	}
	bad := []string{
		"SELECT `id` FROM `users`; DROP TABLE `users`",
		"DELETE FROM `users`",
		"SELECT `id` FROM `users` WHERE (`id` = ?",
		"SELECT `id` FROM `users` WHERE `id` = ?)",
		"SELECT * FROM `users` WHERE x = 'unterminated",
		"SELECT `id` FROM `users` WHERE `id` = 1 -- attempt to comment out the rest",
		"SELECT `id` FROM `users` WHERE `id` = 1; SELECT password FROM creds",
	}
	for _, q := range bad {
		if err := ValidateStructure(q); err == nil {
			t.Errorf("ValidateStructure(%q) expected error", q)
		}
	}
}

func TestUnionHappyPath(t *testing.T) {
	wl := testWhitelist(t)
	left, err := NewSelect(wl, "users", []string{"id", "email"})
	if err != nil {
		t.Fatalf("left: %v", err)
	}
	left.Where(OpFilter("status", OpEq, "active"))

	right, err := NewSelect(wl, "admins", []string{"id", "email"})
	if err != nil {
		t.Fatalf("right: %v", err)
	}
	right.Where(OpFilter("status", OpEq, "super"))

	union, err := NewUnion(left, UnionAll)
	if err != nil {
		t.Fatalf("NewUnion: %v", err)
	}
	if _, err := union.Add(right); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := union.OrderBy(ByDesc("email"), By("id")); err != nil {
		t.Fatalf("OrderBy: %v", err)
	}
	if _, err := union.Limit(20); err != nil {
		t.Fatalf("Limit: %v", err)
	}

	sql, args, err := union.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{
		"(SELECT `id`, `email` FROM `users` WHERE `status` = ?)",
		"UNION ALL",
		"(SELECT `id`, `email` FROM `admins` WHERE `status` = ?)",
		"ORDER BY `email` DESC, `id` ASC",
		"LIMIT 20",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("union SQL missing %q\nfull: %s", want, sql)
		}
	}
	if len(args) != 2 || args[0] != "active" || args[1] != "super" {
		t.Errorf("union args = %v", args)
	}
}

func TestUnionBypassAttemptsRejected(t *testing.T) {
	wl := testWhitelist(t)

	t.Run("different whitelist instance", func(t *testing.T) {
		other, err := NewWhitelist("id", "email", "password")
		if err != nil {
			t.Fatalf("other whitelist: %v", err)
		}
		left, _ := NewSelect(wl, "users", []string{"id", "email"})
		rogue, err := NewSelect(other, "users", []string{"id", "email"})
		if err != nil {
			t.Fatalf("rogue select: %v", err)
		}
		union, err := NewUnion(left, UnionDistinct)
		if err != nil {
			t.Fatalf("NewUnion: %v", err)
		}
		if _, err := union.Add(rogue); err == nil {
			t.Error("branch with a different whitelist must be rejected")
		}
	})

	t.Run("mismatched columns", func(t *testing.T) {
		left, _ := NewSelect(wl, "users", []string{"id", "email"})
		right, _ := NewSelect(wl, "users", []string{"id", "status"})
		union, _ := NewUnion(left, UnionDistinct)
		if _, err := union.Add(right); err == nil {
			t.Error("branch with different projected columns must be rejected")
		}
	})

	t.Run("order by column outside result set", func(t *testing.T) {
		left, _ := NewSelect(wl, "users", []string{"id", "email"})
		right, _ := NewSelect(wl, "admins", []string{"id", "email"})
		union, _ := NewUnion(left, UnionDistinct)
		if _, err := union.Add(right); err != nil {
			t.Fatalf("Add: %v", err)
		}
		// secret exists in the whitelist/table but is not projected.
		if _, err := union.OrderBy(By("secret")); err == nil {
			t.Error("ORDER BY a column absent from the result set must be rejected")
		}
		// Fully injected sort key must fail as well.
		if _, err := union.OrderBy(Order{Field: "email DESC, secret", Dir: Asc}); err == nil {
			t.Error("injected union ORDER BY must be rejected")
		}
	})

	t.Run("per-branch order by rejected", func(t *testing.T) {
		left, _ := NewSelect(wl, "users", []string{"id", "email"})
		right, _ := NewSelect(wl, "admins", []string{"id", "email"})
		right.OrderBy(By("id"))
		union, _ := NewUnion(left, UnionDistinct)
		if _, err := union.Add(right); err == nil {
			t.Error("per-branch ORDER BY in a UNION must be rejected")
		}
	})
}

func TestUnknownFilterFieldRejected(t *testing.T) {
	wl := testWhitelist(t)
	sel, _ := NewSelect(wl, "users", []string{"id"})
	_, _, err := sel.Where(Filter{
		Field: "id = 1 OR 1=1 --",
		Op:    OpEq,
		Value: 1,
	}).Build()
	if err == nil {
		t.Error("filter with injected field name must be rejected")
	}
}
