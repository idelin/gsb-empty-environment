package tenantfilter

import (
	"context"
	"database/sql"
	"fmt"
)

// Queryer is implemented by *sql.DB, *sql.Tx and compatible wrappers.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Execer is implemented by *sql.DB, *sql.Tx and compatible wrappers.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// QueryExecer combines Queryer and Execer.
type QueryExecer interface {
	Queryer
	Execer
}

// RowQueryer is implemented by *sql.DB, *sql.Tx and compatible wrappers.
type RowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// QueryContext rewrites query and executes it.
func (g *Guard) QueryContext(ctx context.Context, db Queryer, query string, args ...any) (*sql.Rows, error) {
	if g == nil {
		return nil, fmt.Errorf("tenantfilter: nil guard")
	}
	if db == nil {
		return nil, fmt.Errorf("tenantfilter: nil queryer")
	}
	rewritten, rewrittenArgs, err := g.Rewrite(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, rewritten, rewrittenArgs...)
}

// ExecContext rewrites query and executes it. SELECT, UPDATE and DELETE are
// supported; INSERT and DDL are rejected by Rewrite.
func (g *Guard) ExecContext(ctx context.Context, db Execer, query string, args ...any) (sql.Result, error) {
	if g == nil {
		return nil, fmt.Errorf("tenantfilter: nil guard")
	}
	if db == nil {
		return nil, fmt.Errorf("tenantfilter: nil execer")
	}
	rewritten, rewrittenArgs, err := g.Rewrite(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, rewritten, rewrittenArgs...)
}

// Row is returned by QueryRowContext.
type Row struct {
	row *sql.Row
	err error
}

// Scan delegates to database/sql.Row.Scan.
func (r *Row) Scan(dest ...any) error {
	if r != nil && r.err != nil {
		return r.err
	}
	if r == nil || r.row == nil {
		return sql.ErrNoRows
	}
	return r.row.Scan(dest...)
}

// Err returns a rewrite error, if any.
func (r *Row) Err() error {
	if r == nil {
		return sql.ErrNoRows
	}
	return r.err
}

// QueryRowContext rewrites query and executes it as a single-row query.
func (g *Guard) QueryRowContext(ctx context.Context, db RowQueryer, query string, args ...any) *Row {
	if g == nil {
		return &Row{err: fmt.Errorf("tenantfilter: nil guard")}
	}
	if db == nil {
		return &Row{err: fmt.Errorf("tenantfilter: nil row queryer")}
	}
	rewritten, rewrittenArgs, err := g.Rewrite(ctx, query, args)
	if err != nil {
		return &Row{err: err}
	}
	return &Row{row: db.QueryRowContext(ctx, rewritten, rewrittenArgs...)}
}

// DB wraps a database handle with automatic tenant filtering.
type DB struct {
	db    QueryExecer
	guard *Guard
}

// Wrap returns a tenant-filtering database handle.
func Wrap(db QueryExecer, guard *Guard) *DB {
	return &DB{db: db, guard: guard}
}

// Guard returns the guard used by d.
func (d *DB) Guard() *Guard { return d.guard }

// QueryContext rewrites and executes a query.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if d == nil || d.guard == nil {
		return nil, fmt.Errorf("tenantfilter: nil guard")
	}
	return d.guard.QueryContext(ctx, d.db, query, args...)
}

// QueryRowContext rewrites and executes a single-row query.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	if d == nil || d.guard == nil {
		return &Row{err: fmt.Errorf("tenantfilter: nil guard")}
	}
	rower, ok := d.db.(RowQueryer)
	if !ok {
		return &Row{err: fmt.Errorf("tenantfilter: database does not implement QueryRowContext")}
	}
	return d.guard.QueryRowContext(ctx, rower, query, args...)
}

// ExecContext rewrites and executes a statement.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d == nil || d.guard == nil {
		return nil, fmt.Errorf("tenantfilter: nil guard")
	}
	return d.guard.ExecContext(ctx, d.db, query, args...)
}
