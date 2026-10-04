package snapshotpage

import (
	"errors"
	"fmt"
)

// Request errors.
var (
	// ErrInvalidLimit means Limit was not positive.
	ErrInvalidLimit = errors.New("snapshotpage: limit must be positive")
	// ErrConflictingCursors means both After and Before were set.
	ErrConflictingCursors = errors.New("snapshotpage: After and Before are mutually exclusive")
)

// PageRequest describes one page fetch. Set exactly one of After/Before to
// follow a cursor from a previous Page, or neither for the first page.
type PageRequest struct {
	Spec   SortSpec
	Limit  int
	After  string // forward: rows strictly after this cursor's key
	Before string // backward: rows strictly before this cursor's key
}

// Page is one fetched window. Next/Prev are opaque cursors; an empty string
// means there is no further page in that direction. Following Next with
// After and then the resulting Prev with Before (or vice versa) returns the
// adjacent pages: forward and backward pagination are mutual inverses.
type Page struct {
	Items []Record
	Next  string
	Prev  string
}

// Paginator issues keyset pages over a Store.
type Paginator struct {
	store *Store
}

// NewPaginator returns a Paginator bound to a store.
func NewPaginator(store *Store) *Paginator {
	return &Paginator{store: store}
}

// Page fetches one window according to req.
//
// Forward (After or first page): the first Limit rows with key greater than
// the After key, in sort order. Backward (Before): the Limit rows
// immediately preceding the Before key, returned in sort order. Because
// cursors carry key values rather than positions, rows inserted, updated or
// deleted between requests — including the boundary row itself — never shift
// or duplicate results.
func (p *Paginator) Page(req PageRequest) (Page, error) {
	if req.Limit <= 0 {
		return Page{}, ErrInvalidLimit
	}
	if req.After != "" && req.Before != "" {
		return Page{}, ErrConflictingCursors
	}

	snapshotID, recs := p.store.view()

	backward := req.Before != ""
	var boundary key
	var hasBoundary bool
	if token := firstNonEmpty(req.After, req.Before); token != "" {
		k, err := decodeCursor(token, snapshotID, req.Spec)
		if err != nil {
			return Page{}, err
		}
		boundary, hasBoundary = k, true
	}

	req.Spec.sortRecords(recs)

	window := make([]Record, 0, req.Limit)
	var more bool
	if !backward {
		for _, rec := range recs {
			if hasBoundary && req.Spec.compareKeys(req.Spec.keyOf(rec), boundary) <= 0 {
				continue
			}
			if len(window) == req.Limit {
				more = true
				break
			}
			window = append(window, rec)
		}
	} else {
		// Collect the rows before the boundary, then keep the Limit rows
		// closest to it, preserving sort order.
		before := make([]Record, 0, len(recs))
		for _, rec := range recs {
			if req.Spec.compareKeys(req.Spec.keyOf(rec), boundary) < 0 {
				before = append(before, rec)
			}
		}
		if len(before) > req.Limit {
			more = true
			before = before[len(before)-req.Limit:]
		}
		window = before
	}

	page := Page{Items: window}
	if len(window) == 0 {
		return page, nil
	}

	first, last := req.Spec.keyOf(window[0]), req.Spec.keyOf(window[len(window)-1])
	// Next exists when more rows follow (forward overflow) or when we paged
	// backward, which by construction means the boundary row's position is
	// ahead of this window.
	if (!backward && more) || backward {
		token, err := encodeCursor(snapshotID, req.Spec.hash(), last)
		if err != nil {
			return Page{}, err
		}
		page.Next = token
	}
	// Prev exists symmetrically: backward overflow, or any forward page that
	// started from a cursor.
	if (backward && more) || (!backward && hasBoundary) {
		token, err := encodeCursor(snapshotID, req.Spec.hash(), first)
		if err != nil {
			return Page{}, err
		}
		page.Prev = token
	}
	return page, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// MustPage is a test-friendly helper that panics on error.
func (p *Paginator) MustPage(req PageRequest) Page {
	page, err := p.Page(req)
	if err != nil {
		panic(fmt.Sprintf("snapshotpage: MustPage: %v", err))
	}
	return page
}
