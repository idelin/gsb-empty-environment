// Package pagecheck implements an independent checker for ordered page
// files. Each page carries a summary (min key, max key, record count,
// next page ID) and the checker verifies that the summaries and the
// link structure agree with the actual page contents.
package pagecheck

// PageID identifies a page inside a File.
type PageID int64

// NilPage terminates the next-page chain.
const NilPage PageID = -1

// Record is a single row. Records are ordered by (Key, Seq): duplicate
// keys are legal as long as their explicit sequence numbers are
// non-decreasing.
type Record struct {
	Key   int64
	Seq   uint32
	Value string
}

// lessRecord reports whether a strictly precedes b in (Key, Seq) order.
func lessRecord(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Seq < b.Seq
}

// Summary is the page header metadata stored in the file.
type Summary struct {
	MinKey int64
	MaxKey int64
	Count  uint32
	NextID PageID
}

// Page is a summary plus the records actually stored on the page.
type Page struct {
	ID      PageID
	Summary Summary
	Records []Record
}

// File is an ordered collection of pages linked through Summary.NextID.
type File struct {
	Pages []Page
}
