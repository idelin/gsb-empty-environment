package polyglot

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// buildMinimalZIP64 手工构造单条目 ZIP64 归档：
// 经典 EOCD 三个字段全部放占位值，真实值写入 ZIP64 记录，
// 本地头与中央目录都携带 tag=0x0001 的 ZIP64 扩展字段。
func buildMinimalZIP64() []byte {
	var buf bytes.Buffer
	content := []byte("zip64-data")
	name := []byte("z64.txt")
	crc := crc32.ChecksumIEEE(content)

	// 本地文件头：固定 30 字节 + 文件名 + ZIP64 extra（两个大小字段，共 20 字节）。
	localExtra := make([]byte, 20)
	binary.LittleEndian.PutUint16(localExtra[0:], 0x0001)
	binary.LittleEndian.PutUint16(localExtra[2:], 16)
	binary.LittleEndian.PutUint64(localExtra[4:], uint64(len(content)))
	binary.LittleEndian.PutUint64(localExtra[12:], uint64(len(content)))

	localHeader := make([]byte, 30)
	binary.LittleEndian.PutUint32(localHeader[0:], zipLocalHeaderSig)
	binary.LittleEndian.PutUint16(localHeader[4:], 45)
	binary.LittleEndian.PutUint16(localHeader[26:], uint16(len(name)))
	binary.LittleEndian.PutUint16(localHeader[28:], uint16(len(localExtra)))
	buf.Write(localHeader)
	buf.Write(name)
	buf.Write(localExtra)
	buf.Write(content)

	cdOff := uint64(buf.Len())

	// 中央目录 ZIP64 extra：两个大小 + 本地头偏移，共 28 字节。
	cdExtra := make([]byte, 28)
	binary.LittleEndian.PutUint16(cdExtra[0:], 0x0001)
	binary.LittleEndian.PutUint16(cdExtra[2:], 24)
	binary.LittleEndian.PutUint64(cdExtra[4:], uint64(len(content)))
	binary.LittleEndian.PutUint64(cdExtra[12:], uint64(len(content)))
	binary.LittleEndian.PutUint64(cdExtra[20:], 0) // 本地头偏移。

	central := make([]byte, 46)
	binary.LittleEndian.PutUint32(central[0:], zipCentralHeaderSig)
	binary.LittleEndian.PutUint16(central[4:], 45)
	binary.LittleEndian.PutUint16(central[6:], 45)
	binary.LittleEndian.PutUint32(central[16:], crc)
	binary.LittleEndian.PutUint32(central[20:], zipU32Placeholder)
	binary.LittleEndian.PutUint32(central[24:], zipU32Placeholder)
	binary.LittleEndian.PutUint16(central[28:], uint16(len(name)))
	binary.LittleEndian.PutUint16(central[30:], uint16(len(cdExtra)))
	binary.LittleEndian.PutUint32(central[42:], zipU32Placeholder)
	buf.Write(central)
	buf.Write(name)
	buf.Write(cdExtra)
	cdSize := uint64(buf.Len()) - cdOff

	// ZIP64 EOCD 记录（56 字节）：+24/+32 条目数、+40 大小、+48 偏移。
	recordOff := uint64(buf.Len())
	record := make([]byte, 56)
	binary.LittleEndian.PutUint32(record[0:], zip64RecordSig)
	binary.LittleEndian.PutUint64(record[4:], 44) // 记录大小字段 = 56-12。
	binary.LittleEndian.PutUint16(record[12:], 45)
	binary.LittleEndian.PutUint16(record[14:], 45)
	binary.LittleEndian.PutUint64(record[24:], 1)
	binary.LittleEndian.PutUint64(record[32:], 1)
	binary.LittleEndian.PutUint64(record[40:], cdSize)
	binary.LittleEndian.PutUint64(record[48:], cdOff)
	buf.Write(record)

	// ZIP64 EOCD 定位器（20 字节）。
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator[0:], zip64LocatorSig)
	binary.LittleEndian.PutUint64(locator[8:], recordOff)
	binary.LittleEndian.PutUint32(locator[16:], 1)
	buf.Write(locator)

	// 经典 EOCD：三个字段全部占位。
	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:], zipEOCDSig)
	binary.LittleEndian.PutUint16(eocd[10:], zipU16Placeholder)
	binary.LittleEndian.PutUint32(eocd[12:], zipU32Placeholder)
	binary.LittleEndian.PutUint32(eocd[16:], zipU32Placeholder)
	buf.Write(eocd)
	return buf.Bytes()
}

func TestZIP64Archive(t *testing.T) {
	data := buildMinimalZIP64()
	format, err := Detect(bytesReaderAt{data: data}, int64(len(data)))
	if err != nil || format != FormatZIP {
		t.Fatalf("ZIP64 归档判定失败: format=%v err=%v", format, err)
	}

	// 标准库同样应能打开并识别全部条目，证明夹具是真实 ZIP64。
	reader, err := zip.NewReader(bytesReaderAt{data: data}, int64(len(data)))
	if err != nil {
		t.Fatalf("标准库无法打开 ZIP64 夹具: %v", err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != "z64.txt" {
		t.Fatalf("ZIP64 条目异常: %+v", reader.File)
	}

	// 破坏定位器签名后必须报错而不是静默接受。
	broken := append([]byte(nil), data...)
	locatorOff := findSig(broken, zip64LocatorSig, false)
	broken[locatorOff] = 0x00
	if _, err := Detect(bytesReaderAt{data: broken}, int64(len(broken))); err == nil {
		t.Fatal("ZIP64 定位器损坏应当被拒绝")
	}
}

// buildGIFWithComment 在图像前插入 Comment Extension，覆盖扩展块解析路径。
func buildGIFWithComment() []byte {
	gif := buildGIF()
	imageAt := bytes.Index(gif, []byte{0x2C})
	if imageAt < 0 {
		panic("GIF 夹具缺少图像描述符")
	}
	extension := []byte{
		0x21, 0xFE, // Extension Introducer + Comment Label。
		0x05, 'h', 'e', 'l', 'l', 'o', // 一个 5 字节子块。
		0x00, // 子块终止符。
	}
	out := make([]byte, 0, len(gif)+len(extension))
	out = append(out, gif[:imageAt]...)
	out = append(out, extension...)
	out = append(out, gif[imageAt:]...)
	return out
}

func TestGIFWithExtensionBlock(t *testing.T) {
	data := buildGIFWithComment()
	format, err := Detect(bytesReaderAt{data: data}, int64(len(data)))
	if err != nil || format != FormatGIF {
		t.Fatalf("带注释扩展的 GIF 判定失败: format=%v err=%v", format, err)
	}

	// 删除扩展块的子块终止符，必须被子块链校验拒绝。
	broken := append([]byte(nil), data...)
	broken[bytes.Index(broken, []byte("hello"))+5] = 0x41
	if _, err := Detect(bytesReaderAt{data: broken}, int64(len(broken))); err == nil {
		t.Fatal("损坏的扩展子块链应当被拒绝")
	}
}

func TestEmptyZIPAndEOCDComment(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	empty := buf.Bytes()
	if format, err := Detect(bytesReaderAt{empty}, int64(len(empty))); err != nil || format != FormatZIP {
		t.Fatalf("空 ZIP 判定失败: format=%v err=%v", format, err)
	}

	// 标准库写出的归档 EOCD 注释长度为 0；手工追加注释并修正长度字段。
	withComment := append([]byte(nil), empty...)
	withComment = append(withComment, []byte("comment")...)
	eocd := findSig(withComment, zipEOCDSig, true)
	withComment[eocd+20] = 7
	withComment[eocd+21] = 0
	if format, err := Detect(bytesReaderAt{withComment}, int64(len(withComment))); err != nil || format != FormatZIP {
		t.Fatalf("带 EOCD 注释的 ZIP 判定失败: format=%v err=%v", format, err)
	}

	// 注释长度与实际不符必须拒绝。
	liar := append([]byte(nil), withComment...)
	liar[eocd+20] = 8
	if _, err := Detect(bytesReaderAt{liar}, int64(len(liar))); err == nil {
		t.Fatal("EOCD 注释长度伪造应当被拒绝")
	}
}

func TestPDFXRefStream(t *testing.T) {
	// 构造使用 /XRef 交叉引用流的现代 PDF 骨架，startxref 指向流对象。
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	streamOff := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /XRef /Size 1 /W [1 2 1] /Length 4 >>\n")
	buf.WriteString("stream\n")
	buf.WriteString("data")
	buf.WriteString("\nendstream\nendobj\n")
	buf.WriteString("startxref\n")
	buf.WriteString(itoa(streamOff))
	buf.WriteString("\n%%EOF\n")

	data := buf.Bytes()
	format, err := Detect(bytesReaderAt{data: data}, int64(len(data)))
	if err != nil || format != FormatPDF {
		t.Fatalf("xref 流 PDF 判定失败: format=%v err=%v", format, err)
	}
}

func TestTruncatedStructures(t *testing.T) {
	t.Run("GIF 缺少终止符", func(t *testing.T) {
		gif := buildGIF()
		_, err := Detect(bytesReaderAt{gif[:len(gif)-1]}, int64(len(gif)-1))
		if err == nil {
			t.Fatal("缺少 0x3B 终止符应当被拒绝")
		}
	})
	t.Run("ZIP 过短", func(t *testing.T) {
		data := []byte("PK\x03\x04short")
		if _, err := Detect(bytesReaderAt{data}, int64(len(data))); err == nil {
			t.Fatal("过短的伪 ZIP 应当被拒绝")
		}
	})
	t.Run("PNG 块越界", func(t *testing.T) {
		png := buildPNG()
		png[8] = 0xFF // 把首块长度改成巨大值。
		if _, err := Detect(bytesReaderAt{png}, int64(len(png))); err == nil {
			t.Fatal("越界块长度应当被拒绝")
		}
	})
	t.Run("startxref 偏移越界", func(t *testing.T) {
		pdf := buildPDF(nil)
		idx := bytes.Index(pdf, []byte("startxref\n")) + len("startxref\n")
		end := idx
		for pdf[end] >= '0' && pdf[end] <= '9' {
			end++
		}
		corrupt := append([]byte(nil), pdf...)
		copy(corrupt[idx:end], []byte("9999999999")[:end-idx])
		if _, err := Detect(bytesReaderAt{corrupt}, int64(len(corrupt))); err == nil {
			t.Fatal("越界 startxref 偏移应当被拒绝")
		}
	})
}

func TestBoundedReaderGuards(t *testing.T) {
	r, err := newBoundedReader(bytesReaderAt{data: []byte("0123456789")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.readAt(-1, make([]byte, 1)); err == nil {
		t.Fatal("负偏移应当报错")
	}
	if err := r.readAt(8, make([]byte, 4)); err == nil {
		t.Fatal("越界读取应当报错")
	}
	if !r.hasPrefixAt(0, []byte("0123")) {
		t.Fatal("hasPrefixAt 正常匹配失败")
	}
	if r.hasPrefixAt(8, []byte("89ABC")) {
		t.Fatal("hasPrefixAt 越界时不应匹配")
	}
	if _, ok := r.lastIndex(0, 10, []byte("56")); !ok {
		t.Fatal("lastIndex 未找到存在的模式")
	}
	if _, ok := r.indexAt(0, 10, []byte("78")); !ok {
		t.Fatal("indexAt 未找到存在的模式")
	}
	if _, ok := r.indexAt(0, 5, []byte("78")); ok {
		t.Fatal("indexAt 不应在窗口外命中")
	}
}

func TestFormatHelpers(t *testing.T) {
	if FormatUnknown.String() != "UNKNOWN" || FormatPNG.MIME() != "image/png" {
		t.Fatal("格式辅助方法返回异常")
	}
	if FormatByExtension("archive.ZIP") != FormatZIP {
		t.Fatal("扩展名映射失败")
	}
	if FormatByExtension("noext") != FormatUnknown {
		t.Fatal("无扩展名应映射为 Unknown")
	}
	if FormatZIP.Extension() != ".zip" {
		t.Fatal("扩展名访问器异常")
	}

	mismatch := &ExtensionMismatchError{Declared: FormatGIF, Detected: FormatPNG}
	if !bytes.Contains([]byte(mismatch.Error()), []byte("扩展名与实际内容结构不匹配")) {
		t.Fatalf("不匹配错误信息异常: %s", mismatch)
	}
}

func TestParseStartXRefErrors(t *testing.T) {
	if _, err := parseStartXRef([]byte("startxref\nNOTANUMBER")); err == nil {
		t.Fatal("非数字 startxref 应当报错")
	}
	if value, err := parseStartXRef([]byte("startxref\n42 \n%%EOF")); err != nil || value != 42 {
		t.Fatalf("合法 startxref 解析异常: value=%d err=%v", value, err)
	}
}
