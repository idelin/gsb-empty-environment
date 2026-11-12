package pdfscan

import (
	"bytes"
	"errors"
	"fmt"

	"compress/zlib"
)

// decodeStream reverses the filters applied to a stream. The scanner needs
// FlateDecode (optionally with PNG predictors) for object streams and
// cross-reference streams. An unsupported filter returns an error so the
// gateway fails closed rather than silently missing compressed actions.
func decodeStream(st *Stream) ([]byte, error) {
	data := st.Raw

	var filters []string
	if fv := st.Dict.Get("Filter"); fv != nil {
		switch fv.Kind {
		case KindName:
			filters = append(filters, string(fv.Str))
		case KindArray:
			for _, item := range fv.Array {
				if item.Kind != KindName {
					return nil, errors.New("pdfscan: unsupported non-name stream filter")
				}
				filters = append(filters, string(item.Str))
			}
		default:
			return nil, errors.New("pdfscan: malformed stream Filter entry")
		}
	}

	parms := filterParams(st.Dict)
	for i, filter := range filters {
		switch filter {
		case "FlateDecode", "Fl":
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("pdfscan: flate decode failed: %w", err)
			}
			decoded, err := readAllLimited(zr, maxDecodedBytes)
			zr.Close()
			if err != nil {
				return nil, err
			}
			if i < len(parms) && parms[i] != nil {
				decoded, err = applyPredictor(parms[i], decoded)
				if err != nil {
					return nil, err
				}
			}
			data = decoded
		case "Crypt":
			// Only the identity (no-op) crypt filter is acceptable.
			if i < len(parms) && parms[i] != nil {
				if name, ok := parms[i].Get("Name").AsName(); ok && name != "Identity" && name != "None" {
					return nil, errors.New("pdfscan: encrypted/non-identity Crypt filter is not supported")
				}
			}
		default:
			return nil, fmt.Errorf("pdfscan: unsupported stream filter %q", filter)
		}
	}
	return data, nil
}

func filterParams(d *Dict) []*Dict {
	pv := d.Get("DecodeParms")
	if pv == nil || pv.Kind == KindNull {
		return nil
	}
	if pv.Kind == KindDict {
		return []*Dict{pv.Dict}
	}
	if pv.Kind == KindArray {
		out := make([]*Dict, len(pv.Array))
		for i, item := range pv.Array {
			if item.Kind == KindDict {
				out[i] = item.Dict
			}
		}
		return out
	}
	return nil
}

func dictInt(d *Dict, key string, def int64) int {
	v := d.Get(key)
	if v != nil && v.Kind == KindInt {
		return int(v.Int)
	}
	return int(def)
}

// applyPredictor implements the PNG predictors (10..15) used by Flate
// streams such as cross-reference and object streams.
func applyPredictor(parms *Dict, data []byte) ([]byte, error) {
	predictor := dictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}
	if predictor < 10 || predictor > 15 {
		return nil, fmt.Errorf("pdfscan: unsupported predictor %d", predictor)
	}
	colors := dictInt(parms, "Colors", 1)
	bits := dictInt(parms, "BitsPerComponent", 8)
	columns := dictInt(parms, "Columns", 1)
	if colors < 1 || bits != 8 || columns < 1 {
		return nil, errors.New("pdfscan: unsupported predictor parameters")
	}
	bpp := colors
	rowLen := columns * colors
	if len(data)%(rowLen+1) != 0 {
		return nil, errors.New("pdfscan: predictor data length mismatch")
	}

	out := make([]byte, 0, (len(data)/(rowLen+1))*rowLen)
	prev := make([]byte, rowLen)
	for off := 0; off < len(data); off += rowLen + 1 {
		tag := data[off]
		row := data[off+1 : off+1+rowLen]
		recon := make([]byte, rowLen)
		switch tag {
		case 0: // None
			copy(recon, row)
		case 1: // Sub
			for x := range row {
				left := byte(0)
				if x >= bpp {
					left = recon[x-bpp]
				}
				recon[x] = row[x] + left
			}
		case 2: // Up
			for x := range row {
				recon[x] = row[x] + prev[x]
			}
		case 3: // Average
			for x := range row {
				left := byte(0)
				if x >= bpp {
					left = recon[x-bpp]
				}
				recon[x] = row[x] + byte((int(left)+int(prev[x]))/2)
			}
		case 4: // Paeth
			for x := range row {
				var left, upLeft int
				if x >= bpp {
					left = int(recon[x-bpp])
					upLeft = int(prev[x-bpp])
				}
				up := int(prev[x])
				p := left + up - upLeft
				pa, pb, pc := abs(p-left), abs(p-up), abs(p-upLeft)
				var pr int
				switch {
				case pa <= pb && pa <= pc:
					pr = left
				case pb <= pc:
					pr = up
				default:
					pr = upLeft
				}
				recon[x] = row[x] + byte(pr)
			}
		default:
			return nil, fmt.Errorf("pdfscan: unknown PNG predictor tag %d", tag)
		}
		out = append(out, recon...)
		prev = recon
	}
	return out, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
