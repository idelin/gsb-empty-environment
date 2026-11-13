package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestRepairMinimumMovesAndLexicalTieBreak(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 10}
	coords := map[PhysicalPage]Location{
		1:   {Channel: 0, Bank: 0, Row: 5},
		2:   {Channel: 0, Bank: 0, Row: 4},
		3:   {Channel: 0, Bank: 0, Row: 6},
		100: {Channel: 0, Bank: 0, Row: 8},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, true),
		testPage(3, 3, Attacker, true),
	}
	layout := Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100},
		Limits:    limits,
		Locate:    testLocate(coords),
	}
	result, err := Repair(layout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Optimal || !result.StaticOptimal || !result.Online {
		t.Fatalf("expected a closed online optimum: %+v", result)
	}
	wantMoves := []Move{{Page: 2, From: 2, To: 100}}
	if !reflect.DeepEqual(result.Moves, wantMoves) || result.MoveCount != 1 {
		t.Fatalf("moves=%+v count=%d, want %+v", result.Moves, result.MoveCount, wantMoves)
	}
	if !reflect.DeepEqual(result.Sequence, wantMoves) {
		t.Fatalf("sequence=%+v", result.Sequence)
	}
	if !reflect.DeepEqual(result.Counts, []int64{1, 0}) {
		t.Fatalf("counts=%v", result.Counts)
	}
}

func TestRepairImmovableSensitiveMovesAttackers(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 8}
	coords := map[PhysicalPage]Location{
		1:   {Channel: 0, Bank: 0, Row: 4},
		2:   {Channel: 0, Bank: 0, Row: 3},
		3:   {Channel: 0, Bank: 0, Row: 5},
		100: {Channel: 0, Bank: 0, Row: 7},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, true),
		testPage(3, 3, Attacker, true),
	}
	result, err := Repair(Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100},
		Limits:    limits,
		Locate:    testLocate(coords),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.MoveCount != 1 || result.Moves[0].Page != 2 {
		t.Fatalf("expected the lexicographically first safe attacker: %+v", result.Moves)
	}
}

func TestRepairMovesNonInitialAttackerBeforeSensitive(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 12}
	coords := map[PhysicalPage]Location{
		1:   {Channel: 0, Bank: 0, Row: 4},
		2:   {Channel: 0, Bank: 0, Row: 7},
		10:  {Channel: 0, Bank: 0, Row: 5},
		11:  {Channel: 0, Bank: 0, Row: 5},
		12:  {Channel: 0, Bank: 0, Row: 3},
		13:  {Channel: 0, Bank: 0, Row: 8},
		14:  {Channel: 0, Bank: 0, Row: 10},
		100: {Channel: 0, Bank: 0, Row: 6},
		101: {Channel: 0, Bank: 0, Row: 9},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, true),
		// Page 2 is outside the initial witness set.  Its move is nevertheless
		// necessary because it blocks the sensitive page's only viable target.
		testPage(2, 2, Attacker, true),
		testPage(10, 10, Attacker, false),
		testPage(11, 11, Attacker, false),
		testPage(12, 12, Attacker, false),
		testPage(13, 13, Attacker, false),
		testPage(14, 14, Attacker, false),
	}
	layout := Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100, 101},
		Limits:    limits,
		Locate:    testLocate(coords),
	}
	initial, err := Audit(layout)
	if err != nil {
		t.Fatal(err)
	}
	if initial.WitnessCount != 2 {
		t.Fatalf("want two initial witnesses, got %+v", initial.Witnesses)
	}
	result, err := Repair(layout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.MoveCount != 2 {
		t.Fatalf("want two moves, got %+v", result)
	}
	wantMoves := []Move{
		{Page: 1, From: 1, To: 100},
		{Page: 2, From: 2, To: 101},
	}
	if !reflect.DeepEqual(result.Moves, wantMoves) {
		t.Fatalf("moves=%+v, want %+v", result.Moves, wantMoves)
	}
	if len(result.Sequence) != 2 || result.Sequence[0].Page != 2 || result.Sequence[1].Page != 1 {
		t.Fatalf("expected attacker 2 to move before sensitive 1: %+v", result.Sequence)
	}
	if !reflect.DeepEqual(result.Counts, []int64{2, 2, 0}) {
		t.Fatalf("strict witness counts=%v, want [2 2 0]", result.Counts)
	}
	if err := ValidateOnlineOrder(layout, result.Sequence); err != nil {
		t.Fatalf("returned sequence is invalid: %v", err)
	}
}

func TestFindOnlineOrderDetectsCircularBlocking(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 14}
	coords := map[PhysicalPage]Location{
		1:   {Channel: 0, Bank: 0, Row: 6},
		2:   {Channel: 0, Bank: 0, Row: 11},
		10:  {Channel: 0, Bank: 0, Row: 5},
		11:  {Channel: 0, Bank: 0, Row: 12},
		12:  {Channel: 0, Bank: 0, Row: 7},
		13:  {Channel: 0, Bank: 0, Row: 10},
		100: {Channel: 0, Bank: 0, Row: 10},
		101: {Channel: 0, Bank: 0, Row: 7},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Sensitive, false),
		testPage(10, 10, Attacker, true),
		testPage(11, 11, Attacker, true),
		testPage(12, 12, Attacker, false),
		testPage(13, 13, Attacker, false),
	}
	layout := Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100, 101},
		Limits:    limits,
		Locate:    testLocate(coords),
	}
	assignment := []Move{
		{Page: 10, From: 10, To: 100},
		{Page: 11, From: 11, To: 101},
	}
	if _, err := FindOnlineOrder(layout, assignment); !errors.Is(err, ErrNoOnlineOrder) {
		t.Fatalf("want ErrNoOnlineOrder, got %v", err)
	}
	if err := ValidateOnlineOrder(layout, assignment); !errors.Is(err, ErrNoOnlineOrder) {
		t.Fatalf("sequence validation should fail: %v", err)
	}

	// The same assignment is statically safe: each moved page leaves its old
	// witness and neither destination has both sides once both pages move.
	e := newEngine(mustScenario(t, layout))
	for _, m := range assignment {
		e.applyMove(e.s.pageByID[m.Page], m.To)
	}
	if e.w != 0 {
		t.Fatalf("test fixture is not statically safe: %d witnesses remain", e.w)
	}
}

func mustScenario(t *testing.T, layout Layout) *scenario {
	t.Helper()
	s, err := buildScenario(layout, true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRepairBudgetReturnsLowerBound(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 10}
	coords := map[PhysicalPage]Location{
		1:   {Channel: 0, Bank: 0, Row: 5},
		2:   {Channel: 0, Bank: 0, Row: 4},
		3:   {Channel: 0, Bank: 0, Row: 6},
		100: {Channel: 0, Bank: 0, Row: 9},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, true),
		testPage(3, 3, Attacker, true),
	}
	result, err := Repair(Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100},
		Limits:    limits,
		Locate:    testLocate(coords),
	}, &RepairOptions{Budget: 1})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	if !result.Exhausted {
		t.Fatalf("result should be marked exhausted: %+v", result)
	}
	if result.LowerBound != 1 || result.InitialCount != 1 {
		t.Fatalf("unexpected partial result: %+v", result)
	}
	if result.StaticSafe {
		t.Fatalf("budget-limited result has no safe assignment but claims StaticSafe: %+v", result)
	}
}

func TestRepairRejectsFullyImmovableWitness(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 8}
	coords := map[PhysicalPage]Location{
		1: {Channel: 0, Bank: 0, Row: 4},
		2: {Channel: 0, Bank: 0, Row: 3},
		3: {Channel: 0, Bank: 0, Row: 5},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, false),
		testPage(3, 3, Attacker, false),
	}
	_, err := Repair(Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100},
		Limits:    limits,
		Locate:    testLocate(coords),
	}, nil)
	if !errors.Is(err, ErrNoSafePlacement) {
		t.Fatalf("want ErrNoSafePlacement, got %v", err)
	}
}

func TestRepairSafeLayoutUsesNoMoves(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 8}
	coords := map[PhysicalPage]Location{
		1: {Channel: 0, Bank: 0, Row: 4},
		2: {Channel: 0, Bank: 0, Row: 2},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, true),
	}
	result, err := Repair(Layout{Pages: pages, Limits: limits, Locate: testLocate(coords)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.MoveCount != 0 || !result.Optimal || !result.Online {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !reflect.DeepEqual(result.Counts, []int64{0}) {
		t.Fatalf("counts=%v", result.Counts)
	}
}

func TestRepairRejectsInvalidMoveInputs(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 8}
	coords := map[PhysicalPage]Location{
		1: {Channel: 0, Bank: 0, Row: 4},
		2: {Channel: 0, Bank: 0, Row: 3},
	}
	pages := []Page{
		testPage(1, 1, Sensitive, false),
		testPage(2, 2, Attacker, true),
	}
	layout := Layout{Pages: pages, FreePages: []PhysicalPage{100}, Limits: limits, Locate: testLocate(coords)}
	if _, err := FindOnlineOrder(layout, []Move{{Page: 2, From: 99, To: 100}}); err == nil {
		t.Fatal("expected a From mismatch error")
	}
	if _, err := FindOnlineOrder(layout, []Move{{Page: 2, From: 2, To: 101}}); err == nil {
		t.Fatal("expected a non-free target error")
	}
}

func TestRepairEnforcesHostCaps(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 40}
	coords := make(map[PhysicalPage]Location)
	pages := []Page{
		testPage(0, 0, Sensitive, false),
		testPage(1, 1, Attacker, true),
		testPage(2, 2, Attacker, true),
	}
	coords[0] = Location{Channel: 0, Bank: 0, Row: 5}
	coords[1] = Location{Channel: 0, Bank: 0, Row: 4}
	coords[2] = Location{Channel: 0, Bank: 0, Row: 6}
	for id := 3; id <= 21; id++ {
		pages = append(pages, testPage(id, id, Attacker, true))
		coords[id] = Location{Channel: 0, Bank: 0, Row: 9 + id}
	}
	coords[100] = Location{Channel: 0, Bank: 0, Row: 39}
	_, err := Repair(Layout{
		Pages:     pages,
		FreePages: []PhysicalPage{100},
		Limits:    limits,
		Locate:    testLocate(coords),
	}, nil)
	if !errors.Is(err, ErrTooManyMovablePages) {
		t.Fatalf("want ErrTooManyMovablePages, got %v", err)
	}

	free := make([]PhysicalPage, 21)
	for i := range free {
		free[i] = 100 + i
	}
	_, err = Repair(Layout{
		Pages:     []Page{testPage(0, 0, Ordinary, false)},
		FreePages: free,
		Limits:    limits,
		Locate:    testLocate(map[PhysicalPage]Location{0: {Channel: 0, Bank: 0, Row: 1}}),
	}, nil)
	if !errors.Is(err, ErrTooManyFreePages) {
		t.Fatalf("want ErrTooManyFreePages, got %v", err)
	}
}

func TestRepairTwentyMovablePageBound(t *testing.T) {
	limits := Limits{Channels: 2, Banks: 1, Rows: 40}
	coords := make(map[PhysicalPage]Location)
	pages := make([]Page, 0, 30)
	for i := 0; i < 10; i++ {
		centre := 3 + 3*i
		pages = append(pages,
			testPage(i, i, Sensitive, false),
			testPage(20+2*i, 10+2*i, Attacker, true),
			testPage(21+2*i, 11+2*i, Attacker, true),
		)
		coords[i] = Location{Channel: 0, Bank: 0, Row: centre}
		coords[10+2*i] = Location{Channel: 0, Bank: 0, Row: centre - 1}
		coords[11+2*i] = Location{Channel: 0, Bank: 0, Row: centre + 1}
	}
	free := make([]PhysicalPage, 0, 10)
	for i := 0; i < 10; i++ {
		physical := 100 + i
		free = append(free, physical)
		// Channel 1 contains no sensitive pages, so these attacker
		// destinations are inert.
		coords[physical] = Location{Channel: 1, Bank: 0, Row: i}
	}
	result, err := Repair(Layout{
		Pages:     pages,
		FreePages: free,
		Limits:    limits,
		Locate:    testLocate(coords),
	}, &RepairOptions{Budget: 500_000})
	if err != nil {
		t.Fatalf("unexpected error after 500000-state budget: %v", err)
	}
	if result.MoveCount != 10 || !result.Optimal || !result.Online {
		t.Fatalf("want a closed 10-move plan, got %+v", result)
	}
	for i, move := range result.Moves {
		if move.Page != 20+2*i || move.To != 100+i {
			t.Fatalf("move %d=%+v, want page %d -> %d", i, move, 20+2*i, 100+i)
		}
	}
}
