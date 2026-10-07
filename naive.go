package morph

import "errors"

// ErrRaggedImage is returned when image rows have differing lengths.
var ErrRaggedImage = errors.New("morph: image rows have inconsistent widths")

func checkImage(img [][]uint8) (width, height int, err error) {
	height = len(img)
	if height == 0 {
		return 0, 0, nil
	}
	width = len(img[0])
	for _, row := range img {
		if len(row) != width {
			return 0, 0, ErrRaggedImage
		}
	}
	return width, height, nil
}

// Dilate computes whole-image grayscale dilation: out[y][x] is the max of
// img[y+dy][x+dx] over the element offsets. Out-of-image pixels are zero
// and therefore never raise the max.
func Dilate(img [][]uint8, el Element) ([][]uint8, error) {
	return applyNaive(img, el, func(best, v uint8) uint8 {
		if v > best {
			return v
		}
		return best
	}, 0)
}

// Erode computes whole-image grayscale erosion: out[y][x] is the min of
// img[y+dy][x+dx] over the element offsets. Out-of-image pixels are zero
// and are NOT skipped: any offset falling outside the image forces the
// output pixel to zero.
func Erode(img [][]uint8, el Element) ([][]uint8, error) {
	return applyNaive(img, el, func(best, v uint8) uint8 {
		if v < best {
			return v
		}
		return best
	}, 255)
}

func applyNaive(img [][]uint8, el Element, fold func(best, v uint8) uint8, init uint8) ([][]uint8, error) {
	width, height, err := checkImage(img)
	if err != nil {
		return nil, err
	}
	out := make([][]uint8, height)
	for y := range out {
		out[y] = make([]uint8, width)
		for x := 0; x < width; x++ {
			best := init
			for _, p := range el.offsets {
				var v uint8 // out-of-image pixels are fixed to zero
				if ry, rx := y+p.Y, x+p.X; ry >= 0 && ry < height && rx >= 0 && rx < width {
					v = img[ry][rx]
				}
				best = fold(best, v)
			}
			out[y][x] = best
		}
	}
	return out, nil
}
