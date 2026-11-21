package tenantfilter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func testGuard(t *testing.T) *Guard {
	t.Helper()
	guard, err := NewGuard(Config{})
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func testContext(tenant any) context.Context {
	return WithTenant(context.Background(), tenant)
}

func normalizeSQL(sql string) string {
	normalized := strings.Join(strings.Fields(sql), " ")
	for _, replacement := range [][2]string{
		{" )", ")"},
		{"( ", "("},
		{" ,", ","},
		{" ;", ";"},
	} {
		normalized = strings.ReplaceAll(normalized, replacement[0], replacement[1])
	}
	return normalized
}

func rewriteForTest(t *testing.T, guard *Guard, tenant any, query string, args ...any) (string, []any) {
	t.Helper()
	rewritten, rewrittenArgs, err := guard.Rewrite(testContext(tenant), query, args)
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", query, err)
	}
	return normalizeSQL(rewritten), rewrittenArgs
}

func assertSQL(t *testing.T, guard *Guard, tenant any, query string, args []any, wantSQL string, wantArgs []any) {
	t.Helper()
	gotSQL, gotArgs := rewriteForTest(t, guard, tenant, query, args...)
	if gotSQL != normalizeSQL(wantSQL) {
		t.Fatalf("SQL mismatch:\n got: %s\nwant: %s", gotSQL, normalizeSQL(wantSQL))
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", gotArgs, wantArgs)
	}
}

func TestRewriteBasicSelect(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a", "SELECT * FROM orders", nil,
		"SELECT * FROM orders WHERE (orders.tenant_id = ?)",
		[]any{"tenant-a"})

	assertSQL(t, guard, "tenant-a", "SELECT o.id FROM orders AS o WHERE o.status = ?", []any{"open"},
		"SELECT o.id FROM orders AS o WHERE ( o.status = ?) AND (o.tenant_id = ?)",
		[]any{"open", "tenant-a"})
}

func TestRewriteCorrelatedSubquery(t *testing.T) {
	guard := testGuard(t)
	query := `SELECT o.id
FROM orders o
WHERE EXISTS (
  SELECT 1 FROM payments p WHERE p.order_id = o.id AND p.amount > ?
)`
	assertSQL(t, guard, "tenant-a", query, []any{10},
		`SELECT o.id FROM orders o WHERE ( EXISTS (
  SELECT 1 FROM payments p WHERE ( p.order_id = o.id AND p.amount > ?) AND (p.tenant_id = ?)
) ) AND (o.tenant_id = ?)`,
		[]any{10, "tenant-a", "tenant-a"})
}

func TestRewriteAggregationAndPagination(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a",
		"SELECT tenant_id, COUNT(*) FROM orders WHERE status = ? GROUP BY tenant_id HAVING COUNT(*) > ?",
		[]any{"open", 1},
		"SELECT tenant_id, COUNT(*) FROM orders WHERE ( status = ?) AND (orders.tenant_id = ?) GROUP BY tenant_id HAVING COUNT(*) > ?",
		[]any{"open", "tenant-a", 1})

	assertSQL(t, guard, "tenant-a",
		"SELECT id FROM orders ORDER BY id LIMIT ? OFFSET ?",
		[]any{10, 5},
		"SELECT id FROM orders WHERE (orders.tenant_id = ?) ORDER BY id LIMIT ? OFFSET ?",
		[]any{"tenant-a", 10, 5})
}

func TestRewriteSetOperationAndCTE(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a",
		"SELECT id FROM orders UNION ALL SELECT order_id FROM payments",
		nil,
		"SELECT id FROM orders WHERE (orders.tenant_id = ?) UNION ALL SELECT order_id FROM payments WHERE (payments.tenant_id = ?)",
		[]any{"tenant-a", "tenant-a"})

	assertSQL(t, guard, "tenant-a",
		"WITH recent AS (SELECT * FROM orders WHERE created_at > ?) SELECT * FROM recent",
		[]any{100},
		"WITH recent AS (SELECT * FROM orders WHERE ( created_at > ?) AND (orders.tenant_id = ?)) SELECT * FROM recent",
		[]any{100, "tenant-a"})
}

func TestRewriteCommaAndStraightJoins(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a",
		"SELECT * FROM orders o, payments p WHERE o.id = p.order_id",
		nil,
		"SELECT * FROM orders o, payments p WHERE ( o.id = p.order_id) AND (o.tenant_id = ? AND p.tenant_id = ?)",
		[]any{"tenant-a", "tenant-a"})
	assertSQL(t, guard, "tenant-a",
		"SELECT * FROM orders o STRAIGHT_JOIN payments p ON o.id = p.order_id",
		nil,
		"SELECT * FROM orders o STRAIGHT_JOIN payments p ON o.id = p.order_id WHERE (o.tenant_id = ? AND p.tenant_id = ?)",
		[]any{"tenant-a", "tenant-a"})
}

func TestQuotedAliasesWithDifferentCaseBothGetFiltered(t *testing.T) {
	guard := testGuard(t)
	query := `SELECT * FROM orders AS "A" JOIN payments AS "a" ON "A".id = "a".order_id`
	rewritten, args, err := guard.Rewrite(testContext("tenant-a"), query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, `"A".tenant_id = ?`) || !strings.Contains(rewritten, `"a".tenant_id = ?`) {
		t.Fatalf("both quoted aliases must be filtered: %q", rewritten)
	}
	if !reflect.DeepEqual(args, []any{"tenant-a", "tenant-a"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestQuotedTableNamesDoNotMatchUnquotedGlobalTables(t *testing.T) {
	guard, err := NewGuard(Config{GlobalTables: []string{"orders"}})
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, guard, "tenant-a",
		`SELECT * FROM "Orders"`,
		nil,
		`SELECT * FROM "Orders" WHERE ("Orders".tenant_id = ?)`,
		[]any{"tenant-a"})
	assertSQL(t, guard, "tenant-a",
		"SELECT * FROM orders",
		nil,
		"SELECT * FROM orders",
		[]any{})
}

func TestCTEShadowingDoesNotDisableOuterFilter(t *testing.T) {
	guard := testGuard(t)
	query := `SELECT *
FROM orders
WHERE EXISTS (
  WITH orders AS (SELECT 1 AS id)
  SELECT 1 FROM orders
)`
	rewritten, args, err := guard.Rewrite(testContext("tenant-a"), query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(rewritten, "tenant_id"); got != 1 {
		t.Fatalf("tenant predicates = %d, want 1 in %q", got, rewritten)
	}
	if !strings.Contains(rewritten, "orders.tenant_id") {
		t.Fatalf("outer physical table was not filtered: %q", rewritten)
	}
	if !reflect.DeepEqual(args, []any{"tenant-a"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestCTENameCaseUsesSQLIdentifierRules(t *testing.T) {
	guard := testGuard(t)

	rewritten, args, err := guard.Rewrite(testContext("tenant-a"),
		`WITH "Orders" AS (SELECT 1 AS id) SELECT * FROM orders`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "orders.tenant_id") || !reflect.DeepEqual(args, []any{"tenant-a"}) {
		t.Fatalf(`unquoted orders must not be shadowed by quoted "Orders": %q, %#v`, rewritten, args)
	}

	rewritten, args, err = guard.Rewrite(testContext("tenant-a"),
		`WITH Orders AS (SELECT 1 AS id) SELECT * FROM orders`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rewritten, "tenant_id") || len(args) != 0 {
		t.Fatalf("unquoted CTE should shadow unquoted orders: %q, %#v", rewritten, args)
	}
}

func TestNonRecursiveCTEIsNotVisibleBeforeItsDefinition(t *testing.T) {
	guard := testGuard(t)
	query := `WITH first_cte AS (SELECT * FROM shadowed),
     shadowed AS (SELECT * FROM orders)
SELECT * FROM first_cte`
	rewritten, args, err := guard.Rewrite(testContext("tenant-a"), query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(rewritten, "tenant_id"); got != 2 {
		t.Fatalf("tenant predicates = %d, want 2 in %q", got, rewritten)
	}
	if !strings.Contains(rewritten, "shadowed.tenant_id") {
		t.Fatalf("forward table reference was not treated as physical: %q", rewritten)
	}
	if !reflect.DeepEqual(args, []any{"tenant-a", "tenant-a"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestMultipleAndRecursiveCTEs(t *testing.T) {
	guard := testGuard(t)
	query := `WITH RECURSIVE
  order_tree AS (
    SELECT id FROM orders WHERE parent_id IS NULL AND id > ?
    UNION ALL
    SELECT o.id FROM orders o JOIN order_tree t ON o.parent_id = t.id
  ),
  recent_payments AS (
    SELECT * FROM payments WHERE created_at > ?
  )
SELECT * FROM order_tree JOIN recent_payments p ON p.order_id = order_tree.id
LIMIT ?`
	assertSQL(t, guard, "tenant-a", query, []any{10, 100, 5},
		`WITH RECURSIVE order_tree AS (
    SELECT id FROM orders WHERE ( parent_id IS NULL AND id > ?) AND (orders.tenant_id = ?)
    UNION ALL
    SELECT o.id FROM orders o JOIN order_tree t ON o.parent_id = t.id WHERE (o.tenant_id = ?)
  ), recent_payments AS (
    SELECT * FROM payments WHERE ( created_at > ?) AND (payments.tenant_id = ?)
  )
SELECT * FROM order_tree JOIN recent_payments p ON p.order_id = order_tree.id LIMIT ?`,
		[]any{10, "tenant-a", "tenant-a", 100, "tenant-a", 5})
}

func TestPlaceholderOrderingAcrossScalarSubquery(t *testing.T) {
	guard := testGuard(t)
	query := `SELECT o.id,
       (SELECT COUNT(*) FROM payments p WHERE p.order_id = o.id AND p.amount > ?) AS pc
FROM orders o
WHERE o.status = ?`
	assertSQL(t, guard, "tenant-a", query, []any{50, "open"},
		`SELECT o.id, (SELECT COUNT(*) FROM payments p WHERE ( p.order_id = o.id AND p.amount > ?) AND (p.tenant_id = ?)) AS pc FROM orders o WHERE ( o.status = ?) AND (o.tenant_id = ?)`,
		[]any{50, "tenant-a", "open", "tenant-a"})
}

func TestRejectsExplicitTenantOverride(t *testing.T) {
	guard := testGuard(t)
	cases := []string{
		"SELECT * FROM orders WHERE tenant_id = 'tenant-b'",
		"SELECT * FROM orders o WHERE o.tenant_id = ?",
		"SELECT * FROM orders o WHERE EXISTS (SELECT 1 FROM payments p WHERE p.tenant_id = 'tenant-b')",
		"SELECT (SELECT 1 WHERE o.tenant_id = 'tenant-b') FROM orders o",
		"SELECT tenant_id, COUNT(*) FROM orders GROUP BY tenant_id HAVING tenant_id = 'tenant-b'",
		"SELECT * FROM orders o JOIN payments p ON o.id = p.order_id AND p.tenant_id = 'tenant-b'",
	}
	for _, query := range cases {
		_, _, err := guard.Rewrite(testContext("tenant-a"), query, []any{"tenant-b"})
		if !errors.Is(err, ErrTenantOverride) {
			t.Errorf("Rewrite(%q) error = %v, want ErrTenantOverride", query, err)
		}
	}
}

func TestRejectsMultipleStatements(t *testing.T) {
	guard := testGuard(t)
	_, _, err := guard.Rewrite(testContext("tenant-a"), "SELECT * FROM orders; SELECT * FROM payments", nil)
	if !errors.Is(err, ErrMultipleStatements) {
		t.Fatalf("error = %v, want ErrMultipleStatements", err)
	}
}

func TestInjectionIsNotSwallowedByTrailingLineComment(t *testing.T) {
	guard := testGuard(t)
	rewritten, args, err := guard.Rewrite(testContext("tenant-a"),
		"SELECT * FROM orders -- keep this comment", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "\n WHERE (orders.tenant_id = ?)") {
		t.Fatalf("tenant predicate was not placed after the comment on a new line: %q", rewritten)
	}
	if !reflect.DeepEqual(args, []any{"tenant-a"}) {
		t.Fatalf("args = %#v", args)
	}

	rewritten, args, err = guard.Rewrite(testContext("tenant-a"),
		"SELECT * FROM orders WHERE id = ? -- keep this comment", []any{7})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "\n) AND (orders.tenant_id = ?)") {
		t.Fatalf("tenant predicate was swallowed by a trailing comment: %q", rewritten)
	}
	if !reflect.DeepEqual(args, []any{7, "tenant-a"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestRejectsExecutableOrAmbiguousCommentForms(t *testing.T) {
	guard := testGuard(t)
	for _, query := range []string{
		"SELECT * FROM orders # trailing mysql comment",
		"# leading mysql comment\nSELECT * FROM orders",
		"SELECT * FROM orders /*! UNION SELECT * FROM payments */",
		"SELECT * FROM orders /*M! UNION SELECT * FROM payments */",
	} {
		if _, _, err := guard.Rewrite(testContext("tenant-a"), query, nil); !errors.Is(err, ErrUnsupportedQuery) {
			t.Errorf("Rewrite(%q) error = %v, want ErrUnsupportedQuery", query, err)
		}
	}

}

func TestMissingTenant(t *testing.T) {
	guard := testGuard(t)
	_, _, err := guard.Rewrite(context.Background(), "SELECT * FROM orders", nil)
	if !errors.Is(err, ErrNoTenant) {
		t.Fatalf("error = %v, want ErrNoTenant", err)
	}
}

func TestTableOverridesAndGlobalTables(t *testing.T) {
	guard, err := NewGuard(Config{
		TableTenantColumns: map[string]string{"events": "account_id"},
		GlobalTables:       []string{"currencies"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSQL(t, guard, 42, "SELECT * FROM events e JOIN currencies c ON c.code = e.currency",
		nil,
		"SELECT * FROM events e JOIN currencies c ON c.code = e.currency WHERE (e.account_id = ?)",
		[]any{42})
}

func TestDollarPlaceholders(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a",
		"SELECT * FROM orders o WHERE o.status = $1 AND o.amount > $2",
		[]any{"open", 10},
		"SELECT * FROM orders o WHERE ( o.status = $1 AND o.amount > $2) AND (o.tenant_id = $3)",
		[]any{"open", 10, "tenant-a"})
}

func TestUpdateAndDelete(t *testing.T) {
	guard := testGuard(t)
	assertSQL(t, guard, "tenant-a",
		"UPDATE orders SET status = ? WHERE id = ?",
		[]any{"closed", 7},
		"UPDATE orders SET status = ? WHERE ( id = ?) AND (orders.tenant_id = ?)",
		[]any{"closed", 7, "tenant-a"})

	assertSQL(t, guard, "tenant-a",
		"DELETE FROM orders WHERE id = ?",
		[]any{7},
		"DELETE FROM orders WHERE ( id = ?) AND (orders.tenant_id = ?)",
		[]any{7, "tenant-a"})

	_, _, err := guard.Rewrite(testContext("tenant-a"), "UPDATE orders SET tenant_id = 'tenant-b' WHERE id = 1", nil)
	if !errors.Is(err, ErrTenantOverride) {
		t.Fatalf("error = %v, want ErrTenantOverride", err)
	}

	assertSQL(t, guard, "tenant-a",
		"UPDATE orders o SET status = ? FROM payments p WHERE o.id = p.order_id",
		[]any{"closed"},
		"UPDATE orders o SET status = ? FROM payments p WHERE ( o.id = p.order_id) AND (o.tenant_id = ? AND p.tenant_id = ?)",
		[]any{"closed", "tenant-a", "tenant-a"})

	assertSQL(t, guard, "tenant-a",
		"DELETE FROM orders o USING payments p WHERE o.id = p.order_id",
		nil,
		"DELETE FROM orders o USING payments p WHERE ( o.id = p.order_id) AND (o.tenant_id = ? AND p.tenant_id = ?)",
		[]any{"tenant-a", "tenant-a"})

	assertSQL(t, guard, "tenant-a",
		"UPDATE orders SET status = 'closed' RETURNING id",
		nil,
		"UPDATE orders SET status = 'closed' WHERE (orders.tenant_id = ?) RETURNING id",
		[]any{"tenant-a"})

	assertSQL(t, guard, "tenant-a",
		"DELETE FROM orders",
		nil,
		"DELETE FROM orders WHERE (orders.tenant_id = ?)",
		[]any{"tenant-a"})
}

func TestRejectsUnsupportedWrites(t *testing.T) {
	guard := testGuard(t)
	for _, query := range []string{
		"INSERT INTO orders (id) VALUES (1)",
		"UPDATE orders o JOIN payments p ON o.id = p.order_id SET o.status = 'x'",
		"DELETE FROM orders, payments WHERE orders.id = payments.order_id",
		"SELECT * FROM orders o WHERE EXISTS (TABLE payments)",
	} {
		_, _, err := guard.Rewrite(testContext("tenant-a"), query, nil)
		if !errors.Is(err, ErrUnsupportedQuery) {
			t.Fatalf("Rewrite(%q) error = %v, want ErrUnsupportedQuery", query, err)
		}
	}
}
