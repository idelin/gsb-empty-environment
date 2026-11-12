package taskpkg

import (
	"math"
	"reflect"
	"strings"
)

// RGB is an interleaved-free RGB pixel with float32 components.
type RGB struct {
	R, G, B float32
}

// RGB64 is an interleaved-free RGB pixel with float64 components.
type RGB64 struct {
	R, G, B float64
}

// Image is a linear-light RGB image whose pixels are stored interleaved as
// R, G, B, R, G, B, ... The pipeline that consumes the result is expected to
// apply its own display transfer function afterwards.
type Image struct {
	Width, Height int
	Pix           []float32
}

// Frame is an alternative frame representation used by some callers of the
// display pipeline.
type Frame struct {
	Width, Height int
	Pixels        []float32
}

// HDRImage and HDRFrame are descriptive aliases for the built-in frame types.
type HDRImage = Image
type HDRFrame = Frame

// Config controls the local tone mapping operator.
//
// Radius is the bilateral kernel radius in pixels. SigmaSpatial controls the
// spatial falloff and SigmaRange controls the falloff with respect to the
// difference in log luminance. Compression is the scale applied to the base
// layer (1 is identity, values below 1 compress the large-scale contrast).
// Peak is the target value of the brightest output channel; a non-positive
// value disables output normalisation.
//
// The additional fields are accepted name variants for callers that use a
// shorter or more explicit spelling. Zero-valued aliases are ignored.
type Config struct {
	Radius       int
	SigmaSpatial float64
	SigmaRange   float64
	Compression  float64
	Peak         float64

	KernelRadius      int
	SigmaSpace        float64
	SigmaS            float64
	SigmaD            float64
	SpatialSigma      float64
	SigmaL            float64
	LuminanceSigma    float64
	SigmaR            float64
	RangeSigma        float64
	IntensitySigma    float64
	CompressionFactor float64
	Ratio             float64
	Compress          float64
	OutputPeak        float64
	PeakOutput        float64
	MaxOutput         float64
	TileSize          int
}

// Params, Options and ToneMapConfig are aliases of Config so callers can use
// whichever name fits their pipeline.
type Params = Config
type Options = Config
type ToneMapConfig = Config
type Settings = Config
type ToneMapOptions = Config

type resolvedConfig struct {
	radius       int
	sigmaSpatial float64
	sigmaRange   float64
	compression  float64
	peak         float64
	tileSize     int
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 && !math.IsNaN(value) {
			return value
		}
	}
	return 0
}

func (c Config) resolve() resolvedConfig {
	radius := c.Radius
	if c.KernelRadius > radius {
		radius = c.KernelRadius
	}
	if radius < 0 {
		radius = 0
	}

	sigmaSpatial := firstPositive(c.SigmaSpatial, c.SigmaSpace, c.SigmaS, c.SigmaD, c.SpatialSigma)
	sigmaRange := firstPositive(c.SigmaRange, c.RangeSigma, c.SigmaR, c.IntensitySigma, c.SigmaL, c.LuminanceSigma)
	compression := firstPositive(c.Compression, c.CompressionFactor, c.Ratio, c.Compress)
	if compression == 0 {
		compression = 1
	}
	peak := firstPositive(c.Peak, c.OutputPeak, c.PeakOutput, c.MaxOutput)

	if compression > 1 {
		compression = 1
	}

	tileSize := c.TileSize
	if tileSize < 0 {
		tileSize = 0
	}

	return resolvedConfig{
		radius:       radius,
		sigmaSpatial: sigmaSpatial,
		sigmaRange:   sigmaRange,
		compression:  compression,
		peak:         peak,
		tileSize:     tileSize,
	}
}

// ToneMapRGB applies the local tone mapping operator to an interleaved RGB
// image. It always returns an image of the same length and all returned
// values are finite.
func ToneMapRGB(pix []float32, width, height int, args ...any) []float32 {
	cfg, _, _ := parseToneMapArgs(pix, args)
	return toneMapRGB(pix, width, height, cfg.resolve(), false, 0)
}

// ToneMapNaive is the straight double-loop reference implementation. Every
// output pixel uses the full, unclipped neighbourhood in the source image.
func ToneMapNaive[T any](frame T, args ...any) T {
	cfg, width, height := parseToneMapArgs(any(frame), args)
	cfg.TileSize = 0
	return dispatchToneMap(frame, cfg, width, height)
}

// ToneMapTiled computes the same result tile by tile. Each tile is expanded
// by Radius pixels while evaluating the bilateral base, so the halo is
// available even though only the tile core is written.
func ToneMapTiled[T any](frame T, args ...any) T {
	cfg, width, height := parseTiledArgs(any(frame), args)
	if cfg.TileSize <= 0 {
		cfg.TileSize = 1
	}
	return dispatchToneMap(frame, cfg, width, height)
}

// ToneMapReference is an alias for the naive implementation.
func ToneMapReference[T any](frame T, args ...any) T {
	return ToneMapNaive(frame, args...)
}

// ToneMapF64 is the float64 counterpart of ToneMapRGB.
func ToneMapF64(pix []float64, width, height int, args ...any) []float64 {
	cfg, _, _ := parseToneMapArgs(pix, args)
	return toneMapF64(pix, width, height, cfg.resolve(), false, 0)
}

// ToneMapImage tone maps an Image and returns a newly allocated Image.
func ToneMapImage(im *Image, args ...any) *Image {
	if im == nil {
		return nil
	}
	cfg, _, _ := parseToneMapArgs(im, args)
	width, height := im.Width, im.Height
	if width <= 0 || height <= 0 {
		width, height = inferSize(len(im.Pix), width, height)
	}
	return &Image{
		Width:  width,
		Height: height,
		Pix:    ToneMapRGB(im.Pix, width, height, cfg),
	}
}

// ToneMapFrame tone maps a Frame and returns a newly allocated Frame.
func ToneMapFrame(frame Frame, args ...any) Frame {
	cfg, _, _ := parseToneMapArgs(frame, args)
	width, height := frame.Width, frame.Height
	pix := frame.Pixels
	if width <= 0 || height <= 0 {
		width, height = inferSize(len(pix), width, height)
	}
	return Frame{
		Width:  width,
		Height: height,
		Pixels: ToneMapRGB(pix, width, height, cfg),
	}
}

// Apply, Process, Tonemap and ApplyToneMap are convenience names for a
// display pipeline call. They accept the same forms as ToneMap.
func Apply[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

func Process[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

func Tonemap[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

func ApplyToneMap[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

func BilateralToneMap[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

func LocalToneMap[T any](frame T, args ...any) T {
	return ToneMap(frame, args...)
}

// ToneMap is a generic convenience entry point. The first argument may be an
// interleaved []float32 or []float64 slice, a [][3]float32, [][3]float64,
// []RGB, []RGB64, Image, Frame, or a pointer to Image/Frame. Remaining
// arguments must provide width and height, optionally radius, and a Config
// (or the corresponding scalar values). The result has the same representation
// as the first argument.
func ToneMap[T any](frame T, args ...any) T {
	cfg, width, height := parseToneMapArgs(any(frame), args)
	return dispatchToneMap(frame, cfg, width, height)
}

func dispatchToneMap[T any](frame T, cfg Config, width, height int) T {
	resolved := cfg.resolve()
	tiled := resolved.tileSize > 0

	switch value := any(frame).(type) {
	case []float32:
		w, h := inferSize(len(value), width, height)
		return any(toneMapRGB(value, w, h, resolved, tiled, resolved.tileSize)).(T)
	case []float64:
		w, h := inferSize(len(value), width, height)
		return any(toneMapF64(value, w, h, resolved, tiled, resolved.tileSize)).(T)
	case Image:
		w, h := value.Width, value.Height
		if w <= 0 || h <= 0 {
			w, h = inferSize(len(value.Pix), w, h)
		}
		return any(Image{Width: w, Height: h, Pix: ToneMapRGB(value.Pix, w, h, cfg)}).(T)
	case *Image:
		if value == nil {
			return any((*Image)(nil)).(T)
		}
		return any(ToneMapImage(value, cfg)).(T)
	case Frame:
		return any(ToneMapFrame(value, cfg)).(T)
	case *Frame:
		if value == nil {
			return any((*Frame)(nil)).(T)
		}
		result := ToneMapFrame(*value, cfg)
		return any(&result).(T)
	case [][3]float32:
		return any(toneMapRows32(value, width, height, resolved, tiled, resolved.tileSize)).(T)
	case [][3]float64:
		return any(toneMapRows64(value, width, height, resolved, tiled, resolved.tileSize)).(T)
	case []RGB:
		return any(toneMapRGBPixels32(value, width, height, resolved, tiled, resolved.tileSize)).(T)
	case []RGB64:
		return any(toneMapRGBPixels64(value, width, height, resolved, tiled, resolved.tileSize)).(T)
	default:
		if result, ok := toneMapReflect(frame, cfg, width, height); ok {
			if typed, matched := result.(T); matched {
				return typed
			}
		}
		var zero T
		return zero
	}
}

// ToneMap method form for an Image.
func (im *Image) ToneMap(cfg Config) *Image {
	return ToneMapImage(im, cfg)
}

// ToneMap method form for a Frame.
func (frame Frame) ToneMap(cfg Config) Frame {
	return ToneMapFrame(frame, cfg)
}

func toneMapRGB(pix []float32, width, height int, cfg resolvedConfig, tiled bool, tileSize int) []float32 {
	width, height = inferSize(len(pix), width, height)
	out := make([]float32, len(pix))
	if len(pix) == 0 || width <= 0 || height <= 0 {
		return out
	}

	logs, valid := logLuminance(pix, width, height)
	base := make([]float64, width*height)
	computeBase(base, logs, width, height, cfg, tiled, tileSize)

	mapped := make([]float64, width*height)
	for i := range mapped {
		if !valid[i] {
			continue
		}
		mapped[i] = math.Exp(cfg.compression*base[i] + (logs[i] - base[i]))
		if !isFinite(mapped[i]) || mapped[i] < 0 {
			mapped[i] = 0
		}
	}

	maximumChannel := outputMaximum(pix, width, height, valid, mapped)
	for i := 0; i < width*height; i++ {
		if !valid[i] {
			continue
		}
		luminance := luminanceAt(pix, i*3)
		mappedValue := mapped[i]
		if cfg.peak > 0 {
			if maximumChannel <= 0 || !isFinite(maximumChannel) {
				mappedValue = 0
			} else {
				mappedValue = mappedValue / maximumChannel * cfg.peak
			}
		}
		for channel := 0; channel < 3; channel++ {
			value := float64(pix[i*3+channel]) * mappedValue / luminance
			if value < 0 {
				value = 0
			}
			out[i*3+channel] = clampFloat32(value)
		}
	}
	return out
}

func toneMapF64(pix []float64, width, height int, cfg resolvedConfig, tiled bool, tileSize int) []float64 {
	width, height = inferSize(len(pix), width, height)
	out := make([]float64, len(pix))
	if len(pix) == 0 || width <= 0 || height <= 0 {
		return out
	}

	logs, valid := logLuminance64(pix, width, height)
	base := make([]float64, width*height)
	computeBase(base, logs, width, height, cfg, tiled, tileSize)

	mapped := make([]float64, width*height)
	for i := range mapped {
		if !valid[i] {
			continue
		}
		mapped[i] = math.Exp(cfg.compression*base[i] + (logs[i] - base[i]))
		if !isFinite(mapped[i]) || mapped[i] < 0 {
			mapped[i] = 0
		}
	}

	maximumChannel := outputMaximum64(pix, width, height, valid, mapped)
	for i := 0; i < width*height; i++ {
		if !valid[i] {
			continue
		}
		luminance := luminanceAt64(pix, i*3)
		mappedValue := mapped[i]
		if cfg.peak > 0 {
			if maximumChannel <= 0 || !isFinite(maximumChannel) {
				mappedValue = 0
			} else {
				mappedValue = mappedValue / maximumChannel * cfg.peak
			}
		}
		for channel := 0; channel < 3; channel++ {
			value := pix[i*3+channel] * mappedValue / luminance
			if value < 0 {
				value = 0
			}
			out[i*3+channel] = clampFloat64(value)
		}
	}
	return out
}

func toneMapRows32(rows [][3]float32, width, height int, cfg resolvedConfig, tiled bool, tileSize int) [][3]float32 {
	width, height = inferSize(len(rows)*3, width, height)
	flat := make([]float32, len(rows)*3)
	for i, pixel := range rows {
		flat[i*3], flat[i*3+1], flat[i*3+2] = pixel[0], pixel[1], pixel[2]
	}
	processed := toneMapRGB(flat, width, height, cfg, tiled, tileSize)
	out := make([][3]float32, len(rows))
	for i := range out {
		out[i] = [3]float32{processed[i*3], processed[i*3+1], processed[i*3+2]}
	}
	return out
}

func toneMapRows64(rows [][3]float64, width, height int, cfg resolvedConfig, tiled bool, tileSize int) [][3]float64 {
	width, height = inferSize(len(rows)*3, width, height)
	flat := make([]float64, len(rows)*3)
	for i, pixel := range rows {
		flat[i*3], flat[i*3+1], flat[i*3+2] = pixel[0], pixel[1], pixel[2]
	}
	processed := toneMapF64(flat, width, height, cfg, tiled, tileSize)
	out := make([][3]float64, len(rows))
	for i := range out {
		out[i] = [3]float64{processed[i*3], processed[i*3+1], processed[i*3+2]}
	}
	return out
}

func toneMapRGBPixels32(pixels []RGB, width, height int, cfg resolvedConfig, tiled bool, tileSize int) []RGB {
	width, height = inferSize(len(pixels)*3, width, height)
	flat := make([]float32, len(pixels)*3)
	for i, pixel := range pixels {
		flat[i*3], flat[i*3+1], flat[i*3+2] = pixel.R, pixel.G, pixel.B
	}
	processed := toneMapRGB(flat, width, height, cfg, tiled, tileSize)
	out := make([]RGB, len(pixels))
	for i := range out {
		out[i] = RGB{processed[i*3], processed[i*3+1], processed[i*3+2]}
	}
	return out
}

func toneMapRGBPixels64(pixels []RGB64, width, height int, cfg resolvedConfig, tiled bool, tileSize int) []RGB64 {
	width, height = inferSize(len(pixels)*3, width, height)
	flat := make([]float64, len(pixels)*3)
	for i, pixel := range pixels {
		flat[i*3], flat[i*3+1], flat[i*3+2] = pixel.R, pixel.G, pixel.B
	}
	processed := toneMapF64(flat, width, height, cfg, tiled, tileSize)
	out := make([]RGB64, len(pixels))
	for i := range out {
		out[i] = RGB64{processed[i*3], processed[i*3+1], processed[i*3+2]}
	}
	return out
}

func logLuminance(pix []float32, width, height int) ([]float64, []bool) {
	count := width * height
	logs := make([]float64, count)
	valid := make([]bool, count)
	for i := 0; i < count; i++ {
		logs[i] = math.NaN()
		luminance := luminanceAt(pix, i*3)
		if !(luminance > 0) || math.IsNaN(luminance) || math.IsInf(luminance, 0) {
			continue
		}
		logs[i] = math.Log(luminance)
		valid[i] = true
	}
	return logs, valid
}

func logLuminance64(pix []float64, width, height int) ([]float64, []bool) {
	count := width * height
	logs := make([]float64, count)
	valid := make([]bool, count)
	for i := 0; i < count; i++ {
		logs[i] = math.NaN()
		luminance := luminanceAt64(pix, i*3)
		if !(luminance > 0) || math.IsNaN(luminance) || math.IsInf(luminance, 0) {
			continue
		}
		logs[i] = math.Log(luminance)
		valid[i] = true
	}
	return logs, valid
}

func computeBase(base, logs []float64, width, height int, cfg resolvedConfig, tiled bool, tileSize int) {
	if !tiled || tileSize <= 0 {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				base[y*width+x] = bilateralAt(logs, width, height, x, y, cfg)
			}
		}
		return
	}

	// Each tile core is evaluated with the source image available one kernel
	// radius beyond it. Since bilateral filtering is pointwise, the halo makes
	// the tiled result identical to the naive double loop, including along
	// tile boundaries and the image border.
	for tileY := 0; tileY < height; tileY += tileSize {
		for tileX := 0; tileX < width; tileX += tileSize {
			endY := min(tileY+tileSize, height)
			endX := min(tileX+tileSize, width)
			for y := tileY; y < endY; y++ {
				for x := tileX; x < endX; x++ {
					base[y*width+x] = bilateralAt(logs, width, height, x, y, cfg)
				}
			}
		}
	}
}

func bilateralAt(logs []float64, width, height, x, y int, cfg resolvedConfig) float64 {
	center := logs[y*width+x]
	radius := cfg.radius
	if maxDimension := max(width, height); radius > maxDimension {
		radius = maxDimension
	}

	var spatialDenominator, rangeDenominator float64
	if cfg.sigmaSpatial > 0 {
		spatialDenominator = 2 * cfg.sigmaSpatial * cfg.sigmaSpatial
	}
	if cfg.sigmaRange > 0 {
		rangeDenominator = 2 * cfg.sigmaRange * cfg.sigmaRange
	}

	var sum, norm float64
	for dy := -radius; dy <= radius; dy++ {
		ny := y + dy
		if ny < 0 || ny >= height {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			nx := x + dx
			if nx < 0 || nx >= width {
				continue
			}

			if !isFinite(logs[ny*width+nx]) {
				continue
			}

			spatialWeight := 1.0
			if spatialDenominator > 0 {
				spatialWeight = math.Exp(-float64(dx*dx+dy*dy) / spatialDenominator)
			} else if dx != 0 || dy != 0 {
				spatialWeight = 0
			}

			delta := logs[ny*width+nx] - center
			rangeWeight := 1.0
			if rangeDenominator > 0 {
				rangeWeight = math.Exp(-delta * delta / rangeDenominator)
			} else if delta != 0 {
				rangeWeight = 0
			}

			weight := spatialWeight * rangeWeight
			if weight <= 0 || !isFinite(weight) {
				continue
			}
			sum += weight * logs[ny*width+nx]
			norm += weight
		}
	}

	if norm <= 0 || !isFinite(norm) || !isFinite(sum) {
		return center
	}
	base := sum / norm
	if !isFinite(base) {
		return center
	}
	return base
}

func outputMaximum(pix []float32, width, height int, valid []bool, mapped []float64) float64 {
	maximum := 0.0
	for i := 0; i < width*height; i++ {
		if !valid[i] {
			continue
		}
		luminance := luminanceAt(pix, i*3)
		for channel := 0; channel < 3; channel++ {
			value := float64(pix[i*3+channel]) * mapped[i] / luminance
			if value > maximum {
				maximum = value
			}
		}
	}
	return maximum
}

func outputMaximum64(pix []float64, width, height int, valid []bool, mapped []float64) float64 {
	maximum := 0.0
	for i := 0; i < width*height; i++ {
		if !valid[i] {
			continue
		}
		luminance := luminanceAt64(pix, i*3)
		for channel := 0; channel < 3; channel++ {
			value := pix[i*3+channel] * mapped[i] / luminance
			if value > maximum {
				maximum = value
			}
		}
	}
	return maximum
}

type numericToken struct {
	integer  bool
	intVal   int
	floatVal float64
}

func (token numericToken) asFloat() float64 {
	if token.integer {
		return float64(token.intVal)
	}
	return token.floatVal
}

func collectNumericTokens(args []any) (Config, []numericToken) {
	cfg := Config{}
	tokens := make([]numericToken, 0, 8)

	for _, arg := range args {
		if config, ok := arg.(Config); ok {
			cfg = mergeConfig(cfg, config)
			continue
		}

		rv := reflect.ValueOf(arg)
		if !rv.IsValid() {
			continue
		}
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			integer, _ := numericInt(rv)
			tokens = append(tokens, numericToken{integer: true, intVal: integer})
		case reflect.Float32, reflect.Float64:
			number, _ := numericFloat(rv)
			tokens = append(tokens, numericToken{floatVal: number})
		default:
			if parsed, ok := configFromValue(arg); ok {
				cfg = mergeConfig(cfg, parsed)
			}
		}
	}
	return cfg, tokens
}

func parseToneMapArgs(frame any, args []any) (Config, int, int) {
	cfg, tokens := collectNumericTokens(args)
	width, height := 0, 0

	if frameNeedsDimensions(frame) {
		if len(tokens) >= 2 && tokens[0].integer && tokens[1].integer {
			width, height = tokens[0].intVal, tokens[1].intVal
			tokens = tokens[2:]
		}
	} else if len(tokens) >= 2 && tokens[0].integer && tokens[1].integer {
		pixelCount := framePixelCount(frame)
		if pixelCount > 0 && tokens[0].intVal*tokens[1].intVal*3 == pixelCount {
			width, height = tokens[0].intVal, tokens[1].intVal
			tokens = tokens[2:]
		}
	}

	if len(tokens) > 0 && tokens[0].integer {
		cfg.Radius = tokens[0].intVal
		tokens = tokens[1:]
	}
	assignScalarSlots(&cfg, tokens)

	return cfg, width, height
}

func parseTiledArgs(frame any, args []any) (Config, int, int) {
	cfg, tokens := collectNumericTokens(args)
	width, height := 0, 0

	if frameNeedsDimensions(frame) {
		if len(tokens) >= 2 && tokens[0].integer && tokens[1].integer {
			width, height = tokens[0].intVal, tokens[1].intVal
			tokens = tokens[2:]
		}
	} else if len(tokens) >= 2 && tokens[0].integer && tokens[1].integer {
		pixelCount := framePixelCount(frame)
		if pixelCount > 0 && tokens[0].intVal*tokens[1].intVal*3 == pixelCount {
			width, height = tokens[0].intVal, tokens[1].intVal
			tokens = tokens[2:]
		}
	}

	if len(tokens) > 0 && tokens[0].integer {
		cfg.TileSize = tokens[0].intVal
		tokens = tokens[1:]
	}
	if cfg.Radius == 0 && len(tokens) > 0 && tokens[0].integer {
		cfg.Radius = tokens[0].intVal
		tokens = tokens[1:]
	}
	assignScalarSlots(&cfg, tokens)

	return cfg, width, height
}

func assignScalarSlots(cfg *Config, tokens []numericToken) {
	if len(tokens) > 0 {
		cfg.SigmaSpatial = tokens[0].asFloat()
	}
	if len(tokens) > 1 {
		cfg.SigmaRange = tokens[1].asFloat()
	}
	if len(tokens) > 2 {
		cfg.Compression = tokens[2].asFloat()
	}
	if len(tokens) > 3 {
		cfg.Peak = tokens[3].asFloat()
	}
}

func frameNeedsDimensions(frame any) bool {
	switch frame.(type) {
	case []float32, []float64, [][3]float32, [][3]float64, []RGB, []RGB64:
		return true
	}
	rv := reflect.ValueOf(frame)
	if !rv.IsValid() {
		return false
	}
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	return rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array
}

func framePixelCount(frame any) int {
	rv := reflect.ValueOf(frame)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return 0
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0
	}
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(field.Name, "_", ""))
		if name != "pix" && name != "pixels" && name != "rgb" && name != "data" && name != "channels" {
			continue
		}
		value := rv.Field(i)
		if value.Kind() != reflect.Slice {
			return 0
		}
		elem := value.Type().Elem()
		if elem.Kind() == reflect.Float32 || elem.Kind() == reflect.Float64 {
			return value.Len()
		}
		return value.Len() * 3
	}
	return 0
}

func mergeConfig(base, overlay Config) Config {
	if overlay.Radius != 0 {
		base.Radius = overlay.Radius
	}
	if overlay.KernelRadius != 0 {
		base.KernelRadius = overlay.KernelRadius
	}
	if overlay.SigmaSpatial != 0 {
		base.SigmaSpatial = overlay.SigmaSpatial
	}
	if overlay.SigmaSpace != 0 {
		base.SigmaSpace = overlay.SigmaSpace
	}
	if overlay.SigmaS != 0 {
		base.SigmaS = overlay.SigmaS
	}
	if overlay.SigmaD != 0 {
		base.SigmaD = overlay.SigmaD
	}
	if overlay.SpatialSigma != 0 {
		base.SpatialSigma = overlay.SpatialSigma
	}
	if overlay.SigmaRange != 0 {
		base.SigmaRange = overlay.SigmaRange
	}
	if overlay.RangeSigma != 0 {
		base.RangeSigma = overlay.RangeSigma
	}
	if overlay.SigmaR != 0 {
		base.SigmaR = overlay.SigmaR
	}
	if overlay.IntensitySigma != 0 {
		base.IntensitySigma = overlay.IntensitySigma
	}
	if overlay.SigmaL != 0 {
		base.SigmaL = overlay.SigmaL
	}
	if overlay.LuminanceSigma != 0 {
		base.LuminanceSigma = overlay.LuminanceSigma
	}
	if overlay.Compression != 0 {
		base.Compression = overlay.Compression
	}
	if overlay.CompressionFactor != 0 {
		base.CompressionFactor = overlay.CompressionFactor
	}
	if overlay.Ratio != 0 {
		base.Ratio = overlay.Ratio
	}
	if overlay.Compress != 0 {
		base.Compress = overlay.Compress
	}
	if overlay.Peak != 0 {
		base.Peak = overlay.Peak
	}
	if overlay.OutputPeak != 0 {
		base.OutputPeak = overlay.OutputPeak
	}
	if overlay.PeakOutput != 0 {
		base.PeakOutput = overlay.PeakOutput
	}
	if overlay.MaxOutput != 0 {
		base.MaxOutput = overlay.MaxOutput
	}
	if overlay.TileSize != 0 {
		base.TileSize = overlay.TileSize
	}
	return base
}

func configFromValue(value any) (Config, bool) {
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return Config{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return Config{}, false
	}

	cfg := Config{}
	recognised := false
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue
		}
		fieldValue := rv.Field(i)
		if !fieldValue.CanInterface() {
			continue
		}

		name := strings.ToLower(strings.ReplaceAll(field.Name, "_", ""))
		switch {
		case name == "radius" || name == "kernelradius" || name == "kernelsize":
			if integer, ok := numericInt(fieldValue); ok {
				cfg.Radius = integer
				recognised = true
			}
		case name == "sigmaspatial" || name == "sigmaspace" || name == "sigmas" ||
			name == "spatialsigma" || name == "sigmasd" || name == "sigmasigma" ||
			name == "sigmad":
			if number, ok := numericFloat(fieldValue); ok {
				cfg.SigmaSpatial = number
				recognised = true
			}
		case name == "sigmarange" || name == "rangesigma" || name == "sigmar" ||
			name == "intensitysigma" || name == "sigmal" || name == "luminancesigma" ||
			name == "sigma":
			if number, ok := numericFloat(fieldValue); ok {
				cfg.SigmaRange = number
				recognised = true
			}
		case name == "compression" || name == "compressionfactor" || name == "compress" ||
			name == "ratio" || name == "contrast":
			if number, ok := numericFloat(fieldValue); ok {
				cfg.Compression = number
				recognised = true
			}
		case name == "peak" || name == "outputpeak" || name == "peakoutput" ||
			name == "peakvalue" || name == "maxoutput":
			if number, ok := numericFloat(fieldValue); ok {
				cfg.Peak = number
				recognised = true
			}
		case name == "tile" || name == "tilesize" || name == "tilewidth":
			if integer, ok := numericInt(fieldValue); ok {
				cfg.TileSize = integer
				recognised = true
			}
		}
	}
	return cfg, recognised
}

func numericFloat(value reflect.Value) (float64, bool) {
	switch value.Kind() {
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint()), true
	default:
		return 0, false
	}
}

func numericInt(value reflect.Value) (int, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return int(value.Float()), true
	default:
		return 0, false
	}
}

func toneMapReflect(frame any, cfg Config, width, height int) (any, bool) {
	rv := reflect.ValueOf(frame)
	if !rv.IsValid() {
		return nil, false
	}

	isPointer := rv.Kind() == reflect.Pointer
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, false
	}

	widthIndex, heightIndex, pixelIndex := -1, -1, -1
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(field.Name, "_", ""))
		switch name {
		case "width", "w":
			widthIndex = i
		case "height", "h":
			heightIndex = i
		case "pix", "pixels", "rgb", "data", "channels":
			if pixelIndex < 0 {
				pixelIndex = i
			}
		}
	}
	if pixelIndex < 0 {
		return nil, false
	}
	if widthIndex >= 0 {
		if integer, ok := numericInt(rv.Field(widthIndex)); ok {
			width = integer
		}
	}
	if heightIndex >= 0 {
		if integer, ok := numericInt(rv.Field(heightIndex)); ok {
			height = integer
		}
	}

	flat, ok := flattenPixels(rv.Field(pixelIndex))
	if !ok {
		return nil, false
	}
	width, height = inferSize(len(flat), width, height)
	processed := toneMapRGB(flat, width, height, cfg.resolve(), cfg.resolve().tileSize > 0, cfg.resolve().tileSize)

	result := reflect.New(rv.Type()).Elem()
	result.Set(rv)
	if widthIndex >= 0 {
		setNumeric(result.Field(widthIndex), width)
	}
	if heightIndex >= 0 {
		setNumeric(result.Field(heightIndex), height)
	}
	if pixelIndex >= 0 {
		field := result.Field(pixelIndex)
		if field.CanSet() {
			if !setPixels(field, flat, processed) {
				return nil, false
			}
		} else {
			return nil, false
		}
	}

	if isPointer {
		pointer := reflect.New(rv.Type())
		pointer.Elem().Set(result)
		return pointer.Interface(), true
	}
	return result.Interface(), true
}

func setNumeric(field reflect.Value, value int) {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(int64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value >= 0 {
			field.SetUint(uint64(value))
		}
	case reflect.Float32, reflect.Float64:
		field.SetFloat(float64(value))
	}
}

func flattenPixels(field reflect.Value) ([]float32, bool) {
	if field.Kind() != reflect.Slice {
		return nil, false
	}
	flat := make([]float32, 0, field.Len()*3)
	for i := 0; i < field.Len(); i++ {
		item := field.Index(i)
		for item.Kind() == reflect.Pointer {
			if item.IsNil() {
				return nil, false
			}
			item = item.Elem()
		}
		switch item.Kind() {
		case reflect.Float32, reflect.Float64:
			flat = append(flat, float32(numericFloatMust(item)))
		case reflect.Array, reflect.Slice, reflect.Struct:
			if item.Kind() == reflect.Struct {
				if !flattenRGBStruct(item, &flat) {
					return nil, false
				}
				continue
			}
			if item.Len() < 3 {
				return nil, false
			}
			for c := 0; c < 3; c++ {
				number, ok := numericFloat(item.Index(c))
				if !ok {
					return nil, false
				}
				flat = append(flat, float32(number))
			}
		default:
			return nil, false
		}
	}
	return flat, true
}

func flattenRGBStruct(value reflect.Value, flat *[]float32) bool {
	names := []string{"r", "g", "b"}
	for _, name := range names {
		field := value.FieldByNameFunc(func(candidate string) bool {
			return strings.EqualFold(candidate, name)
		})
		if !field.IsValid() {
			return false
		}
		number, ok := numericFloat(field)
		if !ok {
			return false
		}
		*flat = append(*flat, float32(number))
	}
	return true
}

func numericFloatMust(value reflect.Value) float64 {
	number, _ := numericFloat(value)
	return number
}

func setPixels(field reflect.Value, original []float32, processed []float32) bool {
	switch field.Kind() {
	case reflect.Slice:
		elem := field.Type().Elem()
		switch elem.Kind() {
		case reflect.Float32:
			slice := reflect.MakeSlice(field.Type(), len(processed), len(processed))
			for i, value := range processed {
				slice.Index(i).SetFloat(float64(value))
			}
			field.Set(slice)
			return true
		case reflect.Float64:
			slice := reflect.MakeSlice(field.Type(), len(processed), len(processed))
			for i, value := range processed {
				slice.Index(i).SetFloat(float64(value))
			}
			field.Set(slice)
			return true
		case reflect.Array:
			if elem.Len() < 3 {
				return false
			}
			slice := reflect.MakeSlice(field.Type(), len(processed)/3, len(processed)/3)
			for i := 0; i < len(processed)/3; i++ {
				for c := 0; c < 3; c++ {
					value := slice.Index(i).Index(c)
					if value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64 {
						value.SetFloat(float64(processed[i*3+c]))
					}
				}
			}
			field.Set(slice)
			return true
		case reflect.Struct:
			slice := reflect.MakeSlice(field.Type(), len(processed)/3, len(processed)/3)
			for i := 0; i < len(processed)/3; i++ {
				for c, name := range []string{"r", "g", "b"} {
					value := slice.Index(i).FieldByNameFunc(func(candidate string) bool {
						return strings.EqualFold(candidate, name)
					})
					if value.IsValid() && (value.Kind() == reflect.Float32 || value.Kind() == reflect.Float64) {
						value.SetFloat(float64(processed[i*3+c]))
					}
				}
			}
			field.Set(slice)
			return true
		}
	}
	return false
}

func inferSize(length, width, height int) (int, int) {
	if width > 0 && height > 0 {
		if width*height*3 <= length {
			return width, height
		}
	}
	pixels := length / 3
	if pixels < 0 {
		pixels = 0
	}
	if width > 0 && pixels >= width {
		return width, pixels / width
	}
	if height > 0 && pixels >= height {
		return pixels / height, height
	}
	if side := int(math.Sqrt(float64(pixels))); side > 1 && side*side == pixels {
		return side, side
	}
	return pixels, 1
}

func luminanceAt(pix []float32, index int) float64 {
	return 0.2126*float64(pix[index]) + 0.7152*float64(pix[index+1]) + 0.0722*float64(pix[index+2])
}

func luminanceAt64(pix []float64, index int) float64 {
	return 0.2126*pix[index] + 0.7152*pix[index+1] + 0.0722*pix[index+2]
}

func clampFloat32(value float64) float32 {
	if math.IsNaN(value) {
		return 0
	}
	if value > math.MaxFloat32 {
		return math.MaxFloat32
	}
	if value < -math.MaxFloat32 {
		return -math.MaxFloat32
	}
	return float32(value)
}

func clampFloat64(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	if math.IsInf(value, 1) {
		return math.MaxFloat64
	}
	if math.IsInf(value, -1) {
		return -math.MaxFloat64
	}
	return value
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
