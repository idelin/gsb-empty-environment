package lockmgr

import "fmt"

// Level identifies one layer of the DB -> Table -> Page -> Row hierarchy.
type Level uint8

const (
	// LevelDB is the root of the hierarchy.
	LevelDB Level = iota
	// LevelTable is a table inside a database.
	LevelTable
	// LevelPage is a page inside a table.
	LevelPage
	// LevelRow is a row inside a page.
	LevelRow
)

// String returns the short name of the hierarchy level.
func (l Level) String() string {
	switch l {
	case LevelDB:
		return "DB"
	case LevelTable:
		return "Table"
	case LevelPage:
		return "Page"
	case LevelRow:
		return "Row"
	default:
		return "Unknown"
	}
}

// Resource is a node in the DB -> Table -> Page -> Row lock hierarchy.
// Names must be non-empty and unique within their parent. A database resource
// has an empty parent name.
type Resource struct {
	Level Level
	DB    string
	Table string
	Page  string
	Row   string
}

// NewDB builds the root resource of a hierarchy.
func NewDB(name string) Resource {
	return Resource{Level: LevelDB, DB: name}
}

// NewTable builds a table resource.
func NewTable(db, table string) Resource {
	return Resource{Level: LevelTable, DB: db, Table: table}
}

// NewPage builds a page resource.
func NewPage(db, table, page string) Resource {
	return Resource{Level: LevelPage, DB: db, Table: table, Page: page}
}

// NewRow builds a row resource.
func NewRow(db, table, page, row string) Resource {
	return Resource{Level: LevelRow, DB: db, Table: table, Page: page, Row: row}
}

// key is the unique map key of the resource.
func (r Resource) key() string {
	switch r.Level {
	case LevelDB:
		return "0:" + r.DB
	case LevelTable:
		return "1:" + r.DB + "/" + r.Table
	case LevelPage:
		return "2:" + r.DB + "/" + r.Table + "/" + r.Page
	case LevelRow:
		return "3:" + r.DB + "/" + r.Table + "/" + r.Page + "/" + r.Row
	default:
		panic("lockmgr: unknown resource level")
	}
}

// validate checks that every component required by the resource level is set.
func (r Resource) validate() error {
	if r.DB == "" {
		return fmt.Errorf("lockmgr: database name must not be empty")
	}
	switch r.Level {
	case LevelDB:
	case LevelTable:
		if r.Table == "" {
			return fmt.Errorf("lockmgr: table name must not be empty")
		}
	case LevelPage:
		if r.Table == "" || r.Page == "" {
			return fmt.Errorf("lockmgr: table and page names must not be empty")
		}
	case LevelRow:
		if r.Table == "" || r.Page == "" || r.Row == "" {
			return fmt.Errorf("lockmgr: table, page and row names must not be empty")
		}
	default:
		return fmt.Errorf("lockmgr: unknown resource level %d", r.Level)
	}
	return nil
}

// ancestors returns the parent chain from the root down to the immediate
// parent of the resource. A database has no ancestors.
func (r Resource) ancestors() []Resource {
	switch r.Level {
	case LevelDB:
		return nil
	case LevelTable:
		return []Resource{NewDB(r.DB)}
	case LevelPage:
		return []Resource{NewDB(r.DB), NewTable(r.DB, r.Table)}
	case LevelRow:
		return []Resource{
			NewDB(r.DB),
			NewTable(r.DB, r.Table),
			NewPage(r.DB, r.Table, r.Page),
		}
	default:
		panic("lockmgr: unknown resource level")
	}
}
