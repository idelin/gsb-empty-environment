package taskpkg

import (
	"math"
	"testing"
)

func nearlyEqual(t *testing.T, got, want, tolerance float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %.12g, want %.12g (tol %.2g)", label, got, want, tolerance)
	}
}

func fillFaces(n, value float64) [FaceCount][]float64 {
	grids := [FaceCount][]float64{}
	for face := 0; face < FaceCount; face++ {
		grids[face] = make([]float64, int(n)*int(n))
		for index := range grids[face] {
			grids[face][index] = value
		}
	}
	return grids
}

func TestSolidAngleSumsToSphere(t *testing.T) {
	for _, n := range []int{1, 2, 3, 8, 32} {
		cube, err := NewCubeMap(n, fillFaces(float64(n), 1))
		if err != nil {
			t.Fatal(err)
		}
		total := 0.0
		for face := 0; face < FaceCount; face++ {
			for j := 0; j < n; j++ {
				for i := 0; i < n; i++ {
					omega := cube.SolidAngle(face, i, j)
					if omega <= 0 {
						t.Fatalf("non-positive solid angle face=%d i=%d j=%d", face, i, j)
					}
					total += omega
				}
			}
		}
		nearlyEqual(t, total, 4*math.Pi, 1e-12, "total solid angle")
	}

	// A single face covers one sixth of the sphere exactly.
	cube, _ := NewCubeMap(1, fillFaces(1, 1))
	nearlyEqual(t, cube.SolidAngle(FacePosX, 0, 0), 4*math.Pi/FaceCount, 1e-12, "single face solid angle")
}

func TestFaceFramesAreRotations(t *testing.T) {
	for face, frame := range faces {
		if length := frame.normal.Length(); math.Abs(length-1) > 1e-15 {
			t.Fatalf("face %d normal not unit: %.15g", face, length)
		}
		if length := frame.uAxis.Length(); math.Abs(length-1) > 1e-15 {
			t.Fatalf("face %d uAxis not unit", face)
		}
		if length := frame.vAxis.Length(); math.Abs(length-1) > 1e-15 {
			t.Fatalf("face %d vAxis not unit", face)
		}
		if dot := frame.normal.Dot(frame.uAxis); math.Abs(dot) > 1e-15 {
			t.Fatalf("face %d normal·uAxis = %.2g", face, dot)
		}
		if dot := frame.normal.Dot(frame.vAxis); math.Abs(dot) > 1e-15 {
			t.Fatalf("face %d normal·vAxis = %.2g", face, dot)
		}
		if dot := frame.uAxis.Dot(frame.vAxis); math.Abs(dot) > 1e-15 {
			t.Fatalf("face %d uAxis·vAxis = %.2g", face, dot)
		}
		det := frame.uAxis.Cross(frame.vAxis).Dot(frame.normal)
		nearlyEqual(t, det, 1, 1e-15, "frame determinant")

		// The four canonical corners map to unit directions.
		for _, u := range []float64{-1, 1} {
			for _, v := range []float64{-1, 1} {
				direction := frame.direction(u, v)
				nearlyEqual(t, direction.Length(), 1, 1e-15, "corner direction length")
			}
		}
	}
}

func TestBoundaryOwnership(t *testing.T) {
	n := 4
	cube, _ := NewCubeMap(n, fillFaces(float64(n), 1))
	sampler := NewSampler(cube, 1e-12)

	type boundaryCase struct {
		direction Vec3
		owner     int
	}
	cases := []boundaryCase{
		{Vec3{1, 1, 0}.Normalize(), FacePosX},
		{Vec3{1, -1, 0}.Normalize(), FacePosX},
		{Vec3{-1, 1, 0}.Normalize(), FaceNegX},
		{Vec3{-1, -1, 0}.Normalize(), FaceNegX},
		{Vec3{1, 0, 1}.Normalize(), FacePosX},
		{Vec3{-1, 0, 1}.Normalize(), FaceNegX},
		{Vec3{0, 1, 1}.Normalize(), FacePosY},
		{Vec3{0, -1, 1}.Normalize(), FaceNegY},
		{Vec3{0, 0, 1}, FacePosZ},
		{Vec3{0, 0, -1}, FaceNegZ},
	}
	for _, tc := range cases {
		face, u, v := locateDirection(tc.direction)
		if face != tc.owner {
			t.Fatalf("direction %v owned by face %d, want %d", tc.direction, face, tc.owner)
		}
		if math.Abs(u) > 1+1e-12 || math.Abs(v) > 1+1e-12 {
			t.Fatalf("canonical coords out of range u=%g v=%g", u, v)
		}
		// PDF must be defined and positive on the seam.
		if pdf := sampler.PDF(tc.direction); !(pdf > 0) {
			t.Fatalf("non-positive seam PDF %.12g for %v", pdf, tc.direction)
		}
	}
}

func TestRejectsInvalidInput(t *testing.T) {
	if _, err := NewCubeMap(0, fillFaces(1, 1)); err == nil {
		t.Fatal("expected error for zero resolution")
	}
	grids := fillFaces(2, 1)
	grids[FacePosZ][0] = -0.5
	if _, err := NewCubeMap(2, grids); err == nil {
		t.Fatal("expected error for negative luminance")
	}
	short := fillFaces(2, 1)
	short[FacePosY] = short[FacePosY][:3]
	if _, err := NewCubeMap(2, short); err == nil {
		t.Fatal("expected error for mismatched face size")
	}
}
