package sqlbind_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"example.org/sqlbind"
)

// recordingDriver is a database/sql driver stub that captures every
// statement handed to it, so tests can assert exactly what reached (or
// never reached) the wire.
type recordingDriver struct {
	mu         sync.Mutex
	statements []recordedStatement
}

type recordedStatement struct {
	query string
	args  []driver.Value
}

func (d *recordingDriver) Open(string) (driver.Conn, error) { return &recordingConn{d: d}, nil }

func (d *recordingDriver) record(query string, args []driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statements = append(d.statements, recordedStatement{query: query, args: args})
}

func (d *recordingDriver) recorded() []recordedStatement {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]recordedStatement(nil), d.statements...)
}

func (d *recordingDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statements = nil
}

type recordingConn struct{ d *recordingDriver }

func (c *recordingConn) Prepare(query string) (driver.Stmt, error) {
	return &recordingStmt{d: c.d, query: query}, nil
}

func (c *recordingConn) Close() error              { return nil }
func (c *recordingConn) Begin() (driver.Tx, error) { return nil, errors.New("stub: no transactions") }

type recordingStmt struct {
	d     *recordingDriver
	query string
}

func (s *recordingStmt) Close() error  { return nil }
func (s *recordingStmt) NumInput() int { return -1 }

func (s *recordingStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.record(s.query, args)
	return driver.RowsAffected(1), nil
}

func (s *recordingStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.d.record(s.query, args)
	return &stubRows{}, nil
}

type stubRows struct{}

func (r *stubRows) Columns() []string         { return nil }
func (r *stubRows) Close() error              { return nil }
func (r *stubRows) Next([]driver.Value) error { return io.EOF }

var stub = &recordingDriver{}

func init() {
	sql.Register("sqlbind-stub", stub)
}

func openStub(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlbind-stub", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	stub.reset()
	return db
}

func TestExecContextSendsReboundStatement(t *testing.T) {
	db := openStub(t)
	query := `UPDATE t SET name = :name -- :ignored
WHERE id IN :ids AND note <> ':literal'`
	_, err := sqlbind.ExecContext(context.Background(), db, query, map[string]any{
		"name": "n",
		"ids":  []int{4, 5},
	})
	if err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	got := stub.recorded()
	if len(got) != 1 {
		t.Fatalf("recorded %d statements, want 1", len(got))
	}
	wantSQL := "UPDATE t SET name = $1 -- :ignored\nWHERE id IN ($2,$3) AND note <> ':literal'"
	if got[0].query != wantSQL {
		t.Errorf("query = %q, want %q", got[0].query, wantSQL)
	}
	wantArgs := []driver.Value{"n", int64(4), int64(5)}
	if !reflect.DeepEqual(got[0].args, wantArgs) {
		t.Errorf("args = %v, want %v", got[0].args, wantArgs)
	}
}

func TestUnclosedStatementNeverSent(t *testing.T) {
	db := openStub(t)
	// The final dollar-quoted string is never closed; the whole statement
	// must be rejected before anything reaches the driver.
	query := "SELECT * FROM t WHERE id = :id AND body = $fn$ unterminated"
	_, err := sqlbind.QueryContext(context.Background(), db, query, map[string]any{"id": 1})
	var unclosed *sqlbind.UnclosedError
	if !errors.As(err, &unclosed) {
		t.Fatalf("err = %v, want *UnclosedError", err)
	}
	if got := stub.recorded(); len(got) != 0 {
		t.Fatalf("driver received %d statements, want 0: %+v", len(got), got)
	}
}
