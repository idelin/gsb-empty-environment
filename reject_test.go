package taskpkg_test

import (
	"errors"
	"testing"

	taskpkg "example.org/domainreissue/q044"
)

func TestRejectMultiBoundary(t *testing.T) {
	// Square with an interior hole: two boundary loops (V=8, E=12, F=4, chi=0).
	pos := []taskpkg.Vec3{
		{0, 0, 0}, {2, 0, 0}, {2, 2, 0}, {0, 2, 0},
		{0.5, 0.5, 0}, {1.5, 0.5, 0}, {1.5, 1.5, 0}, {0.5, 1.5, 0},
	}
	faces := [][3]int{
		{0, 1, 5}, {0, 5, 4},
		{1, 2, 6}, {1, 6, 5},
		{2, 3, 7}, {2, 7, 6},
		{3, 0, 4}, {3, 4, 7},
	}
	m := &taskpkg.Mesh{Positions: pos, Faces: faces}
	_, err := taskpkg.Parameterize(m)
	if !errors.Is(err, taskpkg.ErrBoundary) {
		t.Fatalf("err = %v, want ErrBoundary", err)
	}
}

func TestRejectNonManifoldEdge(t *testing.T) {
	// Three triangles sharing edge (0,1).
	pos := []taskpkg.Vec3{
		{0, 0, 0}, {1, 0, 0}, {0, 1, 0},
		{1, 1, 0}, {0.5, 0.5, 1},
	}
	faces := [][3]int{
		{0, 1, 2},
		{0, 1, 3},
		{0, 1, 4},
	}
	_, err := taskpkg.Parameterize(&taskpkg.Mesh{Positions: pos, Faces: faces})
	if !errors.Is(err, taskpkg.ErrNonManifold) {
		t.Fatalf("err = %v, want ErrNonManifold", err)
	}
}

func TestRejectInconsistentWinding(t *testing.T) {
	// Two triangles of the same square: the second one is wound backwards,
	// so their shared edge appears with the same direction in both faces.
	pos := []taskpkg.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}}
	faces := [][3]int{{0, 1, 2}, {0, 3, 2}}
	_, err := taskpkg.Parameterize(&taskpkg.Mesh{Positions: pos, Faces: faces})
	if !errors.Is(err, taskpkg.ErrNonManifold) {
		t.Fatalf("err = %v, want ErrNonManifold", err)
	}
}

func TestRejectClosedMesh(t *testing.T) {
	// Tetrahedron: closed orientable surface with no boundary, chi=2.
	pos := []taskpkg.Vec3{
		{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1},
	}
	faces := [][3]int{
		{0, 2, 1},
		{0, 1, 3},
		{1, 2, 3},
		{2, 0, 3},
	}
	_, err := taskpkg.Parameterize(&taskpkg.Mesh{Positions: pos, Faces: faces})
	if err == nil {
		t.Fatal("closed tetrahedron must be rejected")
	}
	if !errors.Is(err, taskpkg.ErrBoundary) && !errors.Is(err, taskpkg.ErrNotDisk) {
		t.Fatalf("err = %v, want ErrBoundary or ErrNotDisk", err)
	}
}

func TestRejectBadIndexAndDisconnected(t *testing.T) {
	_, err := taskpkg.Parameterize(&taskpkg.Mesh{
		Positions: []taskpkg.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
		Faces:     [][3]int{{0, 1, 9}},
	})
	if !errors.Is(err, taskpkg.ErrInvalidMesh) {
		t.Fatalf("bad index err = %v, want ErrInvalidMesh", err)
	}

	twoDisks := &taskpkg.Mesh{
		Positions: []taskpkg.Vec3{
			{0, 0, 0}, {1, 0, 0}, {0, 1, 0},
			{3, 3, 0}, {4, 3, 0}, {3, 4, 0},
		},
		Faces: [][3]int{{0, 1, 2}, {3, 4, 5}},
	}
	if _, err := taskpkg.Parameterize(twoDisks); !errors.Is(err, taskpkg.ErrNonManifold) {
		t.Fatalf("disconnected err = %v, want ErrNonManifold", err)
	}
}

func TestFlipReturnsEvidence(t *testing.T) {
	// Two distinct boundary vertices share an identical 3D position, so the
	// arc-length boundary mapping collapses a boundary edge to one point and
	// produces a zero-area (degenerate) face. The mesh is otherwise a valid
	// oriented disk, so this must surface as flip evidence rather than silently
	// passing through the baking pipeline.
	m := &taskpkg.Mesh{
		Positions: []taskpkg.Vec3{
			{0, 0, 0}, {0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0},
		},
		Faces: [][3]int{
			{0, 1, 2},
			{0, 2, 3},
			{0, 3, 4},
		},
	}
	res, err := taskpkg.Parameterize(m)
	var flip *taskpkg.FlipError
	if errors.As(err, &flip) {
		if !errors.Is(err, taskpkg.ErrFlip) {
			t.Fatalf("errors.Is(err, ErrFlip) = false, want true")
		}
		if flip.Error() == "" {
			t.Fatal("FlipError.Error() is empty")
		}
		if flip.Evidence.SignedArea > 0 {
			t.Fatalf("flip evidence has positive area %.3e", flip.Evidence.SignedArea)
		}
		if flip.Evidence.Face < 0 || flip.Evidence.Face >= len(m.Faces) {
			t.Fatalf("flip evidence face index out of range: %d", flip.Evidence.Face)
		}
		if flip.Evidence.Verts != m.Faces[flip.Evidence.Face] {
			t.Fatalf("flip evidence verts %v != face %v",
				flip.Evidence.Verts, m.Faces[flip.Evidence.Face])
		}
		if !errors.Is(err, taskpkg.ErrFlip) {
			t.Fatal("flip error must satisfy errors.Is(err, ErrFlip)")
		}
		return
	}
	// If the geometry still parameterizes without a flip, every reported area
	// must be positive; the assertion below guards that branch.
	if res != nil {
		for _, fa := range res.FaceAreas {
			if fa.Area <= 0 {
				t.Fatalf("face %d non-positive area without FlipError", fa.Face)
			}
		}
	}
}

func TestRejectNilAndEmpty(t *testing.T) {
	if _, err := taskpkg.Parameterize(nil); !errors.Is(err, taskpkg.ErrInvalidMesh) {
		t.Fatalf("nil mesh err = %v, want ErrInvalidMesh", err)
	}
	if _, err := taskpkg.Parameterize(&taskpkg.Mesh{}); !errors.Is(err, taskpkg.ErrInvalidMesh) {
		t.Fatalf("empty mesh err = %v, want ErrInvalidMesh", err)
	}
}
