package polyglot

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// bytesReaderAt 让字节切片满足 io.ReaderAt，测试中模拟上传对象存储句柄。
type bytesReaderAt struct{ data []byte }

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// buildPNG 构造结构合法的最小 PNG：签名 + IHDR + IDAT + IEND，CRC 真实计算。
func buildPNG() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	ihdrData := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdrData[0:], 1) // 宽
	binary.BigEndian.PutUint32(ihdrData[4:], 1) // 高
	ihdrData[8] = 8                             // 位深
	ihdrData[9] = 0                             // 灰度
	writePNGChunk(&buf, "IHDR", ihdrData)
	writePNGChunk(&buf, "IDAT", []byte{})
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, chunkType string, data []byte) {
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	buf.Write(length)
	buf.WriteString(chunkType)
	buf.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	var crcBytes [4]byte
	binary.BigEndian.PutUint32(crcBytes[:], crc.Sum32())
	buf.Write(crcBytes[:])
}

// buildGIF 构造结构合法的最小 GIF89a：1x1 像素、2 色调色板、单个图像。
func buildGIF() []byte {
	var buf bytes.Buffer
	buf.WriteString("GIF89a")
	// 逻辑屏幕描述符：宽1、高1、打包 0x80（全局颜色表，2 色）、背景0、像素比0。
	buf.Write([]byte{0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00})
	buf.Write([]byte{0x00, 0x00, 0x00}) // 颜色 0：黑
	buf.Write([]byte{0xFF, 0xFF, 0xFF}) // 颜色 1：白
	// 图像描述符。
	buf.WriteByte(0x2C)
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00})
	// LZW 最小代码尺寸 + 一个空子块 + 子块终止符。
	buf.Write([]byte{0x02, 0x02, 0x44, 0x01, 0x00})
	buf.WriteByte(0x3B) // GIF Trailer。
	return buf.Bytes()
}

// buildPDF 构造结构合法的最小 PDF；prefix 为允许的前置垃圾字节。
func buildPDF(prefix []byte) []byte {
	var buf bytes.Buffer
	buf.Write(prefix)

	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n",
	}
	offsets := make([]int, 0, len(objects)+1)
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	for _, obj := range objects {
		offsets = append(offsets, buf.Len())
		buf.WriteString(obj)
	}

	xrefOff := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString("0 3\n")
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		buf.WriteString(pad10(off))
		buf.WriteString(" 00000 n \n")
	}
	buf.WriteString("trailer\n<< /Size 3 /Root 1 0 R >>\n")
	buf.WriteString("startxref\n")
	buf.WriteString(itoa(xrefOff))
	buf.WriteString("\n%%EOF\n")
	return buf.Bytes()
}

func pad10(value int) string {
	s := itoa(value)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// buildZIP 用标准库构造包含一个文件的合法 ZIP。
func buildZIP() []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("hello.txt")
	if err != nil {
		panic(err)
	}
	if _, err := entry.Write([]byte("polyglot detection\n")); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// relocateZIP 把 zipBytes 中的全部绝对偏移加上 delta，
// 用于把 ZIP 拼接到 PDF 之后得到 PDF/ZIP 多义文件。
func relocateZIP(zipBytes []byte, delta uint32) []byte {
	out := append([]byte(nil), zipBytes...)

	// EOCD 签名反向定位（测试夹具无注释，取唯一签名即可）。
	eocd := findSig(out, zipEOCDSig, true)
	cdOff := binary.LittleEndian.Uint32(out[eocd+16:])
	cdSize := binary.LittleEndian.Uint32(out[eocd+12:])

	// 中央目录条目：修正自身偏移字段（固定头第 42 字节），并跟随变长字段前进。
	pos := int(cdOff)
	end := int(cdOff + cdSize)
	for pos < end {
		if leUint32(out[pos:]) != zipCentralHeaderSig {
			panic("测试夹具中央目录签名异常")
		}
		nameLen := int(binary.LittleEndian.Uint16(out[pos+28:]))
		extraLen := int(binary.LittleEndian.Uint16(out[pos+30:]))
		commentLen := int(binary.LittleEndian.Uint16(out[pos+32:]))
		binary.LittleEndian.PutUint32(out[pos+42:], binary.LittleEndian.Uint32(out[pos+42:])+delta)
		pos += int(zipCentralFixedLen) + nameLen + extraLen + commentLen
	}

	// 最后修正 EOCD 中的中央目录绝对偏移。
	binary.LittleEndian.PutUint32(out[eocd+16:], cdOff+delta)
	return out
}

func findSig(data []byte, sig uint32, last bool) int {
	found := -1
	for i := 0; i+4 <= len(data); i++ {
		if leUint32(data[i:]) == sig {
			found = i
			if !last {
				return i
			}
		}
	}
	if found < 0 {
		panic("测试夹具中找不到签名")
	}
	return found
}

// buildPDFZIPPolyglot 拼接 PDF 与重定位后的 ZIP，两侧结构均保持有效。
func buildPDFZIPPolyglot() []byte {
	pdf := buildPDF(nil)
	zipPart := relocateZIP(buildZIP(), uint32(len(pdf)))
	return append(append([]byte(nil), pdf...), zipPart...)
}

// buildGIFJSPolyglot 在合法 GIF 终止符后追加 JavaScript，模拟 GIF/JS 多义上传。
func buildGIFJSPolyglot() []byte {
	gif := buildGIF()
	return append(gif, []byte("\n// <script>alert('xss')</script>;\n")...)
}
