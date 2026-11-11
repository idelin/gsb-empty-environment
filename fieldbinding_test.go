package fieldbinding

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func fld(id int, name string, t Type) Field { return Field{ID: id, Name: name, Type: t} }

func typ(k Kind, nullable bool) Type { return Type{Kind: k, Nullable: nullable} }

func structType(nullable bool, fields ...Field) Type {
	return Type{Kind: Struct, Nullable: nullable, Fields: fields}
}

func TestRenameBindsByID(t *testing.T) {
	table := NewTable(&Schema{Fields: []Field{
		fld(1, "name", typ(String, false)),
	}})
	batch := &Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "full_name", typ(String, false)), // renamed, same ID
		}},
		Rows: []Value{Record(map[int]Value{1: Str("ada")})},
	}
	snap, err := table.Commit(batch)
	if err != nil {
		t.Fatalf("commit renamed field: %v", err)
	}
	if got := snap.Rows[0].Child(1); got.State != Present || got.S != "ada" {
		t.Fatalf("field 1 = %v, want present %q", got.State, "ada")
	}
}

func TestSameNameDifferentIDRejected(t *testing.T) {
	table := NewTable(&Schema{Fields: []Field{
		fld(1, "id", typ(Int64, false)),
		fld(2, "email", typ(String, false)),
	}})
	batch := &Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "id", typ(Int64, false)),
			fld(3, "email", typ(String, false)), // same name, new ID: not a rename
		}},
		Rows: []Value{Record(map[int]Value{1: Int(7), 3: Str("a@b.c")})},
	}
	if _, err := table.Commit(batch); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("err = %v, want ErrUnknownField", err)
	}
	snap := table.Snapshot()
	if snap.Version != 0 || len(snap.Rows) != 0 {
		t.Fatalf("snapshot changed after rejected commit: version=%d rows=%d",
			snap.Version, len(snap.Rows))
	}
}

func TestMoveNullableSubtree(t *testing.T) {
	batchSchema := &Schema{Fields: []Field{
		fld(1, "a", structType(true,
			fld(2, "x", typ(Int32, true)))),
	}}
	tableSchema := &Schema{Fields: []Field{
		fld(3, "b", structType(true,
			fld(2, "x", typ(Int32, true)))),
	}}
	m, err := Plan(batchSchema, tableSchema)
	if err != nil {
		t.Fatalf("plan nullable move: %v", err)
	}
	b, ok := m.Binding(2)
	if !ok {
		t.Fatal("no binding for field 2")
	}
	if len(b.BatchPath) != 2 || b.BatchPath[0] != 1 || b.BatchPath[1] != 2 {
		t.Fatalf("batch path = %v, want [1 2]", b.BatchPath)
	}
	if len(b.TablePath) != 2 || b.TablePath[0] != 3 || b.TablePath[1] != 2 {
		t.Fatalf("table path = %v, want [3 2]", b.TablePath)
	}

	table := NewTable(tableSchema)
	snap, err := table.Commit(&Batch{
		Schema: batchSchema,
		Rows:   []Value{Record(map[int]Value{1: Record(map[int]Value{2: Int(7)})})},
	})
	if err != nil {
		t.Fatalf("commit moved subtree: %v", err)
	}
	if got := snap.Rows[0].Child(3).Child(2); got.State != Present || got.I64 != 7 {
		t.Fatalf("moved value = %v, want present 7", got)
	}
}

func TestMoveNonNullableSubtreeRejected(t *testing.T) {
	batchSchema := &Schema{Fields: []Field{
		fld(1, "a", structType(false, // non-nullable ancestor
			fld(2, "x", typ(Int32, true)))),
	}}
	tableSchema := &Schema{Fields: []Field{
		fld(3, "b", structType(true,
			fld(2, "x", typ(Int32, true)))),
	}}
	if _, err := Plan(batchSchema, tableSchema); !errors.Is(err, ErrIllegalMove) {
		t.Fatalf("err = %v, want ErrIllegalMove", err)
	}

	// The moved root itself must be nullable too.
	batchSchema2 := &Schema{Fields: []Field{
		fld(1, "a", structType(true,
			fld(2, "x", typ(Int32, false)))),
	}}
	tableSchema2 := &Schema{Fields: []Field{
		fld(3, "b", structType(true,
			fld(2, "x", typ(Int32, false)))),
	}}
	if _, err := Plan(batchSchema2, tableSchema2); !errors.Is(err, ErrIllegalMove) {
		t.Fatalf("err = %v, want ErrIllegalMove", err)
	}
}

func TestNumericWidening(t *testing.T) {
	cases := []struct {
		name     string
		from, to Kind
		ok       bool
	}{
		{"identity", Int32, Int32, true},
		{"int32->int64", Int32, Int64, true},
		{"int32->float64", Int32, Float64, true},
		{"float32->float64", Float32, Float64, true},
		{"int64->int32", Int64, Int32, false},
		{"int64->float64", Int64, Float64, false}, // lossy beyond 2^53
		{"float64->float32", Float64, Float32, false},
		{"string->int64", String, Int64, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			batch := &Schema{Fields: []Field{fld(1, "n", typ(c.from, false))}}
			table := &Schema{Fields: []Field{fld(1, "n", typ(c.to, false))}}
			_, err := Plan(batch, table)
			if c.ok && err != nil {
				t.Fatalf("plan: %v", err)
			}
			if !c.ok && !errors.Is(err, ErrIncompatibleType) {
				t.Fatalf("err = %v, want ErrIncompatibleType", err)
			}
		})
	}
}

func TestWidenedValuesPreserved(t *testing.T) {
	table := NewTable(&Schema{Fields: []Field{
		fld(1, "i", typ(Int64, false)),
		fld(2, "f", typ(Float64, false)),
	}})
	snap, err := table.Commit(&Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "i", typ(Int32, false)),
			fld(2, "f", typ(Float32, false)),
		}},
		Rows: []Value{Record(map[int]Value{1: Int(42), 2: Float(1.5)})},
	})
	if err != nil {
		t.Fatalf("commit widened batch: %v", err)
	}
	if got := snap.Rows[0].Child(1); got.I64 != 42 {
		t.Fatalf("int = %d, want 42", got.I64)
	}
	if got := snap.Rows[0].Child(2); got.F64 != 1.5 {
		t.Fatalf("float = %v, want 1.5", got.F64)
	}
}

func TestMissingParentNotMergedWithNull(t *testing.T) {
	schema := func() *Schema {
		return &Schema{Fields: []Field{
			fld(1, "addr", structType(true,
				fld(2, "zip", typ(Int32, true)),
				fld(3, "city", typ(String, true)))),
			fld(4, "nickname", typ(String, true)), // absent from the batch schema
		}}
	}
	table := NewTable(schema())
	snap, err := table.Commit(&Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "addr", structType(true,
				fld(2, "zip", typ(Int32, true)),
				fld(3, "city", typ(String, true)))),
		}},
		Rows: []Value{
			Record(map[int]Value{}),                                        // parent missing
			Record(map[int]Value{1: ExplicitNull()}),                       // parent explicit null
			Record(map[int]Value{1: Record(map[int]Value{2: Int(90001)})}), // present
		},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := snap.Rows[0].Child(1).State; got != Missing {
		t.Fatalf("row0 addr = %s, want missing", got)
	}
	if got := snap.Rows[1].Child(1).State; got != Null {
		t.Fatalf("row1 addr = %s, want null", got)
	}
	if snap.Rows[0].Child(1).State == snap.Rows[1].Child(1).State {
		t.Fatal("missing parent and explicit null parent were merged")
	}

	row2 := snap.Rows[2].Child(1)
	if row2.State != Present {
		t.Fatalf("row2 addr = %s, want present", row2.State)
	}
	if got := row2.Child(2); got.State != Present || got.I64 != 90001 {
		t.Fatalf("row2 zip = %v, want present 90001", got)
	}
	if got := row2.Child(3).State; got != Missing {
		t.Fatalf("row2 city = %s, want missing", got)
	}
	// A nullable column unknown to the batch stays missing, not null.
	for i, r := range snap.Rows {
		if got := r.Child(4).State; got != Missing {
			t.Fatalf("row%d nickname = %s, want missing", i, got)
		}
	}
}

func TestMissingRequiredValueRejected(t *testing.T) {
	table := NewTable(&Schema{Fields: []Field{
		fld(1, "id", typ(Int32, false)),
	}})
	_, err := table.Commit(&Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "id", typ(Int32, false)),
		}},
		Rows: []Value{Record(map[int]Value{})}, // required field omitted
	})
	if !errors.Is(err, ErrMissingValue) {
		t.Fatalf("err = %v, want ErrMissingValue", err)
	}
	if got := table.Snapshot().Version; got != 0 {
		t.Fatalf("version = %d, want 0", got)
	}
}

func TestExplicitNullRejectedForNonNullable(t *testing.T) {
	table := NewTable(&Schema{Fields: []Field{
		fld(1, "id", typ(Int32, false)),
	}})
	_, err := table.Commit(&Batch{
		Schema: &Schema{Fields: []Field{
			fld(1, "id", typ(Int32, false)),
		}},
		Rows: []Value{Record(map[int]Value{1: ExplicitNull()})},
	})
	if !errors.Is(err, ErrUnexpectedNull) {
		t.Fatalf("err = %v, want ErrUnexpectedNull", err)
	}
}

func TestLastRowOverflowAbortsAtomically(t *testing.T) {
	schema := &Schema{Fields: []Field{
		fld(1, "n", typ(Int32, false)),
	}}
	table := NewTable(schema)
	first, err := table.Commit(&Batch{
		Schema: schema,
		Rows: []Value{
			Record(map[int]Value{1: Int(1)}),
			Record(map[int]Value{1: Int(2)}),
		},
	})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// The overflow hides in the last row: validation must scan every row
	// before publishing anything.
	_, err = table.Commit(&Batch{
		Schema: schema,
		Rows: []Value{
			Record(map[int]Value{1: Int(3)}),
			Record(map[int]Value{1: Int(4)}),
			Record(map[int]Value{1: Int(1 << 40)}), // overflows int32
		},
	})
	if !errors.Is(err, ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
	if !strings.Contains(err.Error(), "row 2") {
		t.Fatalf("err = %v, want row index 2 reported", err)
	}

	snap := table.Snapshot()
	if snap != first && snap.Version != 1 {
		t.Fatalf("snapshot pointer changed after aborted commit")
	}
	if snap.Version != 1 || len(snap.Rows) != 2 {
		t.Fatalf("readers see version=%d rows=%d, want version=1 rows=2",
			snap.Version, len(snap.Rows))
	}
	if got := snap.Rows[1].Child(1).I64; got != 2 {
		t.Fatalf("last visible row = %d, want 2", got)
	}
}

func TestConcurrentReadersSeeConsistentSnapshot(t *testing.T) {
	schema := &Schema{Fields: []Field{
		fld(1, "n", typ(Int32, false)),
	}}
	table := NewTable(schema)

	const batches = 20
	const rowsPerBatch = 10

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := table.Snapshot()
				if len(snap.Rows) != int(snap.Version)*rowsPerBatch {
					t.Errorf("inconsistent snapshot: version=%d rows=%d",
						snap.Version, len(snap.Rows))
					return
				}
			}
		}()
	}

	for b := 0; b < batches; b++ {
		rows := make([]Value, rowsPerBatch)
		for i := range rows {
			rows[i] = Record(map[int]Value{1: Int(int64(b*rowsPerBatch + i))})
		}
		if _, err := table.Commit(&Batch{Schema: schema, Rows: rows}); err != nil {
			t.Fatalf("commit %d: %v", b, err)
		}
	}
	close(stop)
	wg.Wait()

	snap := table.Snapshot()
	if snap.Version != batches || len(snap.Rows) != batches*rowsPerBatch {
		t.Fatalf("final snapshot: version=%d rows=%d", snap.Version, len(snap.Rows))
	}
}
