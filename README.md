# Tenant filtering middleware

`tenantfilter` rewrites SQL before it reaches the data store so every query is
confined to the tenant stored in the request context.

## Guarantees

- Every physical table in a `SELECT` receives a tenant predicate, including
  tables in joins, derived tables, CTEs, set-operation branches, and correlated
  or scalar subqueries.
- Existing `WHERE` expressions are parenthesized and combined with the injected
  predicate using `AND`, so an outer `OR` cannot widen the result set.
- `GROUP BY`, `HAVING`, `DISTINCT`, window functions, `ORDER BY`, `LIMIT`,
  `OFFSET`, and `FETCH` are always evaluated after the injected tenant filter.
- A caller-supplied tenant column reference in `WHERE`, `HAVING`, `ON`, `USING`,
  `QUALIFY`, or an `UPDATE ... SET` clause is rejected with
  `ErrTenantOverride`. Tenant values must come from the authenticated context.
- Multiple statements are rejected. `INSERT`, DDL, and unguardable sources such
  as table-valued functions are rejected with `ErrUnsupportedQuery`; they must
  use a typed, tenant-aware path instead. Dialect-ambiguous `#` comments and
  executable `/*! ... */` comments are rejected as well.
- CTE and derived-table references are not filtered a second time; their
  defining queries are already guarded. Physical tables that are merely
  shadowed by a CTE name in another scope are still filtered.

## Usage

```go
guard := tenantfilter.MustNewGuard(tenantfilter.Config{})

// Set this only after authentication has established the tenant.
ctx := tenantfilter.WithTenant(request.Context(), "tenant-a")

db := tenantfilter.Wrap(sqlDB, guard)
rows, err := db.QueryContext(ctx, `
    SELECT o.id
    FROM orders o
    WHERE EXISTS (
        SELECT 1 FROM payments p WHERE p.order_id = o.id
    )
    ORDER BY o.id
    LIMIT ? OFFSET ?
`, 20, 0)
```

The statement above is rewritten roughly as:

```sql
SELECT o.id
FROM orders o
WHERE (EXISTS (
    SELECT 1 FROM payments p
    WHERE (p.order_id = o.id) AND (p.tenant_id = ?)
)) AND (o.tenant_id = ?)
ORDER BY o.id
LIMIT ? OFFSET ?
```

The returned arguments are in textual placeholder order, including the tenant
values inserted by the guard.

`Guard.Rewrite`, `Guard.QueryContext`, `Guard.QueryRowContext`, and
`Guard.ExecContext` are also available without the `DB` wrapper. `*sql.DB` and
`*sql.Tx` both satisfy the wrapper interfaces.

## Configuration

```go
guard := tenantfilter.MustNewGuard(tenantfilter.Config{
    TenantColumn: "tenant_id",
    TableTenantColumns: map[string]string{
        "events": "account_id",
    },
    GlobalTables: []string{"currencies", "countries"},
    Placeholder:  tenantfilter.PlaceholderQuestion,
})
```

- `TenantColumn` defaults to `tenant_id`.
- `TableTenantColumns` maps a table name or `schema.table` to a different
  tenant column.
- `GlobalTables` is an explicit allowlist of shared tables. Everything else is
  tenant-scoped by default.
- `PlaceholderAuto` preserves `$1`-style queries and otherwise emits `?`.
  `PlaceholderQuestion` and `PlaceholderDollar` force a style.

## Verification

`go test ./...` runs unit tests for rewriting, override rejection, CTE scope,
aggregation, pagination, and argument ordering. When the `sqlite3` binary is
available, `TestSQLiteResultsStayWithinTenant` additionally creates two tenants
and verifies real query results for correlated subqueries, aggregates,
pagination, and explicit override attempts. A companion test verifies that
tenant-scoped `UPDATE` and `DELETE` statements do not touch another tenant.
