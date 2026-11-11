package sqlbind

import (
	"context"
	"database/sql"
)

// ExecContext binds query and executes it with db. When binding fails (for
// example because the statement is unclosed), nothing is sent to the
// database and the binding error is returned.
func ExecContext(ctx context.Context, db *sql.DB, query string, args map[string]any) (sql.Result, error) {
	rebound, values, err := Bind(query, args)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, rebound, values...)
}

// QueryContext binds query and runs it with db. When binding fails (for
// example because the statement is unclosed), nothing is sent to the
// database and the binding error is returned.
func QueryContext(ctx context.Context, db *sql.DB, query string, args map[string]any) (*sql.Rows, error) {
	rebound, values, err := Bind(query, args)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, rebound, values...)
}
