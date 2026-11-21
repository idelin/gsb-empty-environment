package tenantfilter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func init() {
	sql.Register("tenantfilter-recording", &recordingDriver{})
}

type recordingDriver struct{}

func (d *recordingDriver) Open(string) (driver.Conn, error) {
	return &recordingConn{}, nil
}

type recordingConn struct{}

func (c *recordingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not implemented")
}

func (c *recordingConn) Close() error { return nil }

func (c *recordingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not implemented")
}

type recordingCall struct {
	query string
	args  []any
}

var recorder struct {
	sync.Mutex
	query func(query string, args []any) (driver.Rows, error)
	exec  func(query string, args []any) (driver.Result, error)
	calls []recordingCall
}

func resetRecorder() {
	recorder.Lock()
	defer recorder.Unlock()
	recorder.query = nil
	recorder.exec = nil
	recorder.calls = nil
}

func recordedCalls() []recordingCall {
	recorder.Lock()
	defer recorder.Unlock()
	return append([]recordingCall(nil), recorder.calls...)
}

func (c *recordingConn) QueryContext(_ context.Context, query string, named []driver.NamedValue) (driver.Rows, error) {
	args := namedValues(named)
	recorder.Lock()
	recorder.calls = append(recorder.calls, recordingCall{query: query, args: args})
	hook := recorder.query
	recorder.Unlock()
	if hook == nil {
		return &staticRows{columns: []string{"value"}}, nil
	}
	return hook(query, args)
}

func (c *recordingConn) ExecContext(_ context.Context, query string, named []driver.NamedValue) (driver.Result, error) {
	args := namedValues(named)
	recorder.Lock()
	recorder.calls = append(recorder.calls, recordingCall{query: query, args: args})
	hook := recorder.exec
	recorder.Unlock()
	if hook == nil {
		return driver.RowsAffected(1), nil
	}
	return hook(query, args)
}

func namedValues(named []driver.NamedValue) []any {
	args := make([]any, len(named))
	for i, value := range named {
		args[i] = value.Value
	}
	return args
}

type staticRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *staticRows) Columns() []string { return r.columns }
func (r *staticRows) Close() error      { return nil }

func (r *staticRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func TestDBWrapperRewritesAndScopesCalls(t *testing.T) {
	resetRecorder()
	defer resetRecorder()

	recorder.Lock()
	recorder.query = func(query string, args []any) (driver.Rows, error) {
		return &staticRows{
			columns: []string{"id"},
			values:  [][]driver.Value{{int64(1)}},
		}, nil
	}
	recorder.Unlock()

	sqlDB, err := sql.Open("tenantfilter-recording", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	guard := testGuard(t)
	wrapped := Wrap(sqlDB, guard)
	rows, err := wrapped.QueryContext(testContext("tenant-a"),
		"SELECT id FROM orders WHERE status = ?", "open")
	if err != nil {
		t.Fatal(err)
	}
	var id int
	if !rows.Next() {
		t.Fatal("expected a row")
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}

	calls := recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if !containsTenantPredicate(calls[0].query, "orders.tenant_id") {
		t.Fatalf("query does not contain tenant predicate: %s", calls[0].query)
	}
	if !reflect.DeepEqual(calls[0].args, []any{"open", "tenant-a"}) {
		t.Fatalf("args = %#v, want [open tenant-a]", calls[0].args)
	}
}

func TestDBWrapperRejectsOverrideBeforeDatabaseCall(t *testing.T) {
	resetRecorder()
	defer resetRecorder()

	sqlDB, err := sql.Open("tenantfilter-recording", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	wrapped := Wrap(sqlDB, testGuard(t))
	_, err = wrapped.QueryContext(testContext("tenant-a"),
		"SELECT id FROM orders WHERE tenant_id = 'tenant-b'")
	if !errors.Is(err, ErrTenantOverride) {
		t.Fatalf("error = %v, want ErrTenantOverride", err)
	}
	if calls := recordedCalls(); len(calls) != 0 {
		t.Fatalf("database was called %d times, want 0", len(calls))
	}
}

func TestDBWrapperQueryRowAndExec(t *testing.T) {
	resetRecorder()
	defer resetRecorder()

	recorder.Lock()
	recorder.query = func(string, []any) (driver.Rows, error) {
		return &staticRows{
			columns: []string{"count"},
			values:  [][]driver.Value{{int64(2)}},
		}, nil
	}
	recorder.exec = func(string, []any) (driver.Result, error) {
		return driver.RowsAffected(1), nil
	}
	recorder.Unlock()

	sqlDB, err := sql.Open("tenantfilter-recording", "")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	wrapped := Wrap(sqlDB, testGuard(t))
	var count int
	if err := wrapped.QueryRowContext(testContext("tenant-a"),
		"SELECT COUNT(*) FROM orders").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	result, err := wrapped.ExecContext(testContext("tenant-a"),
		"UPDATE orders SET status = ? WHERE id = ?", "closed", 7)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("affected = %d, want 1", affected)
	}

	calls := recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if !containsTenantPredicate(call.query, "orders.tenant_id") {
			t.Fatalf("query does not contain tenant predicate: %s", call.query)
		}
	}
	if !reflect.DeepEqual(calls[1].args, []any{"closed", int64(7), "tenant-a"}) {
		t.Fatalf("update args = %#v", calls[1].args)
	}
}

func containsTenantPredicate(query, column string) bool {
	return strings.Contains(query, column+" =")
}
