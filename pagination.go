package taskpkg

import (
	"errors"
	"fmt"
)

// Key is a business key returned by the upstream list endpoint. Collection
// hosts commonly use strings for IDs; keeping an alias makes the public API
// readable without forcing callers to convert their IDs.
type Key = string

// Cursor is an opaque cursor value supplied by the upstream endpoint.
type Cursor = string

var (
	// ErrPinnedLength is returned when a page supplies pinned flags but the
	// number of flags does not match the number of keys.
	ErrPinnedLength = errors.New("taskpkg: pinned flags length does not match keys")

	// ErrTrackerDone is returned when Observe is called after the tracker has
	// reached a terminal status.
	ErrTrackerDone = errors.New("taskpkg: tracker has reached a terminal status")
)

// Status is the outcome of observing one page, or the state of a tracker.
type Status string

const (
	// StatusContinue means the page made progress and another request is
	// allowed. It never means that the upstream collection is complete.
	StatusContinue Status = "continue"

	// StatusComplete means the upstream explicitly returned no next page.
	// This is the only status for which a collection is complete.
	StatusComplete Status = "complete"

	// StatusBudgetExhausted means the page budget ran out before the upstream
	// explicitly ended the collection. It is an incomplete outcome.
	StatusBudgetExhausted Status = "budget_exhausted"

	// StatusCursorCycle means the next cursor points at a cursor that was
	// already visited. The full cycle is attached to the result. It is an
	// incomplete outcome, not an inferred upstream end.
	StatusCursorCycle Status = "cursor_cycle"

	// StatusInvalidPage means the page was malformed and was not consumed.
	StatusInvalidPage Status = "invalid_page"
)

// Short aliases for callers that prefer concise status names.
const (
	Continue        = StatusContinue
	Complete        = StatusComplete
	BudgetExhausted = StatusBudgetExhausted
	CursorCycle     = StatusCursorCycle
	InvalidPage     = StatusInvalidPage
)

// IsComplete reports whether the status is the explicit upstream completion
// signal.
func (status Status) IsComplete() bool { return status == StatusComplete }

// IsIncomplete reports whether the status is terminal but did not observe an
// explicit upstream end of pagination.
func (status Status) IsIncomplete() bool {
	return status == StatusBudgetExhausted || status == StatusCursorCycle
}

// IsTerminal reports whether no further pages may be consumed.
func (status Status) IsTerminal() bool { return status.IsComplete() || status.IsIncomplete() }

// Page is one ordered page returned by the upstream list endpoint.
//
// Keys are business keys in the order returned by the endpoint. Pinned marks
// the corresponding key as pinned. An empty Pinned slice means no key is
// pinned; otherwise its length must equal len(Keys).
//
// NextCursor is the cursor supplied by the upstream endpoint for the next
// request. HasNext marks that cursor as an explicit continuation, including
// the case where the cursor value itself is empty. A non-empty NextCursor is
// also accepted as an explicit continuation so that callers with a
// cursor-only representation do not need the boolean. A page with neither
// HasNext nor NextCursor is the explicit "no next page" signal.
type Page struct {
	Keys       []string
	Pinned     []bool
	NextCursor string
	HasNext    bool
}

// NextPage constructs a page that explicitly continues at cursor.
func NextPage(keys []string, pinned []bool, cursor string) Page {
	return Page{Keys: keys, Pinned: pinned, NextCursor: cursor, HasNext: true}
}

// LastPage constructs a page that explicitly reports no next page.
func LastPage(keys []string, pinned []bool) Page {
	return Page{Keys: keys, Pinned: pinned}
}

// HasNextPage returns the explicit continuation state of the page.
func (page Page) HasNextPage() bool {
	return page.HasNext || page.NextCursor != ""
}

// NextPageCursor returns the next cursor and whether it is present.
func (page Page) NextPageCursor() (Cursor, bool) {
	if page.HasNext {
		return page.NextCursor, true
	}
	if page.NextCursor != "" {
		return page.NextCursor, true
	}
	return "", false
}

// Validate checks that pinned flags, when supplied, align with keys.
func (page Page) Validate() error {
	if len(page.Pinned) != 0 && len(page.Pinned) != len(page.Keys) {
		return ErrPinnedLength
	}
	return nil
}

// PageRecord is the tracker's immutable record of an observed page.
type PageRecord struct {
	Index         int
	Cursor        Cursor
	NextCursor    Cursor
	HasNext       bool
	Keys          []string
	Pinned        []bool
	NewKeys       []string
	NewPinnedKeys []string
	DuplicateKeys []string
	Progressed    bool
}

// CursorEdge is one edge in the observed cursor graph.
type CursorEdge struct {
	PageIndex int
	From      Cursor
	To        Cursor
}

// CycleEvidence describes a complete cursor cycle. Cursors starts with the
// repeated cursor and ends with the same cursor; Pages contains every observed
// page in the cycle, in order, so the evidence is not just a boolean flag.
type CycleEvidence struct {
	StartCursor Cursor
	Cursors     []Cursor
	Pages       []PageRecord
	Edges       []CursorEdge
}

// Length returns the number of pages in the cycle.
func (evidence *CycleEvidence) Length() int {
	if evidence == nil {
		return 0
	}
	return len(evidence.Pages)
}

// Progress is the result of observing one page.
type Progress struct {
	Status Status

	// Complete is true only for an explicit upstream no-next page.
	Complete bool
	// Incomplete is true for a terminal outcome that is not completion.
	Incomplete bool

	// Progressed is true only when the page contributed a new non-pinned key
	// or introduced a new cursor.
	Progressed bool

	NewKeys             []string
	NewPinnedKeys       []string
	DuplicateKeys       []string
	DuplicatePinnedKeys []string

	NewCursor      bool
	CursorRepeated bool
	NextCursor     Cursor
	HasNext        bool

	PageIndex       int
	CollectedCount  int
	BudgetRemaining int

	Cycle *CycleEvidence
	Error error
}

// Done reports whether the result is terminal.
func (progress Progress) Done() bool { return progress.Status.IsTerminal() }

// IsComplete reports whether the result is an explicit upstream completion.
func (progress Progress) IsComplete() bool { return progress.Status.IsComplete() }

// Summary is the state of a tracker after one or more observations. The
// Complete field is true only for an explicit upstream no-next signal.
type Summary struct {
	Status          Status
	Complete        bool
	Incomplete      bool
	CollectedKeys   []string
	Pages           []PageRecord
	Cycle           *CycleEvidence
	Progressed      bool
	Budget          int
	Consumed        int
	BudgetRemaining int
	Error           error
}

// Done reports whether the tracker is in a terminal state.
func (summary Summary) Done() bool { return summary.Status.IsTerminal() }

// Tracker consumes pages in request order and determines whether pagination
// is making progress.
//
// A newly seen non-pinned key advances progress. A newly seen next cursor also
// advances progress, even when the page is empty or contains only pinned keys.
// A repeated pinned boundary does not stop pagination. Completion requires an
// explicit no-next page.
type Tracker struct {
	budget      int
	consumed    int
	startCursor Cursor
	cursor      Cursor
	status      Status

	seenKeys    map[string]struct{}
	collected   []string
	seenCursors map[Cursor]struct{}
	records     []PageRecord
	atCursor    map[Cursor]int
	progressed  bool
	cycle       *CycleEvidence
}

// NewTracker creates a tracker with a page budget. The budget counts observed
// pages. A negative budget means unlimited. If the budget is zero the tracker
// starts exhausted and does not consume a page.
func NewTracker(budget int) *Tracker {
	return NewTrackerAt(budget, "")
}

// NewTrackerAt is NewTracker with an explicit starting cursor. The starting
// cursor is considered already visited, so a next cursor that points back to
// it is reported as a complete cycle.
func NewTrackerAt(budget int, startCursor Cursor) *Tracker {
	tracker := &Tracker{
		budget:      budget,
		startCursor: startCursor,
		cursor:      startCursor,
		status:      StatusContinue,
		seenKeys:    make(map[string]struct{}),
		seenCursors: make(map[Cursor]struct{}),
		atCursor:    make(map[Cursor]int),
	}
	tracker.seenCursors[startCursor] = struct{}{}
	if budget == 0 {
		tracker.status = StatusBudgetExhausted
	}
	return tracker
}

// Status returns the tracker's current status.
func (tracker *Tracker) Status() Status { return tracker.status }

// StartCursor returns the cursor initialized by NewTrackerAt.
func (tracker *Tracker) StartCursor() Cursor { return tracker.startCursor }

// CurrentCursor returns the cursor that should be used for the next request.
func (tracker *Tracker) CurrentCursor() Cursor { return tracker.cursor }

// BudgetRemaining returns the number of page observations left, or -1 when
// the budget is unlimited.
func (tracker *Tracker) BudgetRemaining() int {
	if tracker.budget < 0 {
		return -1
	}
	if tracker.consumed >= tracker.budget {
		return 0
	}
	return tracker.budget - tracker.consumed
}

// CollectedKeys returns every business key observed so far, in first-seen
// order, including pinned keys. Repeated keys are returned once.
func (tracker *Tracker) CollectedKeys() []string {
	return append([]string(nil), tracker.collected...)
}

// CollectedSet returns a copy of the collected key set.
func (tracker *Tracker) CollectedSet() map[string]struct{} {
	out := make(map[string]struct{}, len(tracker.seenKeys))
	for key := range tracker.seenKeys {
		out[key] = struct{}{}
	}
	return out
}

// Pages returns the observed page records in request order.
func (tracker *Tracker) Pages() []PageRecord {
	return cloneRecords(tracker.records)
}

// Progressed reports whether any observed page advanced progress.
func (tracker *Tracker) Progressed() bool { return tracker.progressed }

// Observe consumes one page. Invalid pages are rejected without changing the
// tracker's collection, cursor history, or budget. Observe after a terminal
// status returns that terminal status and does not consume another page.
func (tracker *Tracker) Observe(page Page) Progress {
	if tracker.status.IsTerminal() {
		return Progress{
			Status:          tracker.status,
			Complete:        tracker.status.IsComplete(),
			Incomplete:      tracker.status.IsIncomplete(),
			PageIndex:       -1,
			CollectedCount:  len(tracker.collected),
			BudgetRemaining: tracker.BudgetRemaining(),
			Cycle:           tracker.cycle,
			Error:           ErrTrackerDone,
		}
	}
	if err := page.Validate(); err != nil {
		return Progress{
			Status:          StatusInvalidPage,
			PageIndex:       -1,
			CollectedCount:  len(tracker.collected),
			BudgetRemaining: tracker.BudgetRemaining(),
			Error:           err,
		}
	}

	pins := page.Pinned
	if len(pins) == 0 {
		pins = make([]bool, len(page.Keys))
	}

	entryCursor := tracker.cursor
	nextCursor, hasNext := page.NextPageCursor()
	newKeys, newPinned, duplicates, duplicatePinned := tracker.classify(page.Keys, pins)

	newCursor := false
	if hasNext {
		if _, seen := tracker.seenCursors[nextCursor]; !seen {
			newCursor = true
		}
	}

	pageProgressed := len(newKeys) > 0 || newCursor
	tracker.progressed = tracker.progressed || pageProgressed

	record := PageRecord{
		Index:         len(tracker.records),
		Cursor:        entryCursor,
		NextCursor:    nextCursor,
		HasNext:       hasNext,
		Keys:          append([]string(nil), page.Keys...),
		Pinned:        append([]bool(nil), pins...),
		NewKeys:       append([]string(nil), newKeys...),
		NewPinnedKeys: append([]string(nil), newPinned...),
		DuplicateKeys: append([]string(nil), duplicates...),
		Progressed:    pageProgressed,
	}
	tracker.records = append(tracker.records, record)
	tracker.consumed++

	tracker.atCursor[entryCursor] = record.Index

	var cycle *CycleEvidence
	cursorRepeated := false
	if hasNext {
		if _, seen := tracker.seenCursors[nextCursor]; seen {
			cursorRepeated = true
			cycle = tracker.cycleEvidence(nextCursor, record.Index)
		} else {
			tracker.seenCursors[nextCursor] = struct{}{}
		}
		tracker.cursor = nextCursor
	}

	switch {
	case !hasNext:
		tracker.status = StatusComplete
	case cursorRepeated:
		tracker.status = StatusCursorCycle
		tracker.cycle = cycle
	case tracker.budget >= 0 && tracker.consumed >= tracker.budget:
		tracker.status = StatusBudgetExhausted
	default:
		tracker.status = StatusContinue
	}

	return Progress{
		Status:              tracker.status,
		Complete:            tracker.status.IsComplete(),
		Incomplete:          tracker.status.IsIncomplete(),
		Progressed:          pageProgressed,
		NewKeys:             cloneStrings(newKeys),
		NewPinnedKeys:       cloneStrings(newPinned),
		DuplicateKeys:       cloneStrings(duplicates),
		DuplicatePinnedKeys: cloneStrings(duplicatePinned),
		NewCursor:           newCursor,
		CursorRepeated:      cursorRepeated,
		NextCursor:          nextCursor,
		HasNext:             hasNext,
		PageIndex:           record.Index,
		CollectedCount:      len(tracker.collected),
		BudgetRemaining:     tracker.BudgetRemaining(),
		Cycle:               cycle,
	}
}

// Summary returns a snapshot of the tracker state.
func (tracker *Tracker) Summary() Summary {
	return Summary{
		Status:          tracker.status,
		Complete:        tracker.status.IsComplete(),
		Incomplete:      tracker.status.IsIncomplete(),
		CollectedKeys:   tracker.CollectedKeys(),
		Pages:           tracker.Pages(),
		Cycle:           tracker.cycle,
		Progressed:      tracker.progressed,
		Budget:          tracker.budget,
		Consumed:        tracker.consumed,
		BudgetRemaining: tracker.BudgetRemaining(),
	}
}

// Evaluate consumes pages in order until the tracker reaches a terminal
// status or the supplied pages run out. Running out of pages while the tracker
// still says Continue is not completion: Status remains Continue and
// Complete is false.
func Evaluate(pages []Page, budget int) Summary {
	tracker := NewTracker(budget)
	var invalid error
	for _, page := range pages {
		result := tracker.Observe(page)
		if result.Status == StatusInvalidPage {
			invalid = result.Error
			break
		}
		if result.Status.IsTerminal() {
			break
		}
	}
	summary := tracker.Summary()
	if invalid != nil {
		summary.Status = StatusInvalidPage
		summary.Complete = false
		summary.Error = invalid
	}
	return summary
}

// Run is an alias for Evaluate.
func Run(pages []Page, budget int) Summary { return Evaluate(pages, budget) }

func (tracker *Tracker) classify(keys []string, pins []bool) (newKeys, newPinned, duplicates, duplicatePinned []string) {
	for index, key := range keys {
		if _, seen := tracker.seenKeys[key]; seen {
			duplicates = append(duplicates, key)
			if pins[index] {
				duplicatePinned = append(duplicatePinned, key)
			}
			continue
		}
		tracker.seenKeys[key] = struct{}{}
		tracker.collected = append(tracker.collected, key)
		if pins[index] {
			newPinned = append(newPinned, key)
		} else {
			newKeys = append(newKeys, key)
		}
	}
	return newKeys, newPinned, duplicates, duplicatePinned
}

func (tracker *Tracker) cycleEvidence(target Cursor, current int) *CycleEvidence {
	start, ok := tracker.atCursor[target]
	if !ok || start < 0 || start > current {
		start = current
	}
	pages := cloneRecords(tracker.records[start : current+1])
	cursors := make([]Cursor, 0, len(pages)+1)
	cursors = append(cursors, target)
	edges := make([]CursorEdge, 0, len(pages))
	for _, record := range pages {
		cursors = append(cursors, record.NextCursor)
		edges = append(edges, CursorEdge{
			PageIndex: record.Index,
			From:      record.Cursor,
			To:        record.NextCursor,
		})
	}
	return &CycleEvidence{
		StartCursor: target,
		Cursors:     cursors,
		Pages:       pages,
		Edges:       edges,
	}
}

func cloneStrings(in []string) []string {
	return append([]string(nil), in...)
}

func cloneRecords(in []PageRecord) []PageRecord {
	out := make([]PageRecord, len(in))
	for index, record := range in {
		record.Keys = cloneStrings(record.Keys)
		record.Pinned = append([]bool(nil), record.Pinned...)
		record.NewKeys = cloneStrings(record.NewKeys)
		record.NewPinnedKeys = cloneStrings(record.NewPinnedKeys)
		record.DuplicateKeys = cloneStrings(record.DuplicateKeys)
		out[index] = record
	}
	return out
}

func (tracker *Tracker) String() string {
	return fmt.Sprintf("status=%s consumed=%d remaining=%d collected=%d",
		tracker.status, tracker.consumed, tracker.BudgetRemaining(), len(tracker.collected))
}
