package budget

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
)

func bytesLeaf(name string, payload []byte) *Node {
	data := append([]byte(nil), payload...)
	return &Node{
		Name:   name,
		Decode: func(_ context.Context, w io.Writer) error { _, err := w.Write(data); return err },
	}
}

func container(name string, children ...*Node) *Node {
	return &Node{Name: name, Children: children}
}

func stdLimits() Limits {
	return Limits{MaxTotalBytes: 1 << 20, MaxDepth: 8, MaxLeaves: 128, MaxLiveBuffers: 4}
}

func TestUnfoldHappyPathOrdersLeavesAndChargesBytes(t *testing.T) {
	root := container("root",
		bytesLeaf("a", []byte("hello ")),
		container("c", bytesLeaf("b", []byte("world"))),
		bytesLeaf("d", []byte("!")),
	)

	res, err := Unfold(context.Background(), stdLimits(), root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := res.TotalBytes, int64(len("hello ")+len("world")+len("!")); got != want {
		t.Fatalf("TotalBytes = %d, want %d", got, want)
	}
	if len(res.Leaves) != 3 {
		t.Fatalf("leaf count = %d, want 3", len(res.Leaves))
	}
	wantNames := []string{"a", "b", "d"}
	for i, want := range wantNames {
		if res.Leaves[i].Name != want {
			t.Fatalf("leaf[%d].Name = %q, want %q", i, res.Leaves[i].Name, want)
		}
	}
	if got, want := string(res.Leaves[1].Data), "world"; got != want {
		t.Fatalf("leaf b data = %q, want %q", got, want)
	}
	if want := []string{"root", "c", "b"}; !equalStrings(res.Leaves[1].Path, want) {
		t.Fatalf("leaf b path = %v, want %v", res.Leaves[1].Path, want)
	}
}

func TestTotalByteBudgetRejectedBeforeWrite(t *testing.T) {
	var accepted int64
	root := &Node{
		Name: "bombshell",
		// Hostile callback: ignore any error and keep emitting.
		Decode: func(_ context.Context, w io.Writer) error {
			chunk := make([]byte, 64)
			for i := 0; i < 100; i++ {
				n, err := w.Write(chunk)
				atomic.AddInt64(&accepted, int64(n))
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
	limits := stdLimits()
	limits.MaxTotalBytes = 100

	res, err := Unfold(context.Background(), limits, root)
	if !errors.Is(err, ErrTotalBytesExceeded) {
		t.Fatalf("err = %v, want ErrTotalBytesExceeded", err)
	}
	if res != nil {
		t.Fatalf("result must be nil on failure, got %+v", res)
	}
	// At most the budget was ever accepted; no overshoot landed in a buffer.
	if got := atomic.LoadInt64(&accepted); got > limits.MaxTotalBytes {
		t.Fatalf("accepted %d bytes, exceeds budget %d", got, limits.MaxTotalBytes)
	}
}

func TestTotalByteBudgetExactBoundarySucceeds(t *testing.T) {
	limits := stdLimits()
	limits.MaxTotalBytes = 5
	root := bytesLeaf("x", []byte("12345"))

	res, err := Unfold(context.Background(), limits, root)
	if err != nil {
		t.Fatalf("boundary write should succeed, got %v", err)
	}
	if res.TotalBytes != 5 {
		t.Fatalf("TotalBytes = %d, want 5", res.TotalBytes)
	}
}

func TestDeclaredSizeIsNeverTrusted(t *testing.T) {
	// Declares 1 byte but actually emits 10. Byte budget must use real bytes.
	root := &Node{
		Name:         "liar",
		DeclaredSize: 1,
		Decode:       func(_ context.Context, w io.Writer) error { _, err := w.Write([]byte("0123456789")); return err },
	}

	// Budget 4 must reject the actual 10-byte write even though declared is 1.
	tight := stdLimits()
	tight.MaxTotalBytes = 4
	if _, err := Unfold(context.Background(), tight, root); !errors.Is(err, ErrTotalBytesExceeded) {
		t.Fatalf("tight budget: err = %v, want ErrTotalBytesExceeded", err)
	}

	// Budget 20 must succeed and report the true size, not the declared one.
	loose := stdLimits()
	loose.MaxTotalBytes = 20
	res, err := Unfold(context.Background(), loose, root)
	if err != nil {
		t.Fatalf("loose budget: %v", err)
	}
	if res.TotalBytes != 10 {
		t.Fatalf("TotalBytes = %d, want 10 (declared size ignored)", res.TotalBytes)
	}
}

func TestDepthBudgetEnforced(t *testing.T) {
	// root(0) -> a(1) -> b(2) -> leaf(3)
	root := container("root", container("a", container("b", bytesLeaf("leaf", []byte("x")))))

	limits := stdLimits()
	limits.MaxDepth = 2
	if _, err := Unfold(context.Background(), limits, root); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("err = %v, want ErrDepthExceeded", err)
	}

	limits.MaxDepth = 3
	res, err := Unfold(context.Background(), limits, root)
	if err != nil {
		t.Fatalf("within-depth unfold failed: %v", err)
	}
	if res.Leaves[0].Name != "leaf" {
		t.Fatalf("unexpected leaf %q", res.Leaves[0].Name)
	}
}

func TestLeafCountBudgetEnforcedPerReference(t *testing.T) {
	shared := bytesLeaf("shared", []byte("ab")) // 2 bytes each
	root := container("root", shared, shared, shared)

	limits := stdLimits()
	limits.MaxLeaves = 2
	if _, err := Unfold(context.Background(), limits, root); !errors.Is(err, ErrLeafLimitExceeded) {
		t.Fatalf("err = %v, want ErrLeafLimitExceeded", err)
	}
}

func TestSharedNodeChargedOnEveryReference(t *testing.T) {
	shared := bytesLeaf("shared", []byte("1234")) // 4 bytes per reference
	root := container("root", shared, shared, shared)

	res, err := Unfold(context.Background(), stdLimits(), root)
	if err != nil {
		t.Fatalf("unfold: %v", err)
	}
	if res.TotalBytes != 12 {
		t.Fatalf("TotalBytes = %d, want 12 (3 refs * 4 bytes)", res.TotalBytes)
	}
	if len(res.Leaves) != 3 {
		t.Fatalf("leaves = %d, want 3 references", len(res.Leaves))
	}
}

func TestCycleRejectedImmediately(t *testing.T) {
	root := container("root")
	loop := container("loop")
	back := container("back")
	root.Children = []*Node{loop}
	loop.Children = []*Node{back}
	back.Children = []*Node{loop} // back -> loop -> back ...

	if _, err := Unfold(context.Background(), stdLimits(), root); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
}

func TestDAGIsNotMistakenForCycle(t *testing.T) {
	shared := bytesLeaf("s", []byte("x"))
	root := container("root",
		container("left", shared),
		container("right", shared),
	)
	res, err := Unfold(context.Background(), stdLimits(), root)
	if err != nil {
		t.Fatalf("DAG should be legal: %v", err)
	}
	if len(res.Leaves) != 2 {
		t.Fatalf("leaves = %d, want 2", len(res.Leaves))
	}
}

func TestInvalidConfigAndTree(t *testing.T) {
	cases := []Limits{
		{MaxTotalBytes: 0, MaxDepth: 1, MaxLeaves: 1, MaxLiveBuffers: 1},
		{MaxTotalBytes: 1, MaxDepth: -1, MaxLeaves: 1, MaxLiveBuffers: 1},
		{MaxTotalBytes: 1, MaxDepth: 1, MaxLeaves: 0, MaxLiveBuffers: 1},
		{MaxTotalBytes: 1, MaxDepth: 1, MaxLeaves: 1, MaxLiveBuffers: 0},
	}
	for i, l := range cases {
		if _, err := Unfold(context.Background(), l, bytesLeaf("x", nil)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: err = %v, want ErrInvalidConfig", i, err)
		}
	}

	if _, err := Unfold(context.Background(), stdLimits(), nil); !errors.Is(err, ErrInvalidTree) {
		t.Fatalf("nil root: err = %v, want ErrInvalidTree", err)
	}

	both := &Node{Name: "both", Decode: func(context.Context, io.Writer) error { return nil }, Children: []*Node{bytesLeaf("c", nil)}}
	if _, err := Unfold(context.Background(), stdLimits(), both); !errors.Is(err, ErrInvalidTree) {
		t.Fatalf("hybrid node: err = %v, want ErrInvalidTree", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
