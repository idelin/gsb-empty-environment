package eqcube

import (
	"image/color"
	"math"
)

// defaultMaxSamples caps the adaptive sub-sample grid per output pixel.
const defaultMaxSamples = 8

// Options controls resampling. The zero value is usable.
type Options struct {
	// MaxSamples is the largest number of sub-samples per axis used to
	// cover one cubemap pixel's solid angle. Zero selects the default (8);
	// values below 1 are raised to 1.
	MaxSamples int
}

// Convert resamples pano into every face image in faces.
//
// For each output pixel the face rotation maps explicit local face
// coordinates (u, v) to a unit world direction. An adaptive sub-sample grid
// covers the pixel's footprint in direction space: its grid density is
// chosen from the angular span of the pixel corners relative to the source
// pixel spacing, so pixels near the equirectangular poles and longitude
// seam receive more samples. Each sub-sample is weighted by the solid angle
// of its sub-cell on the cube face; weights therefore sum to the pixel
// solid angle and the filter is normalized by area.
func Convert(pano *Equirect, faces []Face, opts *Options) {
	maxSamples := defaultMaxSamples
	if opts != nil && opts.MaxSamples > 0 {
		maxSamples = opts.MaxSamples
	}

	srcW, srcH := pano.Size()
	dPhi := 2 * math.Pi / float64(srcW)
	dTheta := math.Pi / float64(srcH)

	for _, face := range faces {
		b := face.Image.Bounds()
		fw, fh := b.Dx(), b.Dy()
		halfU := 1.0 / float64(fw)
		halfV := 1.0 / float64(fh)

		for j := b.Min.Y; j < b.Max.Y; j++ {
			for i := b.Min.X; i < b.Max.X; i++ {
				u := 2*(float64(i-b.Min.X)+0.5)/float64(fw) - 1
				v := 1 - 2*(float64(j-b.Min.Y)+0.5)/float64(fh)

				nU, nV := adaptiveCounts(face.Rotation, u, v, halfU, halfV, dPhi, dTheta, maxSamples)

				var sumR, sumG, sumB, sumA, weight float64
				stepU := 2 * halfU / float64(nU)
				stepV := 2 * halfV / float64(nV)

				for kv := 0; kv < nV; kv++ {
					v0 := v - halfV + float64(kv)*stepV
					v1 := v0 + stepV
					vc := (v0 + v1) / 2
					for ku := 0; ku < nU; ku++ {
						u0 := u - halfU + float64(ku)*stepU
						u1 := u0 + stepU
						uc := (u0 + u1) / 2

						dir := face.Rotation.Apply(Vec3{X: uc, Y: vc, Z: 1}).Normalize()
						c := pano.AtDirection(dir)
						w := rectSolidAngle(u0, u1, v0, v1)

						sumR += float64(c.R) * w
						sumG += float64(c.G) * w
						sumB += float64(c.B) * w
						sumA += float64(c.A) * w
						weight += w
					}
				}

				face.Image.SetNRGBA(i, j, color.NRGBA{
					R: uint8(math.Round(clamp(sumR/weight, 0, 255))),
					G: uint8(math.Round(clamp(sumG/weight, 0, 255))),
					B: uint8(math.Round(clamp(sumB/weight, 0, 255))),
					A: uint8(math.Round(clamp(sumA/weight, 0, 255))),
				})
			}
		}
	}
}

// adaptiveCounts chooses grid dimensions whose angular resolution is fine
// enough to cover the solid angle of one cubemap pixel.
func adaptiveCounts(r Mat3, u, v, halfU, halfV, dPhi, dTheta float64, max int) (nU, nV int) {
	corners := [4]Vec3{
		{X: u - halfU, Y: v - halfV, Z: 1},
		{X: u + halfU, Y: v - halfV, Z: 1},
		{X: u + halfU, Y: v + halfV, Z: 1},
		{X: u - halfU, Y: v + halfV, Z: 1},
	}

	minPhi, maxPhi := math.Inf(1), math.Inf(-1)
	minTheta, maxTheta := math.Inf(1), math.Inf(-1)
	var prevPhi, unwrapped float64
	minUnwrapped, maxUnwrapped := math.Inf(1), math.Inf(-1)
	for k, c := range corners {
		dir := r.Apply(c).Normalize()
		phi := math.Atan2(dir.Z, dir.X)
		if k == 0 {
			unwrapped = phi
		} else {
			// Walk the boundary in order so a footprint crossing the
			// +/-pi longitude seam is measured as one continuous span.
			unwrapped += wrapAngle(phi - prevPhi)
		}
		prevPhi = phi
		if unwrapped < minUnwrapped {
			minUnwrapped = unwrapped
		}
		if unwrapped > maxUnwrapped {
			maxUnwrapped = unwrapped
		}
		if phi < minPhi {
			minPhi = phi
		}
		if phi > maxPhi {
			maxPhi = phi
		}
		theta := math.Asin(clamp(dir.Y, -1, 1))
		if theta < minTheta {
			minTheta = theta
		}
		if theta > maxTheta {
			maxTheta = theta
		}
	}

	// The boundary-walk span is correct across the seam; the plain
	// min/max span is the right answer when no wrapping occurred.
	longitudeSpan := maxUnwrapped - minUnwrapped
	if s := maxPhi - minPhi; s > longitudeSpan {
		longitudeSpan = s
	}
	latitudeSpan := maxTheta - minTheta

	nU = clampInt(int(math.Ceil(longitudeSpan/dPhi)), 1, max)
	nV = clampInt(int(math.Ceil(latitudeSpan/dTheta)), 1, max)
	return nU, nV
}

// PixelSolidAngle returns the solid angle (steradians) subtended by pixel
// (x, y) of a face with the given pixel dimensions. Summing it over every
// pixel of all six faces yields 4*pi.
func PixelSolidAngle(faceWidth, faceHeight, x, y int) float64 {
	halfU := 1.0 / float64(faceWidth)
	halfV := 1.0 / float64(faceHeight)
	u := 2*(float64(x)+0.5)/float64(faceWidth) - 1
	v := 1 - 2*(float64(y)+0.5)/float64(faceHeight)
	return rectSolidAngle(u-halfU, u+halfU, v-halfV, v+halfV)
}

// rectSolidAngle integrates dOmega over an axis-aligned rectangle on the
// local face plane z = 1. The integrand is 1/(1+u^2+v^2)^(3/2) du dv; its
// antiderivative evaluated as an alternating corner sum is:
//
//	atan2(u*v, sqrt(1+u^2+v^2))
func rectSolidAngle(u0, u1, v0, v1 float64) float64 {
	term := func(u, v float64) float64 {
		return math.Atan2(u*v, math.Sqrt(1+u*u+v*v))
	}
	return term(u1, v1) - term(u0, v1) - term(u1, v0) + term(u0, v0)
}

// wrapAngle maps an angle difference to (-pi, pi].
func wrapAngle(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}
