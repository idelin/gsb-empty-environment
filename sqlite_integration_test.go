package tenantfilter

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sqliteBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed")
	}
	return path
}

func runSQLite(t *testing.T, binary, database, script string) []string {
	t.Helper()
	command := exec.Command(binary, "-batch", "-noheader", "-separator", "|", database)
	command.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("sqlite3 failed: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func bindSQLite(t *testing.T, query string, args []any) string {
	t.Helper()
	tokens, err := lexSQL(query)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	index := 0
	for _, tok := range tokens {
		if tok.kind != tokPlaceholder {
			out.WriteString(tok.text)
			continue
		}
		if index >= len(args) {
			t.Fatalf("query %q has more placeholders than args", query)
		}
		out.WriteString(sqliteLiteral(args[index]))
		index++
	}
	if index != len(args) {
		t.Fatalf("query %q has %d placeholders but %d args", query, index, len(args))
	}
	return out.String()
}

func sqliteLiteral(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(typed, "'", "''") + "'"
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(value), "'", "''") + "'"
	}
}

func queryTenantRows(t *testing.T, guard *Guard, binary, database, tenant, query string, args ...any) []string {
	t.Helper()
	rewritten, rewrittenArgs, err := guard.Rewrite(testContext(tenant), query, args)
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", query, err)
	}
	return runSQLite(t, binary, database, bindSQLite(t, rewritten, rewrittenArgs)+";\n")
}

func execTenantSQL(t *testing.T, guard *Guard, binary, database, tenant, query string, args ...any) {
	t.Helper()
	rewritten, rewrittenArgs, err := guard.Rewrite(testContext(tenant), query, args)
	if err != nil {
		t.Fatalf("Rewrite(%q): %v", query, err)
	}
	runSQLite(t, binary, database, bindSQLite(t, rewritten, rewrittenArgs)+";\n")
}

func TestSQLiteResultsStayWithinTenant(t *testing.T) {
	binary := sqliteBinary(t)
	database := filepath.Join(t.TempDir(), "tenant.sqlite")
	runSQLite(t, binary, database, `
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  customer TEXT NOT NULL,
  status TEXT NOT NULL,
  amount INTEGER NOT NULL
);
CREATE TABLE payments (
  id INTEGER PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  order_id INTEGER NOT NULL,
  amount INTEGER NOT NULL
);
INSERT INTO orders VALUES
  (1, 't1', 'alice', 'open',   100),
  (2, 't2', 'bob',   'open',   200),
  (3, 't1', 'carol', 'closed', 300),
  (4, 't2', 'dave',  'open',   400);
INSERT INTO payments VALUES
  (1, 't1', 1, 10),
  (2, 't2', 2, 20),
  (3, 't1', 3, 30),
  (4, 't2', 4, 40),
  (5, 't1', 2, 50);
`)

	guard := testGuard(t)

	t.Run("correlated subquery", func(t *testing.T) {
		query := `SELECT o.id
FROM orders o
WHERE EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id)
ORDER BY o.id`
		if got := queryTenantRows(t, guard, binary, database, "t1", query); strings.Join(got, ",") != "1,3" {
			t.Fatalf("t1 rows = %v, want [1 3]", got)
		}
		if got := queryTenantRows(t, guard, binary, database, "t2", query); strings.Join(got, ",") != "2,4" {
			t.Fatalf("t2 rows = %v, want [2 4]", got)
		}
	})

	t.Run("aggregation", func(t *testing.T) {
		if got := queryTenantRows(t, guard, binary, database, "t1", "SELECT COUNT(*) FROM orders"); strings.Join(got, ",") != "2" {
			t.Fatalf("t1 count = %v, want [2]", got)
		}
		if got := queryTenantRows(t, guard, binary, database, "t2", "SELECT COUNT(*) FROM orders"); strings.Join(got, ",") != "2" {
			t.Fatalf("t2 count = %v, want [2]", got)
		}
		grouped := queryTenantRows(t, guard, binary, database, "t1",
			"SELECT tenant_id, COUNT(*) FROM orders GROUP BY tenant_id ORDER BY tenant_id")
		if strings.Join(grouped, ",") != "t1|2" {
			t.Fatalf("t1 grouped rows = %v, want only t1|2", grouped)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		query := "SELECT id FROM orders ORDER BY id LIMIT ? OFFSET ?"
		if got := queryTenantRows(t, guard, binary, database, "t2", query, 2, 0); strings.Join(got, ",") != "2,4" {
			t.Fatalf("t2 page = %v, want [2 4]", got)
		}
		if got := queryTenantRows(t, guard, binary, database, "t1", query, 1, 1); strings.Join(got, ",") != "3" {
			t.Fatalf("t1 page = %v, want [3]", got)
		}
	})

	t.Run("outer or cannot widen results", func(t *testing.T) {
		query := "SELECT id FROM orders WHERE status = 'open' OR 1 = 1 ORDER BY id"
		if got := queryTenantRows(t, guard, binary, database, "t1", query); strings.Join(got, ",") != "1,3" {
			t.Fatalf("t1 rows = %v, want [1 3]", got)
		}
	})

	t.Run("correlated aggregate plus pagination", func(t *testing.T) {
		query := `SELECT o.id,
       (SELECT COUNT(*) FROM payments p WHERE p.order_id = o.id) AS payment_count
FROM orders o
ORDER BY o.id
LIMIT ? OFFSET ?`
		if got := queryTenantRows(t, guard, binary, database, "t2", query, 2, 0); strings.Join(got, ",") != "2|1,4|1" {
			t.Fatalf("t2 rows = %v, want [2|1 4|1]", got)
		}
		if got := queryTenantRows(t, guard, binary, database, "t1", query, 2, 0); strings.Join(got, ",") != "1|1,3|1" {
			t.Fatalf("t1 rows = %v, want [1|1 3|1]", got)
		}
	})

	t.Run("explicit overrides rejected", func(t *testing.T) {
		for _, query := range []string{
			"SELECT o.id FROM orders o WHERE o.tenant_id = 't2'",
			"SELECT o.id FROM orders o WHERE EXISTS (SELECT 1 FROM payments p WHERE p.tenant_id = 't2')",
			`SELECT o.id FROM orders o
			 WHERE EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id)
			   AND o.tenant_id = 't2'`,
			"SELECT tenant_id, COUNT(*) FROM orders GROUP BY tenant_id HAVING tenant_id = 't2'",
		} {
			if _, _, err := guard.Rewrite(testContext("t1"), query, nil); !errors.Is(err, ErrTenantOverride) {
				t.Fatalf("Rewrite(%q) error = %v, want ErrTenantOverride", query, err)
			}
		}
	})
}

func TestSQLiteUpdateAndDeleteStayWithinTenant(t *testing.T) {
	binary := sqliteBinary(t)
	database := filepath.Join(t.TempDir(), "tenant-writes.sqlite")
	runSQLite(t, binary, database, `
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  status TEXT NOT NULL
);
INSERT INTO orders VALUES
  (1, 't1', 'open'),
  (2, 't1', 'open'),
  (3, 't2', 'open');
`)

	guard := testGuard(t)
	execTenantSQL(t, guard, binary, database, "t1", "UPDATE orders SET status = 'done'")
	if got := runSQLite(t, binary, database, "SELECT id, status FROM orders ORDER BY id;\n"); strings.Join(got, ",") != "1|done,2|done,3|open" {
		t.Fatalf("after update rows = %v", got)
	}

	execTenantSQL(t, guard, binary, database, "t1", "DELETE FROM orders WHERE status = 'done'")
	if got := runSQLite(t, binary, database, "SELECT id, status FROM orders ORDER BY id;\n"); strings.Join(got, ",") != "3|open" {
		t.Fatalf("after delete rows = %v, want only t2 row", got)
	}

	if _, _, err := guard.Rewrite(testContext("t1"), "UPDATE orders SET status = 'x' WHERE tenant_id = 't2'", nil); !errors.Is(err, ErrTenantOverride) {
		t.Fatalf("error = %v, want ErrTenantOverride", err)
	}
}
