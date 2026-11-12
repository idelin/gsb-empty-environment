package eqcube

import (
	"image"
	"image/color"
	"math"
	"math/rand"
	"testing"
)

const channelEps = 6.0 // 8-bit quantization plus bilinear filter slack.

// directionPano paints every equirect pixel with the unit direction its
// center encodes, scaled from [-1,1] to [0,255]. Rotation-invariant checks
// can therefore recover the exact sampled direction from any face pixel.
func directionPano(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		theta := math.Pi*(float64(y)+0.5)/float64(h) - math.Pi/2
		for x := 0; x < w; x++ {
			phi := 2 * math.Pi * float64(x) / float64(w)
			d := Vec3{
				X: math.Cos(theta) * math.Cos(phi),
				Y: math.Sin(theta),
				Z: math.Cos(theta) * math.Sin(phi),
			}
			img.SetNRGBA(x, y, color.NRGBA{R: enc(d.X), G: enc(d.Y), B: enc(d.Z), A: 255})
		}
	}
	return img
}

func enc(v float64) uint8 {
	return uint8(math.Round((v + 1) * 255 / 2))
}

func dec(b uint8) float64 {
	return float64(b)*2/255 - 1
}

func allocFaces(size int) []Face {
	tmpl := DefaultFaces(size)
	faces := make([]Face, len(tmpl))
	for i, f := range tmpl {
		f.Image = image.NewNRGBA(image.Rect(0, 0, size, size))
		faces[i] = f
	}
	return faces
}

func faceDirection(f Face, x, y, size int) Vec3 {
	u := 2*(float64(x)+0.5)/float64(size) - 1
	v := 1 - 2*(float64(y)+0.5)/float64(size)
	return f.Rotation.Apply(Vec3{X: u, Y: v, Z: 1}).Normalize()
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestConstantImage(t *testing.T) {
	const w, h, size = 64, 32, 16
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	want := color.NRGBA{R: 30, G: 120, B: 200, A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetNRGBA(x, y, want)
		}
	}

	faces := allocFaces(size)
	Convert(NewEquirect(src), faces, nil)

	for _, f := range faces {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				got := f.Image.NRGBAAt(x, y)
				if abs(float64(got.R)-float64(want.R)) > 1 ||
					abs(float64(got.G)-float64(want.G)) > 1 ||
					abs(float64(got.B)-float64(want.B)) > 1 ||
					got.A != 255 {
					t.Fatalf("face %s (%d,%d) = %v, want %v", f.Name, x, y, got, want)
				}
			}
		}
	}
}

func TestDirectionEncoding(t *testing.T) {
	const w, h, size = 128, 64, 24
	faces := allocFaces(size)
	Convert(NewEquirect(directionPano(w, h)), faces, nil)

	for _, f := range faces {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				got := f.Image.NRGBAAt(x, y)
				want := faceDirection(f, x, y, size)
				if abs(dec(got.R)-want.X) > 0.05 ||
					abs(dec(got.G)-want.Y) > 0.05 ||
					abs(dec(got.B)-want.Z) > 0.05 {
					t.Fatalf("face %s (%d,%d) direction = (%.3f,%.3f,%.3f), want (%.3f,%.3f,%.3f)",
						f.Name, x, y, dec(got.R), dec(got.G), dec(got.B), want.X, want.Y, want.Z)
				}
			}
		}
	}
}

func TestRotationConsistency(t *testing.T) {
	const w, h, size = 128, 64, 24

	// 90 degree rotation about the Y axis: X -> -Z, Z -> X.
	rotY := Mat3{
		C0: Vec3{0, 0, 1},
		C1: Vec3{0, 1, 0},
		C2: Vec3{-1, 0, 0},
	}

	base := directionPano(w, h)
	rotated := image.NewNRGBA(base.Bounds())
	for y := 0; y < h; y++ {
		theta := math.Pi*(float64(y)+0.5)/float64(h) - math.Pi/2
		for x := 0; x < w; x++ {
			phi := 2 * math.Pi * float64(x) / float64(w)
			d := Vec3{
				X: math.Cos(theta) * math.Cos(phi),
				Y: math.Sin(theta),
				Z: math.Cos(theta) * math.Sin(phi),
			}
			sx, sy := directionPixel(base, rotY.Apply(d))
			rotated.SetNRGBA(x, y, base.NRGBAAt(sx, sy))
		}
	}

	faces := allocFaces(size)
	Convert(NewEquirect(rotated), faces, nil)

	for _, f := range faces {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				got := f.Image.NRGBAAt(x, y)
				want := rotY.Apply(faceDirection(f, x, y, size))
				if abs(dec(got.R)-want.X) > 0.07 ||
					abs(dec(got.G)-want.Y) > 0.07 ||
					abs(dec(got.B)-want.Z) > 0.07 {
					t.Fatalf("face %s (%d,%d) rotated direction = (%.3f,%.3f,%.3f), want (%.3f,%.3f,%.3f)",
						f.Name, x, y, dec(got.R), dec(got.G), dec(got.B), want.X, want.Y, want.Z)
				}
			}
		}
	}
}

// directionPixel returns the source pixel whose center covers direction d.
func directionPixel(img *image.NRGBA, d Vec3) (x, y int) {
	b := img.Bounds()
	phi := math.Atan2(d.Z, d.X)
	if phi < 0 {
		phi += 2 * math.Pi
	}
	theta := math.Asin(clamp(d.Y, -1, 1))
	x = int(phi / (2 * math.Pi) * float64(b.Dx()))
	y = int((theta + math.Pi/2) / math.Pi * float64(b.Dy()))
	return clampInt(x, 0, b.Dx()-1), clampInt(y, 0, b.Dy()-1)
}

func TestLongitudeSeam(t *testing.T) {
	const w, h = 128, 64

	// 1. Bilinear sampler must be periodic across the longitude border.
	pano := NewEquirect(directionPano(w, h))
	for _, theta := range []float64{-1.2, -0.3, 0.4, 1.3} {
		eps := 1e-4
		left := Vec3{
			X: math.Cos(theta) * math.Cos(-math.Pi+eps),
			Y: math.Sin(theta),
			Z: math.Cos(theta) * math.Sin(-math.Pi+eps),
		}
		right := Vec3{
			X: math.Cos(theta) * math.Cos(math.Pi-eps),
			Y: math.Sin(theta),
			Z: math.Cos(theta) * math.Sin(math.Pi-eps),
		}
		c1 := pano.AtDirection(left)
		c2 := pano.AtDirection(right)
		if abs(float64(c1.R)-float64(c2.R)) > channelEps ||
			abs(float64(c1.G)-float64(c2.G)) > channelEps ||
			abs(float64(c1.B)-float64(c2.B)) > channelEps {
			t.Fatalf("seam discontinuity at theta %.2f: %v vs %v", theta, c1, c2)
		}
	}

	// 2. A stripe painted on the phi=+/-pi meridian must survive the
	// projection without a broken line at the touching cube edges.
	stripe := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if dx := x; dx <= 6 || w-dx <= 6 {
				stripe.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	const size = 32
	faces := allocFaces(size)
	Convert(NewEquirect(stripe), faces, &Options{MaxSamples: 12})

	// The painted stripe wraps the equirectangular border at phi=0/2*pi,
	// i.e. the +X meridian. Sample several world directions along that
	// meridian and require every face whose footprint covers the
	// direction to return the same value: a broken longitude seam would
	// show up as disagreement between adjacent cube faces.
	for _, lat := range []float64{-0.8, -0.3, 0.0, 0.3, 0.8} {
		target := Vec3{X: math.Cos(lat), Y: math.Sin(lat), Z: 0}
		var ref color.NRGBA
		refSet := false
		for _, f := range faces {
			// Project target into the face's local frame. The face only
			// covers it when the forward component dominates.
			local := invertRotation(f.Rotation).Apply(target)
			if local.Z <= 0.5 {
				continue
			}
			u := local.X / local.Z
			v := local.Y / local.Z
			if math.Abs(u) > 1 || math.Abs(v) > 1 {
				continue
			}
			bx := clampInt(int((u+1)/2*float64(size)), 0, size-1)
			by := clampInt(int((1-v)/2*float64(size)), 0, size-1)
			got := f.Image.NRGBAAt(bx, by)
			if !refSet {
				ref, refSet = got, true
				continue
			}
			if abs(float64(got.R)-float64(ref.R)) > 24 {
				t.Fatalf("seam stripe broken at lat %.2f on face %s: %d vs %d",
					lat, f.Name, got.R, ref.R)
			}
		}
		if !refSet || ref.R < 120 {
			t.Fatalf("stripe lost at lat %.2f: ref=%v set=%v", lat, ref, refSet)
		}
	}
}

func invertRotation(m Mat3) Mat3 {
	// Transpose of an orthonormal rotation matrix.
	return Mat3{
		C0: Vec3{X: m.C0.X, Y: m.C1.X, Z: m.C2.X},
		C1: Vec3{X: m.C0.Y, Y: m.C1.Y, Z: m.C2.Y},
		C2: Vec3{X: m.C0.Z, Y: m.C1.Z, Z: m.C2.Z},
	}
}

func TestPoleClamp(t *testing.T) {
	pano := NewEquirect(directionPano(64, 32))
	for _, d := range []Vec3{{0, 1, 0}, {0, -1, 0}, {1e-12, 1, 1e-12}} {
		c := pano.AtDirection(d.Normalize())
		if c.A != 255 {
			t.Fatalf("pole sample returned invalid color %v", c)
		}
	}
}

func TestSolidAngleAndEnergy(t *testing.T) {
	const size = 32
	var total float64
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			total += PixelSolidAngle(size, size, x, y)
		}
	}
	total *= 6
	if abs(total-4*math.Pi) > 1e-10 {
		t.Fatalf("cubemap solid angle = %.10f, want 4*pi", total)
	}

	// Smooth direction-encoded panorama: resampled energy must match the
	// source within a small budget when integrated with solid angles.
	const w, h = 128, 64
	src := directionPano(w, h)
	faces := allocFaces(size)
	Convert(NewEquirect(src), faces, nil)

	srcEnergy := equirectEnergy(src)
	dstEnergy := cubeEnergy(faces, size)
	if rel := abs(dstEnergy-srcEnergy) / srcEnergy; rel > 0.02 {
		t.Fatalf("smooth energy error %.3f%% exceeds 2%% (src=%.1f dst=%.1f)",
			100*rel, srcEnergy, dstEnergy)
	}

	// High-frequency random source: adaptive sampling keeps the integrated
	// energy closer than single-sample nearest-pixel style resampling.
	rng := rand.New(rand.NewSource(1))
	noise := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(noise.Pix); i += 4 {
		v := uint8(rng.Intn(256))
		noise.Pix[i], noise.Pix[i+1], noise.Pix[i+2], noise.Pix[i+3] = v, v, v, 255
	}

	adaptive := allocFaces(size)
	Convert(NewEquirect(noise), adaptive, &Options{MaxSamples: 12})
	nearest := allocFaces(size)
	Convert(NewEquirect(noise), nearest, &Options{MaxSamples: 1})

	noiseSrc := equirectEnergy(noise)
	errAdaptive := abs(cubeEnergy(adaptive, size)-noiseSrc) / noiseSrc
	errNearest := abs(cubeEnergy(nearest, size)-noiseSrc) / noiseSrc
	if errAdaptive > 0.05 {
		t.Fatalf("adaptive noise energy error %.3f%% exceeds 5%%", 100*errAdaptive)
	}
	if errAdaptive > errNearest {
		t.Fatalf("adaptive sampling not helping: %.3f%% vs nearest %.3f%%",
			100*errAdaptive, 100*errNearest)
	}
}

func TestAdaptiveCounts(t *testing.T) {
	// Coarse source so even modest corner footprints span multiple
	// source pixels; fine panoramas trivially need one sample everywhere.
	const w, h, size = 8, 4, 32
	dPhi := 2 * math.Pi / w
	dTheta := math.Pi / h
	half := 1.0 / size

	cases := []struct {
		name      string
		faceIdx   int
		u, v      float64
		wantDense bool
	}{
		{"+X center", 0, 0, 0, false},
		{"+Y pole center", 2, 0, 0, true},
		{"-Y pole center", 3, 0, 0, true},
	}

	var centerGrid int
	for _, tc := range cases {
		face := DefaultFaces(size)[tc.faceIdx]
		nU, nV := adaptiveCounts(face.Rotation, tc.u, tc.v, half, half, dPhi, dTheta, 16)
		if nU < 1 || nV < 1 {
			t.Fatalf("%s produced non-positive sample count (%d,%d)", tc.name, nU, nV)
		}
		if !tc.wantDense {
			centerGrid = nU * nV
			continue
		}
		if centerGrid > 0 && nU*nV <= centerGrid {
			t.Fatalf("%s grid %dx%d no denser than center grid %d",
				tc.name, nU, nV, centerGrid)
		}
	}

	// MaxSamples is respected.
	poleFace := DefaultFaces(size)[2]
	nU, nV := adaptiveCounts(poleFace.Rotation, 0, 0, half, half, dPhi, dTheta, 4)
	if nU > 4 || nV > 4 {
		t.Fatalf("sample count (%d,%d) exceeds cap 4", nU, nV)
	}
}

func equirectEnergy(img *image.NRGBA) float64 {
	b := img.Bounds()
	var energy float64
	dPhi := 2 * math.Pi / float64(b.Dx())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		// Integrate cos(theta) dtheta over the pixel's latitude span.
		theta0 := math.Pi*float64(y-b.Min.Y)/float64(b.Dy()) - math.Pi/2
		theta1 := theta0 + math.Pi/float64(b.Dy())
		dOmega := dPhi * (math.Sin(theta1) - math.Sin(theta0))
		for x := b.Min.X; b.Max.X > x; x++ {
			c := img.NRGBAAt(x, y)
			lum := 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
			energy += lum * dOmega
		}
	}
	return energy
}

func cubeEnergy(faces []Face, size int) float64 {
	var energy float64
	for _, f := range faces {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				c := f.Image.NRGBAAt(x, y)
				lum := 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
				energy += lum * PixelSolidAngle(size, size, x, y)
			}
		}
	}
	return energy
}
