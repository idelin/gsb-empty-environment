package pixelbudget

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
)

// crcTable 是 PNG chunk CRC 使用的 IEEE 多项式表。
var crcTable = crc32.MakeTable(crc32.IEEE)

// buildChunk 按 PNG 规范拼装一个 chunk：长度 + 类型 + 载荷 + CRC。
func buildChunk(typ string, payload []byte) []byte {
	buf := bytes.NewBuffer(nil)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(payload)))
	buf.Write(length)
	buf.WriteString(typ)
	buf.Write(payload)

	crcInput := append([]byte(typ), payload...)
	crcVal := make([]byte, 4)
	binary.BigEndian.PutUint32(crcVal, crc32.Checksum(crcInput, crcTable))
	buf.Write(crcVal)
	return buf.Bytes()
}

// ihdrPayload 生成 13 字节 IHDR 载荷（8 位真彩色、其余字段取常用默认值）。
func ihdrPayload(width, height uint32) []byte {
	p := make([]byte, 13)
	binary.BigEndian.PutUint32(p[0:4], width)
	binary.BigEndian.PutUint32(p[4:8], height)
	p[8] = 8 // bit depth
	p[9] = 2 // color type: truecolor RGB
	return p
}

// actlPayload 生成 acTL 载荷：num_frames + num_plays。
func actlPayload(frames uint32) []byte {
	p := make([]byte, 8)
	binary.BigEndian.PutUint32(p[0:4], frames)
	binary.BigEndian.PutUint32(p[4:8], 0) // 0 = 无限循环
	return p
}

// fctlPayload 生成最小可用的 fcTL 载荷（26 字节）。
func fctlPayload(seq, width, height uint32) []byte {
	p := make([]byte, 26)
	binary.BigEndian.PutUint32(p[0:4], seq)
	binary.BigEndian.PutUint32(p[4:8], width)
	binary.BigEndian.PutUint32(p[8:12], height)
	// x_offset/y_offset = 0；delay_num=1, delay_den=1；dispose=0, blend=0。
	p[20] = 1
	p[23] = 1
	return p
}

// minimalPNGWithIHDR 构造一个“大声明、短数据”的 PNG：
// IHDR 声明任意宽高（可达 65535），但 IDAT 只是 1 字节占位，
// 足以让头部解析器在图像数据前停下。
func minimalPNGWithIHDR(width, height uint32) []byte {
	buf := bytes.NewBuffer(nil)
	buf.Write(pngSignature)
	buf.Write(buildChunk("IHDR", ihdrPayload(width, height)))
	buf.Write(buildChunk("IDAT", []byte{0x00}))
	buf.Write(buildChunk("IEND", nil))
	return buf.Bytes()
}

// validSmallPNG 用标准库编码一张真实可解码的小 PNG。
func validSmallPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// validSmallJPEG 用标准库编码一张真实可解码的小 JPEG。
func validSmallJPEG(width, height int) []byte {
	img := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio444)
	img.Y[0] = 200
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// minimalJPEGWithSOF 手工拼装只含 SOI/APP0/SOF0 的 JPEG 头部，
// SOF 声明给定宽高，后面不提供任何扫描数据（短数据攻击形态）。
func minimalJPEGWithSOF(width, height uint16) []byte {
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte{0xFF, 0xD8}) // SOI

	// APP0/JFIF 段，长度 16（含长度字段自身 2 字节）。
	app0 := []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
	buf.Write([]byte{0xFF, 0xE0})
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(app0)+2))
	buf.Write(lenBuf)
	buf.Write(app0)

	// SOF0：精度(1) + 高(2) + 宽(2) + 1 个组件（每个 3 字节）。
	sof := make([]byte, 0, 8)
	sof = append(sof, 8)
	dim := make([]byte, 4)
	binary.BigEndian.PutUint16(dim[0:2], height)
	binary.BigEndian.PutUint16(dim[2:4], width)
	sof = append(sof, dim...)
	sof = append(sof, 1, 0x01, 0x11, 0x00) // 一个组件：id=1, 采样=1x1, 量化表=0
	buf.Write([]byte{0xFF, 0xC0})
	binary.BigEndian.PutUint16(lenBuf, uint16(len(sof)+2))
	buf.Write(lenBuf)
	buf.Write(sof)
	return buf.Bytes()
}

// apngWithFrames 基于一张真实可解码的静态 PNG，在 IHDR 之后插入
// acTL/fcTL，并在 IDAT 之后追加每帧的 fcTL/fdAT，构造 APNG。
// 标准库 png 解码器不认识这些新 chunk，会按首帧静态 PNG 正常解码。
func apngWithFrames(frames uint32) []byte {
	base := validSmallPNG(4, 3)

	// 拆出基础 PNG 的签名与全部 chunk。
	r := bytes.NewReader(base)
	sig := make([]byte, 8)
	if _, err := r.Read(sig); err != nil {
		panic(err)
	}
	type chunk struct {
		typ     string
		payload []byte
	}
	var chunks []chunk
	for {
		head := make([]byte, 8)
		if _, err := r.Read(head); err != nil {
			break
		}
		length := binary.BigEndian.Uint32(head[0:4])
		typ := string(head[4:8])
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			panic(err)
		}
		crc := make([]byte, 4)
		if _, err := io.ReadFull(r, crc); err != nil {
			panic(err)
		}
		chunks = append(chunks, chunk{typ: typ, payload: payload})
		if typ == "IEND" {
			break
		}
	}

	out := bytes.NewBuffer(nil)
	out.Write(sig)
	var seq uint32
	for _, c := range chunks {
		switch c.typ {
		case "IHDR":
			out.Write(buildChunk(c.typ, c.payload))
			out.Write(buildChunk("acTL", actlPayload(frames)))
			out.Write(buildChunk("fcTL", fctlPayload(seq, 4, 3)))
			seq++
		case "IDAT":
			out.Write(buildChunk(c.typ, c.payload))
			// 首帧之后的每一帧用 fcTL + fdAT 表达。
			for f := uint32(1); f < frames; f++ {
				out.Write(buildChunk("fcTL", fctlPayload(seq, 4, 3)))
				seq++
				fdat := append([]byte{0, 0, 0, 0}, c.payload...) // 前 4 字节是 fdAT 序号
				binary.BigEndian.PutUint32(fdat[0:4], seq)
				seq++
				out.Write(buildChunk("fdAT", fdat))
			}
		default:
			out.Write(buildChunk(c.typ, c.payload))
		}
	}
	return out.Bytes()
}
