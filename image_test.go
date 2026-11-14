package netsec_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"example.org/netsec/q0396"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: uint8(x * 20), B: uint8(y * 20), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	base := buf.Bytes()
	if !bytes.HasSuffix(base, []byte{'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}) {
		t.Fatal("unexpected PNG encoder output")
	}
	iend := base[len(base)-12:]
	body := base[:len(base)-12]

	var out bytes.Buffer
	out.Write(body)
	out.Write(pngChunk("eXIf", []byte("II*\x00\x08\x00\x00\x00\x01\x00GPSLatitude=37.7749")))
	out.Write(pngChunk("tEXt", []byte("GPSLatitude\x0037.7749;-122.4194")))
	out.Write(iend)
	return out.Bytes()
}

func pngChunk(typ string, payload []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.WriteString(typ)
	b.Write(payload)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), payload...)))
	return b.Bytes()
}

func TestPNGLocationMetadataIsRemoved(t *testing.T) {
	raw := testPNG(t)
	if !bytes.Contains(raw, []byte("GPSLatitude")) {
		t.Fatal("test fixture is missing metadata")
	}
	clean, err := netsec.StripMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("GPSLatitude")) || bytes.Contains(clean, []byte("37.7749")) {
		t.Fatal("PNG metadata was not removed")
	}
	if !bytes.Contains(clean, []byte("IHDR")) || !bytes.Contains(clean, []byte("IDAT")) || !bytes.Contains(clean, []byte("IEND")) {
		t.Fatal("PNG image data was damaged")
	}
	if bytes.Contains(clean, []byte("eXIf")) || bytes.Contains(clean, []byte("tEXt")) {
		t.Fatal("location-bearing PNG chunk survived")
	}
	if _, err := png.Decode(bytes.NewReader(clean)); err != nil {
		t.Fatalf("sanitized PNG no longer decodes: %v", err)
	}
}

func TestJPEGLocationMetadataIsRemoved(t *testing.T) {
	raw := testJPEG(t)
	clean, err := netsec.StripMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("GPSLatitude")) || bytes.Contains(clean, []byte("37.7749")) {
		t.Fatal("JPEG metadata was not removed")
	}
	if !bytes.HasPrefix(clean, []byte{0xff, 0xd8}) || !bytes.HasSuffix(clean, []byte{0xff, 0xd9}) {
		t.Fatal("JPEG structure was damaged")
	}
	// A JFIF APP0 segment is rendering information, not location data.
	if !bytes.Contains(clean, []byte("JFIF")) {
		t.Fatal("safe JPEG APP0 segment was removed")
	}
	if _, err := jpeg.Decode(bytes.NewReader(clean)); err != nil {
		t.Fatalf("sanitized JPEG no longer decodes: %v", err)
	}
}

func TestContentTypeMustMatchBytes(t *testing.T) {
	if _, err := netsec.SanitizeImage(testJPEG(t), "image/png"); err == nil {
		t.Fatal("mismatched declared content type was accepted")
	}
	if _, err := netsec.SanitizeImage([]byte("not an image"), ""); err == nil {
		t.Fatal("unsupported bytes were accepted")
	}
	got, err := netsec.SanitizeImage(testJPEG(t), "image/jpg")
	if err != nil || got.ContentType != "image/jpeg" || got.Format != netsec.FormatJPEG {
		t.Fatalf("JPEG detection = (%+v, %v)", got, err)
	}
}

func TestWebPLocationMetadataIsRemoved(t *testing.T) {
	var out bytes.Buffer
	out.WriteString("RIFF")
	out.Write([]byte{0, 0, 0, 0})
	out.WriteString("WEBP")
	out.Write(webpChunk("VP8X", []byte{0x0c, 0, 0, 0, 0, 0, 0, 0, 0, 0}))
	out.Write(webpChunk("EXIF", []byte("GPSLatitude=37.7749;GPSLongitude=-122.4194")))
	out.Write(webpChunk("XMP ", []byte("<x:xmpmeta>GPSLatitude=37.7749</x:xmpmeta>")))
	out.Write(webpChunk("VP8 ", []byte{0x01}))
	raw := out.Bytes()
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))

	clean, err := netsec.StripMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("GPSLatitude")) || bytes.Contains(clean, []byte("EXIF")) || bytes.Contains(clean, []byte("XMP ")) {
		t.Fatal("WebP metadata was not removed")
	}
	if !bytes.Contains(clean, []byte("VP8X")) || !bytes.Contains(clean, []byte("VP8 ")) {
		t.Fatal("WebP image chunks were damaged")
	}
	if clean[20]&0x0c != 0 {
		t.Fatal("WebP VP8X metadata feature bits were not cleared")
	}
	if got := binary.LittleEndian.Uint32(clean[4:8]); int(got) != len(clean)-8 {
		t.Fatalf("WebP RIFF size = %d, want %d", got, len(clean)-8)
	}
}

func webpChunk(typ string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString(typ)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
	b.Write(payload)
	if len(payload)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func TestGIFCommentAndXMPMetadataIsRemoved(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	})
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetColorIndex(x, y, uint8((x+y)%2))
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	base := buf.Bytes()
	gctSize := 0
	if base[10]&0x80 != 0 {
		gctSize = 3 * (1 << ((base[10] & 0x07) + 1))
	}
	insertAt := 13 + gctSize
	comment := gifSubBlocksExtension(0xfe, []byte("GPSLatitude=37.7749"))
	xmpID := []byte("XMP DataXMP")
	xmp := []byte{0x21, 0xff, byte(len(xmpID))}
	xmp = append(xmp, xmpID...)
	xmp = append(xmp, byte(len("GPSLongitude=-122.4194")))
	xmp = append(xmp, []byte("GPSLongitude=-122.4194")...)
	xmp = append(xmp, 0)

	raw := make([]byte, 0, len(base)+len(comment)+len(xmp))
	raw = append(raw, base[:insertAt]...)
	raw = append(raw, comment...)
	raw = append(raw, xmp...)
	raw = append(raw, base[insertAt:]...)

	clean, err := netsec.StripMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("GPSLatitude")) || bytes.Contains(clean, []byte("GPSLongitude")) || bytes.Contains(clean, []byte("XMP Data")) {
		t.Fatal("GIF metadata was not removed")
	}
	if _, err := gif.Decode(bytes.NewReader(clean)); err != nil {
		t.Fatalf("sanitized GIF no longer decodes: %v", err)
	}
}

func gifSubBlocksExtension(label byte, payload []byte) []byte {
	out := []byte{0x21, label}
	for len(payload) > 0 {
		n := len(payload)
		if n > 255 {
			n = 255
		}
		out = append(out, byte(n))
		out = append(out, payload[:n]...)
		payload = payload[n:]
	}
	return append(out, 0)
}
