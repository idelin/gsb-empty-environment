package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"runtime"
)

// ObjectID identifies a render object. The alias keeps frame literals and
// motion tables convenient.
type ObjectID = uint32

// RGBA is a linear, premultiplied color. R, G, B and A are not gamma encoded.
type RGBA struct {
	R, G, B, A float64
}

// Color is an alias for RGBA.
type Color = RGBA

// ZeroRGBA returns a fully transparent black color.
func ZeroRGBA() RGBA {
	return RGBA{}
}

// Finite reports whether all channels are finite and non-negative alpha.
func (c RGBA) Finite() bool {
	return isFinite(c.R) && isFinite(c.G) && isFinite(c.B) && isFinite(c.A)
}

// Camera describes one frame's world-to-view and view-to-clip transforms.
//
// View is right handed and looks down -Z. Depth buffers store positive
// camera-space depth (the distance along the camera's forward axis), so a
// world point p has depth -View.Z at p. Near and Far are in the same units.
type Camera struct {
	View       Mat4
	Projection Mat4
	Near       float64
	Far        float64
}

// ViewProjection returns Projection*View.
func (c Camera) ViewProjection() Mat4 {
	return c.Projection.Mul(c.View)
}

// InverseViewProjection returns the inverse of Projection*View.
func (c Camera) InverseViewProjection() (Mat4, bool) {
	return c.ViewProjection().Inverse()
}

// Validate checks the camera matrices and clipping planes.
func (c Camera) Validate() error {
	if !c.View.Finite() {
		return fmt.Errorf("%w: camera view is not finite", ErrInvalidCamera)
	}
	if !c.Projection.Finite() {
		return fmt.Errorf("%w: camera projection is not finite", ErrInvalidCamera)
	}
	if !isFinite(c.Near) || !isFinite(c.Far) || c.Near < 0 || c.Far <= c.Near {
		return fmt.Errorf("%w: invalid near/far (%g, %g)", ErrInvalidCamera, c.Near, c.Far)
	}
	if _, ok := c.InverseViewProjection(); !ok {
		return fmt.Errorf("%w: view-projection is singular", ErrInvalidCamera)
	}
	if _, ok := c.Projection.Inverse(); !ok {
		return fmt.Errorf("%w: projection is singular", ErrInvalidCamera)
	}
	if _, ok := c.View.Inverse(); !ok {
		return fmt.Errorf("%w: view is singular", ErrInvalidCamera)
	}
	return nil
}

// ndcDepthAt converts a positive camera-space depth to normalized device
// depth using the camera projection.
func (c Camera) ndcDepthAt(depth float64) (float64, bool) {
	if !isFinite(depth) || depth <= 0 {
		return 0, false
	}
	z := -depth
	clipZ := c.Projection[2][2]*z + c.Projection[2][3]
	clipW := c.Projection[3][2]*z + c.Projection[3][3]
	if !isFinite(clipZ) || !isFinite(clipW) || math.Abs(clipW) <= 1e-15 {
		return 0, false
	}
	ndcZ := clipZ / clipW
	if !isFinite(ndcZ) {
		return 0, false
	}
	return ndcZ, true
}

// UnprojectPixel reconstructs a world-space point from the center of pixel
// (x, y) and a positive camera-space depth.
func (c Camera) UnprojectPixel(x, y, width, height int, depth float64) (Vec3, bool) {
	if width <= 0 || height <= 0 || x < 0 || y < 0 || x >= width || y >= height {
		return Vec3{}, false
	}
	invVP, ok := c.InverseViewProjection()
	if !ok {
		return Vec3{}, false
	}
	ndcZ, ok := c.ndcDepthAt(depth)
	if !ok {
		return Vec3{}, false
	}
	ndcX := (2.0*(float64(x)+0.5))/float64(width) - 1.0
	ndcY := 1.0 - (2.0*(float64(y)+0.5))/float64(height)
	p := invVP.MulVec(Vec4{X: ndcX, Y: ndcY, Z: ndcZ, W: 1})
	if !isFinite(p.W) || math.Abs(p.W) <= 1e-15 {
		return Vec3{}, false
	}
	w := Vec3{X: p.X / p.W, Y: p.Y / p.W, Z: p.Z / p.W}
	if !w.Finite() {
		return Vec3{}, false
	}
	// The reconstructed point must agree with the supplied camera depth.
	view := c.View.MulVec(Vec4{X: w.X, Y: w.Y, Z: w.Z, W: 1})
	if math.Abs(-view.Z-depth) > 1e-6*math.Max(1, math.Abs(depth)) {
		return Vec3{}, false
	}
	return w, true
}

// ProjectWorld projects a world point to continuous pixel coordinates. Pixel
// integer i has its center at i+0.5, so an exactly centered projection yields
// an integer coordinate in the returned (sx, sy) pair.
//
// The returned depth is positive camera-space depth. The final bool is false
// for points at or behind the near plane or outside the projective domain.
func (c Camera) ProjectWorld(world Vec3, width, height int, nearEpsilon float64) (Vec2, float64, bool) {
	if width <= 0 || height <= 0 {
		return Vec2{}, 0, false
	}
	view := c.View.MulVec(Vec4{X: world.X, Y: world.Y, Z: world.Z, W: 1})
	depth := -view.Z
	if !isFinite(depth) || depth <= c.Near+nearEpsilon {
		return Vec2{}, 0, false
	}
	clip := c.Projection.MulVec(view)
	if !isFinite(clip.X) || !isFinite(clip.Y) || !isFinite(clip.Z) ||
		!isFinite(clip.W) || clip.W <= 1e-15 {
		return Vec2{}, 0, false
	}
	ndcX := clip.X / clip.W
	ndcY := clip.Y / clip.W
	if !isFinite(ndcX) || !isFinite(ndcY) {
		return Vec2{}, 0, false
	}
	sx := ((ndcX + 1.0) * 0.5 * float64(width)) - 0.5
	sy := ((1.0 - ndcY) * 0.5 * float64(height)) - 0.5
	if !isFinite(sx) || !isFinite(sy) {
		return Vec2{}, 0, false
	}
	return Vec2{X: sx, Y: sy}, depth, true
}

// NewOrthographicCamera builds a camera useful for synthetic tests and for
// pipelines that work in pixel-aligned world units. World X spans [0,width],
// world Y spans [0,height] with Y increasing downward, and world Z is
// -depth. The view matrix is identity.
func NewOrthographicCamera(width, height int, near, far float64) (Camera, error) {
	if width <= 0 || height <= 0 {
		return Camera{}, fmt.Errorf("%w: invalid viewport %dx%d", ErrInvalidCamera, width, height)
	}
	if !isFinite(near) || !isFinite(far) || near < 0 || far <= near {
		return Camera{}, fmt.Errorf("%w: invalid near/far (%g, %g)", ErrInvalidCamera, near, far)
	}
	w := float64(width)
	h := float64(height)
	projection := Mat4{
		{2 / w, 0, 0, -1},
		{0, -2 / h, 0, 1},
		{0, 0, -2 / (far - near), -(far + near) / (far - near)},
		{0, 0, 0, 1},
	}
	camera := Camera{
		View:       Identity4(),
		Projection: projection,
		Near:       near,
		Far:        far,
	}
	return camera, camera.Validate()
}

// Frame is one raster frame. All slices have Width*Height elements in row-major
// order. Depth and normals describe the current visible surface. Color is
// linear premultiplied RGBA. ObjectIDs and Generations identify that surface.
//
// Frame is treated as immutable input by the reprojector. Use Clone before
// publishing a buffer to more than one consumer.
type Frame struct {
	Width, Height int
	Camera        Camera

	Depth       []float64 // positive camera-space depth
	Color       []RGBA    // linear premultiplied color
	Normals     []Vec3    // world-space unit normals
	ObjectIDs   []ObjectID
	Generations []uint32
}

// Index returns the row-major slice index for pixel (x, y), or -1 when the
// coordinate is outside the frame.
func (f Frame) Index(x, y int) int {
	if x < 0 || y < 0 || x >= f.Width || y >= f.Height {
		return -1
	}
	return y*f.Width + x
}

// Clone makes a deep copy of the frame.
func (f Frame) Clone() Frame {
	out := f
	out.Depth = append([]float64(nil), f.Depth...)
	out.Color = append([]RGBA(nil), f.Color...)
	out.Normals = append([]Vec3(nil), f.Normals...)
	out.ObjectIDs = append([]ObjectID(nil), f.ObjectIDs...)
	out.Generations = append([]uint32(nil), f.Generations...)
	return out
}

// Validate checks shape and slice lengths. It does not reject non-finite
// per-pixel values: those pixels are rejected locally by the reprojector.
func (f Frame) Validate() error {
	if f.Width <= 0 || f.Height <= 0 {
		return fmt.Errorf("%w: invalid dimensions %dx%d", ErrInvalidFrame, f.Width, f.Height)
	}
	n := f.Width * f.Height
	if len(f.Depth) != n {
		return fmt.Errorf("%w: depth length %d, want %d", ErrInvalidFrame, len(f.Depth), n)
	}
	if len(f.Color) != n {
		return fmt.Errorf("%w: color length %d, want %d", ErrInvalidFrame, len(f.Color), n)
	}
	if len(f.Normals) != n {
		return fmt.Errorf("%w: normal length %d, want %d", ErrInvalidFrame, len(f.Normals), n)
	}
	if len(f.ObjectIDs) != n {
		return fmt.Errorf("%w: object id length %d, want %d", ErrInvalidFrame, len(f.ObjectIDs), n)
	}
	if len(f.Generations) != n {
		return fmt.Errorf("%w: generation length %d, want %d", ErrInvalidFrame, len(f.Generations), n)
	}
	if err := f.Camera.Validate(); err != nil {
		return err
	}
	return nil
}

// Config contains the evidence thresholds and execution controls.
//
// A completely zero Config selects DefaultConfig. Otherwise all fields are
// interpreted literally. MaxNormalAngle is in radians; a negative value
// disables the normal test (useful only for debugging).
type Config struct {
	// DepthTolerance is the absolute allowed difference between a reprojected
	// point's current camera depth and the current pixel's depth.
	DepthTolerance float64
	// RelativeDepthTolerance is added to DepthTolerance as
	// RelativeDepthTolerance*|currentDepth|.
	RelativeDepthTolerance float64
	// MaxNormalAngle is the largest allowed angle between the transformed
	// previous normal and the current normal.
	MaxNormalAngle float64
	// AlphaEpsilon rejects historical colors whose alpha is at or below this
	// value. Current pixels with alpha<=AlphaEpsilon resolve to zero.
	AlphaEpsilon float64
	// CurrentWeight is the weight of the current pixel in the temporal average.
	// It must be non-negative; zero makes the resolved history dominant.
	CurrentWeight float64
	// NearEpsilon is the strictness margin for near-plane rejection.
	NearEpsilon float64
	// TilesX and TilesY split resolution into independently processed tiles.
	TilesX, TilesY int
	// Workers limits concurrent tiles. Zero means GOMAXPROCS.
	Workers int
}

// DefaultConfig returns conservative geometric-evidence defaults.
func DefaultConfig() Config {
	return Config{
		DepthTolerance:         0.02,
		RelativeDepthTolerance: 0.01,
		MaxNormalAngle:         30.0 * math.Pi / 180.0,
		AlphaEpsilon:           0,
		CurrentWeight:          1,
		NearEpsilon:            1e-9,
		TilesX:                 1,
		TilesY:                 1,
		Workers:                0,
	}
}

func normalizeConfig(c Config) (Config, error) {
	if c == (Config{}) {
		c = DefaultConfig()
	}
	if !isFinite(c.DepthTolerance) || c.DepthTolerance < 0 {
		return Config{}, fmt.Errorf("%w: negative or non-finite depth tolerance", ErrInvalidConfig)
	}
	if !isFinite(c.RelativeDepthTolerance) || c.RelativeDepthTolerance < 0 {
		return Config{}, fmt.Errorf("%w: negative or non-finite relative depth tolerance", ErrInvalidConfig)
	}
	if !isFinite(c.MaxNormalAngle) || c.MaxNormalAngle > math.Pi+1e-12 {
		return Config{}, fmt.Errorf("%w: normal angle must be finite and <= pi", ErrInvalidConfig)
	}
	if !isFinite(c.AlphaEpsilon) || c.AlphaEpsilon < 0 {
		return Config{}, fmt.Errorf("%w: negative or non-finite alpha epsilon", ErrInvalidConfig)
	}
	if !isFinite(c.CurrentWeight) || c.CurrentWeight < 0 {
		return Config{}, fmt.Errorf("%w: negative or non-finite current weight", ErrInvalidConfig)
	}
	if !isFinite(c.NearEpsilon) || c.NearEpsilon < 0 {
		return Config{}, fmt.Errorf("%w: negative or non-finite near epsilon", ErrInvalidConfig)
	}
	if c.TilesX <= 0 {
		c.TilesX = 1
	}
	if c.TilesY <= 0 {
		c.TilesY = 1
	}
	if c.Workers < 0 {
		return Config{}, fmt.Errorf("%w: negative worker count", ErrInvalidConfig)
	}
	if c.Workers == 0 {
		c.Workers = runtime.GOMAXPROCS(0)
	}
	return c, nil
}

// ReprojectionStats counts local rejections. Target counters count
// per-source-per-destination bilinear contributions, not pixels.
type ReprojectionStats struct {
	SourcePixels              int
	SourceAlphaZero           int
	SourceInvalidColor        int
	SourceInvalidDepth        int
	SourceNearPlane           int
	SourceInvalidTransform    int
	SourceNegativeScale       int
	SourceInvalidNormal       int
	SourceBackprojectFailed   int
	SourceForwardProjectFail  int
	RejectedOutOfBounds       int
	RejectedTargetAlphaZero   int
	RejectedTargetColor       int
	RejectedTargetDepth       int
	RejectedObjectGeneration  int
	RejectedDepthError        int
	RejectedNormalAngle       int
	AcceptedContributions     int
	DroppedByCollisionOrder   int
	PixelsWithoutHistory      int
	PixelsResolvedToZeroAlpha int
}

// Result is a fully resolved frame. Frame.Color contains the final temporal
// color; Frame.Depth/Normals/ObjectIDs/Generations are owned copies of the
// current frame's geometry, ready to become the next history frame.
//
// HistoryColor is the normalized and spatially clamped history color, exposed
// for diagnostics and for pipelines that perform their own temporal blend.
// HistoryWeight is the sum of accepted bilinear footprint weights.
type Result struct {
	Frame         Frame
	HistoryColor  []RGBA
	HistoryWeight []float64
	Stats         ReprojectionStats
}

var (
	// ErrInvalidFrame reports malformed frame storage.
	ErrInvalidFrame = errors.New("taskpkg: invalid frame")
	// ErrInvalidCamera reports a singular or malformed camera.
	ErrInvalidCamera = errors.New("taskpkg: invalid camera")
	// ErrInvalidConfig reports invalid thresholds or execution controls.
	ErrInvalidConfig = errors.New("taskpkg: invalid config")
	// ErrDimensionMismatch reports previous/current frames of different sizes.
	ErrDimensionMismatch = errors.New("taskpkg: frame dimension mismatch")
	// ErrStaleHistory reports a commit based on a history version that has
	// already been superseded.
	ErrStaleHistory = errors.New("taskpkg: stale history result")
)
