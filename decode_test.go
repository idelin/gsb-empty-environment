package netsec

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

var errTestBoom = errors.New("test constructor failure")

type testObject struct {
	id ID
}

type borrowObservation struct {
	from        ID
	target      ID
	targetBuilt bool
	hasValue    bool
}

type testTracker struct {
	allocations    map[ID]int
	destructorHits map[ID]int
	selfFrees      map[ID]int
	constructOrder []ID
	destroyOrder   []ID
	borrows        []borrowObservation
	built          map[ID]bool
	bad            []string

	failEnabled bool
	failAt      int
}

func newTestTracker() *testTracker {
	return &testTracker{
		allocations:    make(map[ID]int),
		destructorHits: make(map[ID]int),
		selfFrees:      make(map[ID]int),
		built:          make(map[ID]bool),
	}
}

func (t *testTracker) construct(in Construction) (any, error) {
	callIndex := len(t.constructOrder)
	t.constructOrder = append(t.constructOrder, in.ID)
	t.allocations[in.ID]++

	for _, child := range in.Children {
		switch child.Kind {
		case Own:
			obj, ok := child.Value.(*testObject)
			if !ok || obj.id != child.ID {
				t.bad = append(t.bad, fmt.Sprintf("object %d own child %d has value %#v", in.ID, child.ID, child.Value))
			}
		case Borrow:
			t.borrows = append(t.borrows, borrowObservation{
				from:        in.ID,
				target:      child.ID,
				targetBuilt: t.built[child.ID],
				hasValue:    child.Value != nil,
			})
		default:
			t.bad = append(t.bad, fmt.Sprintf("object %d has edge kind %d", in.ID, child.Kind))
		}
	}

	if t.failEnabled && callIndex == t.failAt {
		// A failed construction is the constructor's responsibility to clean
		// up. The package must not invoke the destructor for it.
		t.selfFrees[in.ID]++
		return nil, errTestBoom
	}

	t.built[in.ID] = true
	return &testObject{id: in.ID}, nil
}

func (t *testTracker) destroy(id ID, _ string, value any) {
	t.destructorHits[id]++
	t.destroyOrder = append(t.destroyOrder, id)
	if obj, ok := value.(*testObject); !ok || obj.id != id {
		t.bad = append(t.bad, fmt.Sprintf("object %d has destructor value %#v", id, value))
	}
}

func (t *testTracker) checkTotals(tst *testing.T) {
	tst.Helper()
	for id, allocated := range t.allocations {
		freed := t.destructorHits[id] + t.selfFrees[id]
		if freed != allocated {
			tst.Errorf("object %d allocated %d times, released %d times", id, allocated, freed)
		}
	}
	for id, freed := range t.destructorHits {
		if freed > 1 {
			tst.Errorf("object %d destroyed %d times", id, freed)
		}
		if t.allocations[id] != 1 {
			tst.Errorf("object %d destroyed without exactly one allocation", id)
		}
	}
}

func testTree() ([]Record, TypeTable) {
	records := []Record{
		{ID: 1, Type: "root", Children: []ID{2, 3}},
		{ID: 2, Type: "mid", Children: []ID{4}},
		{ID: 3, Type: "leaf"},
		{ID: 4, Type: "leaf"},
	}
	types := TypeTable{
		"root": {Own, Own},
		"mid":  {Own},
		"leaf": nil,
	}
	return records, types
}

func TestDecodeConstructionOrderAndClose(t *testing.T) {
	records, types := testTree()
	tracker := newTestTracker()

	graph, err := Decode(records, types, 1, tracker.construct, tracker.destroy)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if graph == nil {
		t.Fatal("Decode returned a nil graph")
	}
	if graph.RootID() != 1 {
		t.Fatalf("RootID = %d, want 1", graph.RootID())
	}
	if graph.NodeCount() != 4 {
		t.Fatalf("NodeCount = %d, want 4", graph.NodeCount())
	}
	if root, ok := graph.RootValue().(*testObject); !ok || root.id != 1 {
		t.Fatalf("RootValue = %#v, want object 1", graph.RootValue())
	}

	wantConstruct := []ID{4, 2, 3, 1}
	if !slices.Equal(tracker.constructOrder, wantConstruct) {
		t.Fatalf("construction order = %v, want %v", tracker.constructOrder, wantConstruct)
	}
	if len(tracker.bad) != 0 {
		t.Fatalf("constructor input problems: %v", tracker.bad)
	}

	graph.Close()
	wantDestroy := []ID{1, 3, 2, 4}
	if !slices.Equal(tracker.destroyOrder, wantDestroy) {
		t.Fatalf("destruction order = %v, want %v", tracker.destroyOrder, wantDestroy)
	}

	graph.Close()
	graph.Close()
	if !slices.Equal(tracker.destroyOrder, wantDestroy) {
		t.Fatalf("repeated Close changed destruction order: %v", tracker.destroyOrder)
	}
	for _, id := range wantConstruct {
		if tracker.allocations[id] != 1 {
			t.Errorf("object %d allocations = %d, want 1", id, tracker.allocations[id])
		}
		if tracker.destructorHits[id] != 1 {
			t.Errorf("object %d destructor calls = %d, want 1", id, tracker.destructorHits[id])
		}
	}
	tracker.checkTotals(t)
}

func TestSameLayerUsesAscendingID(t *testing.T) {
	records := []Record{
		{ID: 1, Type: "root", Children: []ID{10, 3, 7}},
		{ID: 10, Type: "leaf"},
		{ID: 3, Type: "leaf"},
		{ID: 7, Type: "leaf"},
	}
	types := TypeTable{
		"root": {Own, Own, Own},
		"leaf": nil,
	}
	tracker := newTestTracker()

	graph, err := Decode(records, types, 1, tracker.construct, tracker.destroy)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []ID{3, 7, 10, 1}
	if !slices.Equal(tracker.constructOrder, want) {
		t.Fatalf("construction order = %v, want %v", tracker.constructOrder, want)
	}

	graph.Close()
	if wantClose := []ID{1, 10, 7, 3}; !slices.Equal(tracker.destroyOrder, wantClose) {
		t.Fatalf("destruction order = %v, want %v", tracker.destroyOrder, wantClose)
	}
	tracker.checkTotals(t)
}

func TestBorrowCycleAndStableBorrowIDs(t *testing.T) {
	records := []Record{
		{ID: 1, Type: "root", Children: []ID{2, 3}},
		{ID: 2, Type: "node", Children: []ID{3}},
		{ID: 3, Type: "node", Children: []ID{2}},
	}
	types := TypeTable{
		"root": {Own, Own},
		"node": {Borrow},
	}
	tracker := newTestTracker()

	graph, err := Decode(records, types, 1, tracker.construct, tracker.destroy)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if graph.NodeCount() != 3 {
		t.Fatalf("NodeCount = %d, want 3", graph.NodeCount())
	}
	if want := []ID{2, 3, 1}; !slices.Equal(tracker.constructOrder, want) {
		t.Fatalf("construction order = %v, want %v", tracker.constructOrder, want)
	}

	if len(tracker.borrows) != 2 {
		t.Fatalf("borrow observations = %d, want 2", len(tracker.borrows))
	}
	if tracker.borrows[0].from != 2 || tracker.borrows[0].target != 3 || tracker.borrows[0].targetBuilt {
		t.Fatalf("first borrow = %+v, want 2->3 with target not yet built", tracker.borrows[0])
	}
	if tracker.borrows[1].from != 3 || tracker.borrows[1].target != 2 || !tracker.borrows[1].targetBuilt {
		t.Fatalf("second borrow = %+v, want 3->2 with target already built", tracker.borrows[1])
	}
	for _, observation := range tracker.borrows {
		if observation.hasValue {
			t.Errorf("borrow %d->%d unexpectedly carried an object value", observation.from, observation.target)
		}
	}

	graph.Close()
	if want := []ID{1, 3, 2}; !slices.Equal(tracker.destroyOrder, want) {
		t.Fatalf("destruction order = %v, want %v", tracker.destroyOrder, want)
	}
	tracker.checkTotals(t)
}

func TestSharedOwnTargetIsRejectedBeforeConstruction(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
		types   TypeTable
	}{
		{
			name: "same parent fields",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2, 2}},
				{ID: 2, Type: "leaf"},
			},
			types: TypeTable{"root": {Own, Own}, "leaf": nil},
		},
		{
			name: "different parents",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2, 3}},
				{ID: 2, Type: "node", Children: []ID{4}},
				{ID: 3, Type: "node", Children: []ID{4}},
				{ID: 4, Type: "leaf"},
			},
			types: TypeTable{"root": {Own, Own}, "node": {Own}, "leaf": nil},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := newTestTracker()
			graph, err := Decode(test.records, test.types, 1, tracker.construct, tracker.destroy)
			if err == nil || graph != nil {
				t.Fatalf("Decode = (%v, %v), want a validation error", graph, err)
			}
			if !errors.Is(err, ErrSharedOwn) {
				t.Fatalf("error = %v, want ErrSharedOwn", err)
			}
			if len(tracker.constructOrder) != 0 || len(tracker.destroyOrder) != 0 {
				t.Fatalf("invalid graph invoked callbacks: construct=%v destroy=%v",
					tracker.constructOrder, tracker.destroyOrder)
			}
			tracker.checkTotals(t)
		})
	}
}

func TestConstructionFailureAtEveryPoint(t *testing.T) {
	records, types := testTree()
	fullOrder := []ID{4, 2, 3, 1}

	for failAt := range fullOrder {
		t.Run(fmt.Sprintf("fail-at-%d", failAt), func(t *testing.T) {
			tracker := newTestTracker()
			tracker.failEnabled = true
			tracker.failAt = failAt

			graph, err := Decode(records, types, 1, tracker.construct, tracker.destroy)
			if err == nil || graph != nil {
				t.Fatalf("Decode = (%v, %v), want construction failure", graph, err)
			}
			if !errors.Is(err, ErrConstructionFailed) {
				t.Fatalf("error = %v, want ErrConstructionFailed", err)
			}
			if !errors.Is(err, errTestBoom) {
				t.Fatalf("error = %v, want original constructor error", err)
			}

			if want := fullOrder[:failAt+1]; !slices.Equal(tracker.constructOrder, want) {
				t.Fatalf("construction order = %v, want %v", tracker.constructOrder, want)
			}

			wantDestroy := make([]ID, 0, failAt)
			for i := failAt - 1; i >= 0; i-- {
				wantDestroy = append(wantDestroy, fullOrder[i])
			}
			if !slices.Equal(tracker.destroyOrder, wantDestroy) {
				t.Fatalf("unwind order = %v, want %v", tracker.destroyOrder, wantDestroy)
			}

			failedID := fullOrder[failAt]
			if tracker.destructorHits[failedID] != 0 {
				t.Errorf("failed object %d was destroyed by the package", failedID)
			}
			if tracker.selfFrees[failedID] != 1 {
				t.Errorf("failed object %d self cleanup = %d, want 1", failedID, tracker.selfFrees[failedID])
			}
			tracker.checkTotals(t)
		})
	}
}

func TestPanickingConstructorUnwindsSuccessfullyBuiltObjects(t *testing.T) {
	records := []Record{
		{ID: 1, Type: "root", Children: []ID{2}},
		{ID: 2, Type: "leaf"},
	}
	types := TypeTable{"root": {Own}, "leaf": nil}
	tracker := newTestTracker()
	construct := func(in Construction) (any, error) {
		tracker.constructOrder = append(tracker.constructOrder, in.ID)
		tracker.allocations[in.ID]++
		if in.ID == 1 {
			panic("intentional constructor panic")
		}
		value := &testObject{id: in.ID}
		tracker.built[in.ID] = true
		return value, nil
	}

	graph, err := Decode(records, types, 1, construct, tracker.destroy)
	if err == nil || graph != nil {
		t.Fatalf("Decode = (%v, %v), want failure", graph, err)
	}
	if !errors.Is(err, ErrConstructionFailed) {
		t.Fatalf("error = %v, want ErrConstructionFailed", err)
	}
	if !errors.Is(err, ErrConstructionPanicked) {
		t.Fatalf("error = %v, want ErrConstructionPanicked", err)
	}
	if want := []ID{2}; !slices.Equal(tracker.destroyOrder, want) {
		t.Fatalf("unwind order = %v, want %v", tracker.destroyOrder, want)
	}
	// The callback itself owns the failed object and did not ask the package
	// to clean it up, so the test accounts for its release here.
	tracker.selfFrees[1]++
	tracker.checkTotals(t)
}

func TestInvalidGraphsNeverInvokeConstructionCallbacks(t *testing.T) {
	tooMany := make([]Record, 0, MaxNodes+1)
	for i := 0; i <= MaxNodes; i++ {
		tooMany = append(tooMany, Record{ID: ID(i), Type: "leaf"})
	}

	tests := []struct {
		name    string
		records []Record
		types   TypeTable
		root    ID
		want    error
	}{
		{
			name:    "too many nodes",
			records: tooMany,
			types:   TypeTable{"leaf": nil},
			root:    0,
			want:    ErrTooManyNodes,
		},
		{
			name: "duplicate ID",
			records: []Record{
				{ID: 1, Type: "leaf"},
				{ID: 1, Type: "leaf"},
			},
			types: TypeTable{"leaf": nil},
			root:  1,
			want:  ErrDuplicateID,
		},
		{
			name:    "ID overflow high",
			records: []Record{{ID: MaxID + 1, Type: "leaf"}},
			types:   TypeTable{"leaf": nil},
			root:    MaxID + 1,
			want:    ErrIDOutOfRange,
		},
		{
			name:    "ID overflow low",
			records: []Record{{ID: MinID - 1, Type: "leaf"}},
			types:   TypeTable{"leaf": nil},
			root:    MinID - 1,
			want:    ErrIDOutOfRange,
		},
		{
			name:    "child ID overflow",
			records: []Record{{ID: 1, Type: "root", Children: []ID{MaxID + 1}}},
			types:   TypeTable{"root": {Own}},
			root:    1,
			want:    ErrIDOutOfRange,
		},
		{
			name:    "root ID overflow",
			records: []Record{{ID: 1, Type: "leaf"}},
			types:   TypeTable{"leaf": nil},
			root:    MaxID + 1,
			want:    ErrIDOutOfRange,
		},
		{
			name:    "root missing",
			records: []Record{{ID: 1, Type: "leaf"}},
			types:   TypeTable{"leaf": nil},
			root:    2,
			want:    ErrRootNotFound,
		},
		{
			name:    "unknown type",
			records: []Record{{ID: 1, Type: "missing"}},
			types:   TypeTable{"leaf": nil},
			root:    1,
			want:    ErrUnknownType,
		},
		{
			name: "unknown type off root path",
			records: []Record{
				{ID: 1, Type: "leaf"},
				{ID: 2, Type: "missing"},
			},
			types: TypeTable{"leaf": nil},
			root:  1,
			want:  ErrUnknownType,
		},
		{
			name:    "edge count mismatch",
			records: []Record{{ID: 1, Type: "root"}},
			types:   TypeTable{"root": {Own}},
			root:    1,
			want:    ErrEdgeCountMismatch,
		},
		{
			name: "unknown edge kind",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2}},
				{ID: 2, Type: "leaf"},
			},
			types: TypeTable{"root": {EdgeKind(7)}, "leaf": nil},
			root:  1,
			want:  ErrUnknownEdgeKind,
		},
		{
			name:    "missing child",
			records: []Record{{ID: 1, Type: "root", Children: []ID{2}}},
			types:   TypeTable{"root": {Own}},
			root:    1,
			want:    ErrUnknownNode,
		},
		{
			name: "own cycle",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2}},
				{ID: 2, Type: "node", Children: []ID{1}},
			},
			types: TypeTable{"root": {Own}, "node": {Own}},
			root:  1,
			want:  ErrOwnCycle,
		},
		{
			name: "borrow target not own reachable",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2}},
				{ID: 2, Type: "node", Children: []ID{3}},
				{ID: 3, Type: "leaf"},
			},
			types: TypeTable{"root": {Own}, "node": {Borrow}, "leaf": nil},
			root:  1,
			want:  ErrInvalidBorrowTarget,
		},
		{
			name: "borrow target missing",
			records: []Record{
				{ID: 1, Type: "root", Children: []ID{2}},
				{ID: 2, Type: "node", Children: []ID{99}},
			},
			types: TypeTable{"root": {Own}, "node": {Borrow}},
			root:  1,
			want:  ErrUnknownNode,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := newTestTracker()
			graph, err := Decode(test.records, test.types, test.root, tracker.construct, tracker.destroy)
			if err == nil || graph != nil {
				t.Fatalf("Decode = (%v, %v), want an error", graph, err)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(tracker.constructOrder) != 0 {
				t.Fatalf("invalid graph invoked %d constructors: %v", len(tracker.constructOrder), tracker.constructOrder)
			}
			if len(tracker.destroyOrder) != 0 {
				t.Fatalf("invalid graph invoked %d destructors: %v", len(tracker.destroyOrder), tracker.destroyOrder)
			}
			tracker.checkTotals(t)
		})
	}
}

func TestNodeLimitBoundary(t *testing.T) {
	records := make([]Record, 0, MaxNodes)
	children := make([]ID, 0, MaxNodes-1)
	for i := 0; i < MaxNodes-1; i++ {
		records = append(records, Record{ID: ID(i), Type: "leaf"})
		children = append(children, ID(i))
	}
	rootID := ID(MaxNodes - 1)
	records = append(records, Record{ID: rootID, Type: "root", Children: children})
	rootEdges := make([]EdgeKind, 0, len(children))
	for range children {
		rootEdges = append(rootEdges, Own)
	}
	types := TypeTable{"root": rootEdges, "leaf": nil}

	tracker := newTestTracker()
	graph, err := Decode(records, types, rootID, tracker.construct, tracker.destroy)
	if err != nil {
		t.Fatalf("Decode at %d nodes: %v", MaxNodes, err)
	}
	if graph.NodeCount() != MaxNodes {
		t.Fatalf("NodeCount = %d, want %d", graph.NodeCount(), MaxNodes)
	}

	wantConstruct := make([]ID, 0, MaxNodes)
	for i := 0; i < MaxNodes-1; i++ {
		wantConstruct = append(wantConstruct, ID(i))
	}
	wantConstruct = append(wantConstruct, rootID)
	if !slices.Equal(tracker.constructOrder, wantConstruct) {
		t.Fatalf("construction order size = %d, want %d", len(tracker.constructOrder), len(wantConstruct))
	}

	graph.Close()
	if len(tracker.destroyOrder) != MaxNodes {
		t.Fatalf("destruction count = %d, want %d", len(tracker.destroyOrder), MaxNodes)
	}
	for i, id := range tracker.destroyOrder {
		if want := wantConstruct[MaxNodes-1-i]; id != want {
			t.Fatalf("destruction order[%d] = %d, want %d", i, id, want)
		}
	}
	tracker.checkTotals(t)
}

func TestNilCallbacksAreRejected(t *testing.T) {
	records, types := testTree()
	if _, err := Decode(records, types, 1, nil, func(ID, string, any) {}); !errors.Is(err, ErrNilConstructor) {
		t.Fatalf("nil constructor error = %v", err)
	}
	if _, err := Decode(records, types, 1, func(Construction) (any, error) { return nil, nil }, nil); !errors.Is(err, ErrNilDestructor) {
		t.Fatalf("nil destructor error = %v", err)
	}
}
