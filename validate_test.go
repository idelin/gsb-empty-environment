package delaunay

import (
	"testing"
)

func squareOutput(t *testing.T) ([]RawPoint, *Output) {
	t.Helper()
	pts := []RawPoint{raw("a", 0, 0), raw("b", 2, 0), raw("c", 2, 2), raw("d", 0, 2)}
	out := Triangulate(pts)
	if err := Validate(pts, out); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}
	return pts, out
}

func TestValidatorCatchesFlippedTriangle(t *testing.T) {
	pts, out := squareOutput(t)
	tr := out.Triangles[0]
	out.Triangles[0] = [3]int{tr[0], tr[2], tr[1]} // clockwise
	if err := Validate(pts, out); err == nil {
		t.Fatal("flipped triangle not detected")
	}
}

func TestValidatorCatchesMissingTriangle(t *testing.T) {
	pts, out := squareOutput(t)
	out.Triangles = out.Triangles[:1]
	out.Adjacency = out.Adjacency[:1]
	if err := Validate(pts, out); err == nil {
		t.Fatal("missing triangle not detected")
	}
}

func TestValidatorCatchesBadAdjacency(t *testing.T) {
	pts, out := squareOutput(t)
	out.Adjacency[0][0] = -1 // break the shared-edge link
	if err := Validate(pts, out); err == nil {
		t.Fatal("bad adjacency not detected")
	}
}

func TestValidatorCatchesBadHull(t *testing.T) {
	pts, out := squareOutput(t)
	out.Hull = []int{out.Hull[1], out.Hull[0], out.Hull[2], out.Hull[3]}
	if err := Validate(pts, out); err == nil {
		t.Fatal("bad hull not detected")
	}
}

func TestValidatorCatchesNonDelaunay(t *testing.T) {
	// (1,1) is strictly inside the circumcircle of the other three.
	pts := []RawPoint{raw("a", 0, 0), raw("b", 4, 0), raw("c", 0, 4), raw("d", 1, 1)}
	out := &Output{
		Mode: ModeTriangulation,
		Points: []OutputPoint{
			{Index: 0, X: "0", Y: "0", IDs: []string{"a"}},
			{Index: 1, X: "4", Y: "0", IDs: []string{"b"}},
			{Index: 2, X: "0", Y: "4", IDs: []string{"c"}},
			{Index: 3, X: "1", Y: "1", IDs: []string{"d"}},
		},
		Triangles: [][3]int{{0, 1, 2}, {0, 3, 1}, {0, 2, 3}, {1, 3, 2}},
	}
	out.Adjacency = adjacencyOf(out.Triangles)
	out.Hull = hullOf(out.Triangles)
	err := Validate(pts, out)
	if err == nil {
		t.Fatal("non-Delaunay triangulation not detected")
	}
	// Triangle (0,1,2) = (0,0),(4,0),(0,4) has (1,1) strictly inside its
	// circumcircle; also the fan from (1,1) is the valid Delaunay here,
	// so the failure may fire on either check.
	t.Logf("detected: %v", err)
}

func TestValidatorCatchesWrongChainOrder(t *testing.T) {
	pts := []RawPoint{raw("a", 0, 0), raw("b", 1, 1), raw("c", 2, 2)}
	out := Triangulate(pts)
	out.Chain = []int{out.Chain[1], out.Chain[0], out.Chain[2]}
	if err := Validate(pts, out); err == nil {
		t.Fatal("unordered chain not detected")
	}
}

func TestValidatorCatchesDroppedID(t *testing.T) {
	pts, out := squareOutput(t)
	out.Points[0].IDs = out.Points[0].IDs[:0]
	if err := Validate(pts, out); err == nil {
		t.Fatal("dropped source ID not detected")
	}
}
