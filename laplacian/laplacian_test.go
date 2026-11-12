package laplacian

import (
	"math"
	"testing"
)

const testEps = 1e-10

// gridMesh builds a rows x cols quadrangulated triangle sheet in the XY plane.
func gridMesh(cols, rows int) (*Mesh, [][2]int) {
	verts := make([]Vec3, 0, cols*rows)
	grid := make([][2]int, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			grid[r*cols+c] = [2]int{c, r}
			verts = append(verts, Vec3{X: float64(c), Y: float64(r), Z: 0})
		}
	}

	faces := make([][3]int, 0, 2*(cols-1)*(rows-1))
	for r := 0; r < rows-1; r++ {
		for c := 0; c < cols-1; c++ {
			a := r*cols + c
			b := r*cols + c + 1
			d := (r+1)*cols + c
			e := (r+1)*cols + c + 1
			faces = append(faces, [3]int{a, b, d}, [3]int{b, e, d})
		}
	}
	return &Mesh{Vertices: verts, Faces: faces}, grid
}

func twoComponentMesh() *Mesh {
	return &Mesh{
		Vertices: []Vec3{
			{0, 0, 0}, {1, 0, 0}, {0, 1, 0},
			{5, 5, 5}, {6, 5, 5}, {5, 6, 5},
		},
		Faces: [][3]int{
			{0, 1, 2},
			{3, 4, 5},
		},
	}
}

func addVec(a, b Vec3) Vec3 {
	return Vec3{X: a.X + b.X, Y: a.Y + b.Y, Z: a.Z + b.Z}
}

func TestRejectsSingularAndInvalidInputs(t *testing.T) {
	t.Run("nil mesh", func(t *testing.T) {
		if _, err := NewEditor(nil, []int{0}, []Vec3{{}}); err != ErrInvalidMesh {
			t.Fatalf("got %v, want ErrInvalidMesh", err)
		}
	})

	t.Run("empty mesh", func(t *testing.T) {
		if _, err := NewEditor(&Mesh{}, []int{0}, []Vec3{{}}); err != ErrInvalidMesh {
			t.Fatalf("got %v, want ErrInvalidMesh", err)
		}
	})

	t.Run("bad face index", func(t *testing.T) {
		mesh := &Mesh{
			Vertices: []Vec3{{}, {}, {}},
			Faces:    [][3]int{{0, 1, 3}},
		}
		if _, err := NewEditor(mesh, []int{0}, []Vec3{{}}); err != ErrInvalidMesh {
			t.Fatalf("got %v, want ErrInvalidMesh", err)
		}
	})

	t.Run("degenerate face", func(t *testing.T) {
		mesh := &Mesh{
			Vertices: []Vec3{{}, {}, {}},
			Faces:    [][3]int{{0, 0, 1}},
		}
		if _, err := NewEditor(mesh, []int{0}, []Vec3{{}}); err != ErrInvalidMesh {
			t.Fatalf("got %v, want ErrInvalidMesh", err)
		}
	})

	t.Run("non-finite coordinate", func(t *testing.T) {
		mesh, _ := gridMesh(2, 2)
		mesh.Vertices[1].X = math.NaN()
		if _, err := NewEditor(mesh, []int{0}, []Vec3{{}}); err != ErrInvalidMesh {
			t.Fatalf("got %v, want ErrInvalidMesh", err)
		}
	})

	t.Run("no anchors", func(t *testing.T) {
		mesh, _ := gridMesh(3, 3)
		if _, err := NewEditor(mesh, nil, nil); err != ErrNoAnchors {
			t.Fatalf("got %v, want ErrNoAnchors", err)
		}
	})

	t.Run("anchor out of range", func(t *testing.T) {
		mesh, _ := gridMesh(3, 3)
		if _, err := NewEditor(mesh, []int{9}, []Vec3{{}}); err != ErrAnchorOutOfRange {
			t.Fatalf("got %v, want ErrAnchorOutOfRange", err)
		}
	})

	t.Run("target length mismatch", func(t *testing.T) {
		mesh, _ := gridMesh(3, 3)
		if _, err := NewEditor(mesh, []int{0, 1}, []Vec3{{}}); err != ErrAnchorOutOfRange {
			t.Fatalf("got %v, want ErrAnchorOutOfRange", err)
		}
	})

	t.Run("component without anchor", func(t *testing.T) {
		mesh := twoComponentMesh()
		_, err := NewEditor(mesh, []int{0, 1}, []Vec3{{}, {X: 1}})
		if err != ErrComponentWithoutAnchor {
			t.Fatalf("got %v, want ErrComponentWithoutAnchor", err)
		}
	})

	t.Run("isolated vertex must be anchored", func(t *testing.T) {
		mesh, _ := gridMesh(2, 2)
		mesh.Vertices = append(mesh.Vertices, Vec3{X: 9, Y: 9})
		_, err := NewEditor(mesh, []int{0}, []Vec3{{}})
		if err != ErrComponentWithoutAnchor {
			t.Fatalf("got %v, want ErrComponentWithoutAnchor", err)
		}
	})

	t.Run("singular pivot is reported", func(t *testing.T) {
		zero := buildCSC([]cooEntry{{0, 0, 0}, {1, 1, 0}, {1, 0, 0}}, 2, buildLowerDiscard)
		if _, err := factorLDL(zero); err == nil {
			t.Fatal("expected singularity error")
		}
	})
}
