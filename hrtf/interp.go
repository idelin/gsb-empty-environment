package hrtf

import "math"

// Response is the interpolated state for one ear at one sampling instant.
type Response struct {
	// FIR is the barycentric interpolation of the delay-removed FIRs.
	FIR []float64
	// Delay is the interpolated delay in (possibly fractional) samples.
	Delay float64
	// Loc is the shared triangle location used for both ears.
	Loc Location
}

// Engine interpolates a fixed mesh and drives two ear convolvers.
type Engine struct {
	mesh *Mesh
}

// NewEngine builds an interpolation engine over mesh.
func NewEngine(mesh *Mesh) *Engine {
	if mesh == nil {
		panic("hrtf: NewEngine requires a non-nil mesh")
	}
	return &Engine{mesh: mesh}
}

// Mesh returns the underlying mesh.
func (e *Engine) Mesh() *Mesh { return e.mesh }

// Locate exposes the triangle selection used by an interpolation, applying the
// fixed smallest-ID boundary rule.
func (e *Engine) Locate(dir Vec3) (Location, error) {
	return e.mesh.Locate(dir)
}

// Interpolate resolves one direction for both ears at the same sampling
// instant. A single triangle lookup is shared, guaranteeing that left and
// right use identical geometry and therefore coherent timing.
func (e *Engine) Interpolate(dir Vec3) (left, right Response, err error) {
	loc, err := e.mesh.Locate(dir)
	if err != nil {
		return Response{}, Response{}, err
	}
	return e.interpolateEar(Left, loc), e.interpolateEar(Right, loc), nil
}

// InterpolateEar evaluates a single ear for an already resolved location. It
// is useful for tests that need to compare two faces sharing an edge.
func (e *Engine) InterpolateEar(ear Channel, loc Location) Response {
	return e.interpolateEar(ear, loc)
}

func (e *Engine) interpolateEar(ear Channel, loc Location) Response {
	n := e.mesh.length
	fir := make([]float64, n)
	idx := loc.Vertices
	weights := [3]float64{loc.A, loc.B, loc.C}

	var delay float64
	for k, vi := range idx {
		w := weights[k]
		src := e.mesh.vertices[vi].channel(ear)
		delay += w * float64(src.Delay)
		for j := 0; j < n; j++ {
			fir[j] += w * src.FIR[j]
		}
	}

	return Response{FIR: fir, Delay: delay, Loc: loc}
}

func (v Vertex) channel(ear Channel) Filter {
	if ear == Right {
		return v.Right
	}
	return v.Left
}

// Impulse materializes the physical impulse response represented by r: the
// delay-removed FIR is re-delayed by r.Delay, where a fractional delay is
// realized by linear interpolation between the two adjacent samples.
//
// If D = floor(Delay) and frac = Delay - D, output index D+j receives
// (1-frac)*fir[j] and index D+j+1 receives frac*fir[j].
func (r Response) Impulse() []float64 {
	base := math.Floor(r.Delay)
	frac := r.Delay - base
	d := int(base)
	n := len(r.FIR)

	out := make([]float64, d+n+1)
	for j := 0; j < n; j++ {
		x := r.FIR[j]
		out[d+j] += (1 - frac) * x
		out[d+j+1] += frac * x
	}
	return out
}
