package cdcsched

// Op is the kind of change captured by the CDC stream.
type Op int

const (
	OpInsert Op = iota
	OpUpdate
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "INSERT"
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	}
	return "UNKNOWN"
}

// Event is a single CDC change for one row.
//
// Row carries the after-image for inserts/updates and the before-image for
// deletes. The scheduler only needs the foreign key columns (and the primary
// key via Key) to be present in Row in order to derive dependencies.
type Event struct {
	ID    int
	Table string
	Op    Op
	Key   string            // primary key value of the affected row
	Row   map[string]string // column values (after-image or before-image)
}
