package tenantfilter

import (
	"context"
	"testing"
)

func FuzzRewriteDoesNotPanic(f *testing.F) {
	seeds := []string{
		"SELECT * FROM orders",
		"SELECT * FROM orders o WHERE EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id)",
		"WITH x AS (SELECT * FROM orders) SELECT * FROM x",
		"SELECT tenant_id, COUNT(*) FROM orders GROUP BY tenant_id LIMIT 10",
		"",
		"SELECT ((((",
		"/* comment */ SELECT * FROM orders -- trailing",
		"UPDATE orders SET status = ? WHERE id = ?",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	guard := MustNewGuard(Config{})
	ctx := WithTenant(context.Background(), "tenant-a")
	f.Fuzz(func(t *testing.T, query string) {
		_, _, _ = guard.Rewrite(ctx, query, nil)
	})
}
