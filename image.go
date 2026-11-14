package netsec

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// ImageFormat is the canonical image container detected by the sanitizer.
type ImageFormat string

const (
	FormatJPEG ImageFormat = "jpeg"
	FormatPNG  ImageFormat = "png"
	FormatWebP ImageFormat = "webp"
	FormatGIF  ImageFormat = "gif"
)

// ContentType returns the canonical MIME type for the format.
func (f ImageFormat) ContentType() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	case FormatWebP:
		return "image/webp"
	case FormatGIF:
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// String implements fmt.Stringer.
func (f ImageFormat) String() string { return string(f) }

// SanitizedImage is an image whose location-bearing metadata has been removed.
type SanitizedImage struct {
	Data        []byte
	ContentType string
	Format      ImageFormat
}

// DetectImageFormat identifies supported image containers from their magic
// bytes. It never trusts a filename or client-supplied content type.
func DetectImageFormat(data []byte) (ImageFormat, bool) {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return FormatJPEG, true
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return FormatPNG, true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return FormatWebP, true
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return FormatGIF, true
	default:
		return "", false
	}
}

// SanitizeImage removes metadata that can carry location information. It
// validates that the bytes match declaredContentType when one is supplied, and
// returns the canonical media type. Unsupported or malformed containers are
// rejected rather than passed through unsanitized.
func SanitizeImage(data []byte, declaredContentType string) (SanitizedImage, error) {
	format, ok := DetectImageFormat(data)
	if !ok {
		return SanitizedImage{}, ErrUnsupported
	}
	if declaredContentType != "" && !contentTypeMatches(declaredContentType, format) {
		return SanitizedImage{}, &AccessError{
			Code:    CodeInvalidImage,
			Message: "declared content type does not match image bytes",
		}
	}

	var (
		clean []byte
		err   error
	)
	switch format {
	case FormatJPEG:
		clean, err = stripJPEGMetadata(data)
	case FormatPNG:
		clean, err = stripPNGMetadata(data)
	case FormatWebP:
		clean, err = stripWebPMetadata(data)
	case FormatGIF:
		clean, err = stripGIFMetadata(data)
	default:
		err = ErrUnsupported
	}
	if err != nil {
		return SanitizedImage{}, err
	}
	return SanitizedImage{
		Data:        clean,
		ContentType: format.ContentType(),
		Format:      format,
	}, nil
}

// StripMetadata is a convenience wrapper around SanitizeImage for callers that
// only need the bytes.
func StripMetadata(data []byte) ([]byte, error) {
	clean, err := SanitizeImage(data, "")
	if err != nil {
		return nil, err
	}
	return clean.Data, nil
}

func contentTypeMatches(declared string, format ImageFormat) bool {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if i := strings.IndexByte(declared, ';'); i >= 0 {
		declared = strings.TrimSpace(declared[:i])
	}
	if declared == "" || declared == "application/octet-stream" || declared == "binary/octet-stream" {
		return true
	}
	switch format {
	case FormatJPEG:
		return declared == "image/jpeg" || declared == "image/jpg" || declared == "image/pjpeg"
	case FormatPNG:
		return declared == "image/png"
	case FormatWebP:
		return declared == "image/webp"
	case FormatGIF:
		return declared == "image/gif"
	default:
		return false
	}
}

// JPEG keeps only a structurally valid JFIF header and the fixed-size Adobe
// colour-transform marker. APP1/EXIF, APP2/ICC, APP13/IPTC, comments, vendor
// segments and any malformed or oversized "safe" marker are removed.
func stripJPEGMetadata(in []byte) ([]byte, error) {
	if len(in) < 4 || in[0] != 0xff || in[1] != 0xd8 {
		return nil, invalidImage("JPEG")
	}
	out := make([]byte, 0, len(in))
	out = append(out, 0xff, 0xd8)

	for i := 2; i < len(in); {
		if in[i] != 0xff {
			return nil, invalidImage("JPEG")
		}
		start := i
		for i < len(in) && in[i] == 0xff {
			i++
		}
		if i >= len(in) {
			return nil, invalidImage("JPEG")
		}
		marker := in[i]
		i++

		switch {
		case marker == 0xd9: // EOI
			out = append(out, 0xff, 0xd9)
			return out, nil
		case marker == 0xda: // SOS: entropy-coded data follows.
			end, ok := jpegScanEnd(in, i)
			if !ok {
				return nil, invalidImage("JPEG")
			}
			out = append(out, in[start:end]...)
			return out, nil
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			// Standalone markers without a length field.
			out = append(out, in[start:i]...)
			continue
		}

		if i+2 > len(in) {
			return nil, invalidImage("JPEG")
		}
		segmentLen := int(binary.BigEndian.Uint16(in[i : i+2]))
		if segmentLen < 2 || i+segmentLen > len(in) {
			return nil, invalidImage("JPEG")
		}

		keep := true
		payload := in[i+2 : i+segmentLen]
		switch {
		case marker >= 0xe0 && marker <= 0xef:
			switch marker {
			case 0xe0:
				keep = validJFIF(payload)
			case 0xee:
				keep = validAdobe(payload)
			default:
				keep = false
			}
		case marker == 0xfe: // COM
			keep = false
		}
		if keep {
			out = append(out, in[start:i+segmentLen]...)
		}
		i += segmentLen
	}
	return nil, invalidImage("JPEG")
}

func validJFIF(payload []byte) bool {
	return len(payload) == 14 && string(payload[:5]) == "JFIF\x00"
}

func validAdobe(payload []byte) bool {
	return len(payload) == 12 && string(payload[:5]) == "Adobe"
}

// jpegScanEnd finds the EOI marker while ignoring byte-stuffed 0xff00 pairs.
func jpegScanEnd(in []byte, start int) (int, bool) {
	for i := start; i+1 < len(in); i++ {
		if in[i] != 0xff {
			continue
		}
		next := in[i+1]
		if next == 0x00 {
			i++
			continue
		}
		if next == 0xd9 {
			return i + 2, true
		}
	}
	return 0, false
}

// PNG keeps all critical chunks plus a small allow-list of rendering-related
// ancillary chunks. EXIF (eXIf), text chunks (which may contain XMP or GPS
// descriptions) and unknown vendor chunks are removed.
func stripPNGMetadata(in []byte) ([]byte, error) {
	const sigLen = 8
	if len(in) < sigLen || !bytes.Equal(in[:sigLen], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return nil, invalidImage("PNG")
	}
	out := make([]byte, 0, len(in))
	out = append(out, in[:sigLen]...)

	for i := sigLen; i < len(in); {
		if i+12 > len(in) {
			return nil, invalidImage("PNG")
		}
		n := int(binary.BigEndian.Uint32(in[i : i+4]))
		if n < 0 || n > len(in)-i-12 {
			return nil, invalidImage("PNG")
		}
		chunkEnd := i + 12 + n
		typ := string(in[i+4 : i+8])
		if !validPNGChunkType(typ) {
			return nil, invalidImage("PNG")
		}
		if !pngChunkKept(typ) {
			i = chunkEnd
			continue
		}
		out = append(out, in[i:chunkEnd]...)
		if typ == "IEND" {
			return out, nil
		}
		i = chunkEnd
	}
	return nil, invalidImage("PNG")
}

func validPNGChunkType(typ string) bool {
	if len(typ) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		c := typ[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

func pngChunkKept(typ string) bool {
	switch typ {
	case "IHDR", "PLTE", "IDAT", "IEND", "cHRM", "cICP", "gAMA", "sBIT", "sRGB", "mDCv", "cLLi",
		"tRNS", "bKGD", "hIST", "pHYs", "sPLT",
		"acTL", "fcTL", "fdAT", "oFFs", "pCAL", "sCAL":
		return true
	default:
		return false
	}
}

// WebP metadata lives in EXIF and XMP chunks. VP8X feature bits are cleared so
// decoders are not told that metadata still exists.
func stripWebPMetadata(in []byte) ([]byte, error) {
	if len(in) < 12 || string(in[:4]) != "RIFF" || string(in[8:12]) != "WEBP" {
		return nil, invalidImage("WebP")
	}
	out := make([]byte, 12, len(in))
	copy(out, in[:12])
	out[4], out[5], out[6], out[7] = 0, 0, 0, 0

	for i := 12; i < len(in); {
		if i+8 > len(in) {
			return nil, invalidImage("WebP")
		}
		n := int(binary.LittleEndian.Uint32(in[i+4 : i+8]))
		if n < 0 || n > len(in)-i-8 {
			return nil, invalidImage("WebP")
		}
		end := i + 8 + n
		paddedEnd := end
		if n%2 == 1 {
			if paddedEnd >= len(in) {
				return nil, invalidImage("WebP")
			}
			paddedEnd++
		}
		typ := string(in[i : i+4])
		if webpChunkKept(typ) {
			chunk := in[i:paddedEnd]
			if typ == "VP8X" && n >= 1 {
				copied := make([]byte, len(chunk))
				copy(copied, chunk)
				copied[8] &^= 0x08 | 0x04 // clear EXIF and XMP feature bits
				chunk = copied
			}
			out = append(out, chunk...)
		}
		i = paddedEnd
	}
	if len(out) < 12 {
		return nil, invalidImage("WebP")
	}
	riffSize := len(out) - 8
	binary.LittleEndian.PutUint32(out[4:8], uint32(riffSize))
	return out, nil
}

func webpChunkKept(typ string) bool {
	switch typ {
	case "VP8 ", "VP8L", "VP8X", "ANIM", "ANMF", "ALPH", "ICCP":
		return true
	default:
		return false
	}
}

// GIF comments and XMP application extensions are removed. The logical screen,
// colour tables, image blocks and non-XMP application extensions survive byte
// for byte.
func stripGIFMetadata(in []byte) ([]byte, error) {
	if len(in) < 13 {
		return nil, invalidImage("GIF")
	}
	header := string(in[:6])
	if header != "GIF87a" && header != "GIF89a" {
		return nil, invalidImage("GIF")
	}
	out := make([]byte, 0, len(in))
	out = append(out, in[:13]...)

	packed := in[10]
	i := 13
	if packed&0x80 != 0 {
		tableSize := 3 * (1 << ((packed & 0x07) + 1))
		if i+tableSize > len(in) {
			return nil, invalidImage("GIF")
		}
		out = append(out, in[i:i+tableSize]...)
		i += tableSize
	}

	appendSubBlocks := func(pos int) (int, []byte, error) {
		start := pos
		for {
			if pos >= len(in) {
				return 0, nil, invalidImage("GIF")
			}
			n := int(in[pos])
			pos++
			if n == 0 {
				return pos, in[start:pos], nil
			}
			if pos+n > len(in) {
				return 0, nil, invalidImage("GIF")
			}
			pos += n
		}
	}

	for i < len(in) {
		switch in[i] {
		case 0x3b: // trailer
			out = append(out, 0x3b)
			return out, nil
		case 0x2c: // image descriptor
			if i+10 > len(in) {
				return nil, invalidImage("GIF")
			}
			start := i
			localPacked := in[i+9]
			i += 10
			if localPacked&0x80 != 0 {
				tableSize := 3 * (1 << ((localPacked & 0x07) + 1))
				if i+tableSize > len(in) {
					return nil, invalidImage("GIF")
				}
				i += tableSize
			}
			if i >= len(in) {
				return nil, invalidImage("GIF")
			}
			i++ // LZW minimum code size
			end, _, err := appendSubBlocks(i)
			if err != nil {
				return nil, err
			}
			out = append(out, in[start:end]...)
			i = end
		case 0x21: // extension
			if i+2 > len(in) {
				return nil, invalidImage("GIF")
			}
			label := in[i+1]
			start := i
			i += 2
			firstEnd, firstBlock, err := appendSubBlocks(i)
			if err != nil {
				return nil, err
			}
			remove := true
			switch label {
			case 0xf9: // graphic control; harmless rendering state
				remove = false
			case 0xff: // application extension
				if len(firstBlock) >= 2 {
					idLen := int(firstBlock[0])
					if idLen > 0 && 1+idLen <= len(firstBlock) {
						appID := string(firstBlock[1 : 1+idLen])
						remove = appID != "NETSCAPE2.0" && appID != "ANIMEXTS1.0"
					}
				}
			}
			end := firstEnd
			// appendSubBlocks stops at the first zero terminator, which is the
			// end of the extension. No extra loop is required.
			if !remove {
				out = append(out, in[start:end]...)
			}
			i = end
		case 0x00:
			// Some encoders leave padding before the trailer.
			i++
		default:
			return nil, invalidImage("GIF")
		}
	}
	return nil, invalidImage("GIF")
}
