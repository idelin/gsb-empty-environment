package polyglot

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type detectCase struct {
	name         string
	build        func() []byte
	wantFormat   Format
	wantErrIs    error
	successHints []string
	errorHints   []string
}

func TestDetect(t *testing.T) {
	cases := []detectCase{
		{
			name:         "合法 PNG 单格式",
			build:        buildPNG,
			wantFormat:   FormatPNG,
			successHints: []string{"IEND"},
		},
		{
			name:         "合法 GIF 单格式",
			build:        buildGIF,
			wantFormat:   FormatGIF,
			successHints: []string{"GIF89a", "终止符"},
		},
		{
			name:         "合法 PDF 单格式",
			build:        func() []byte { return buildPDF(nil) },
			wantFormat:   FormatPDF,
			successHints: []string{"%PDF-", "startxref", "%%EOF"},
		},
		{
			name:         "带前置垃圾字节的 PDF 仍可定位",
			build:        func() []byte { return buildPDF([]byte("GARBAGE-PREFIX-")) },
			wantFormat:   FormatPDF,
			successHints: []string{"头位于偏移 15"},
		},
		{
			name:         "合法 ZIP 单格式",
			build:        buildZIP,
			wantFormat:   FormatZIP,
			successHints: []string{"EOCD", "中央目录"},
		},
		{
			name:       "PDF/ZIP 多义文件必须拒绝",
			build:      buildPDFZIPPolyglot,
			wantFormat: FormatUnknown,
			wantErrIs:  ErrAmbiguous,
			errorHints: []string{".pdf", ".zip", "拒绝多义文件"},
		},
		{
			name:       "GIF 后追加 JavaScript 按尾随数据拒绝",
			build:      buildGIFJSPolyglot,
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{".gif: GIF 终止符", "尾随数据"},
		},
		{
			name:       "随机垃圾内容无法解析",
			build:      func() []byte { return []byte("not a real file at all, just plain text\n") },
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{"无法解析为任何受支持的格式"},
		},
		{
			name: "截断的 PNG 块链不完整",
			build: func() []byte {
				png := buildPNG()
				return png[:len(png)-7]
			},
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{".png:"},
		},
		{
			name: "伪造魔数但无 GIF 块链",
			build: func() []byte {
				return append([]byte("GIF89a"), bytes.Repeat([]byte{0x00}, 40)...)
			},
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{".gif:"},
		},
		{
			name: "缺 startxref 的 PDF 拒绝",
			build: func() []byte {
				pdf := buildPDF(nil)
				idx := bytes.Index(pdf, []byte("startxref"))
				return append(pdf[:idx], []byte("BROKEN")...)
			},
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{".pdf:"},
		},
		{
			name: "中央目录签名被破坏的 ZIP 拒绝",
			build: func() []byte {
				zipBytes := buildZIP()
				eocd := findSig(zipBytes, zipEOCDSig, true)
				cdOff := int(binary.LittleEndian.Uint32(zipBytes[eocd+16:]))
				zipBytes[cdOff] = 0x00
				return zipBytes
			},
			wantFormat: FormatUnknown,
			wantErrIs:  ErrUnknown,
			errorHints: []string{".zip:", "中央目录头签名错误"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			format, err := Detect(bytesReaderAt{data: data}, int64(len(data)))
			if format != tc.wantFormat {
				t.Fatalf("格式 = %s，期望 %s", format, tc.wantFormat)
			}
			if tc.wantErrIs == nil {
				if err != nil {
					t.Fatalf("意外错误: %v", err)
				}
			} else if !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("错误 %v 未命中哨兵 %v", err, tc.wantErrIs)
			}

			assertHints := func(hints []string, message string) {
				for _, hint := range hints {
					if !bytes.Contains([]byte(message), []byte(hint)) {
						t.Fatalf("信息 %q 未包含 %q", message, hint)
					}
				}
			}

			if tc.wantErrIs == nil {
				r, brErr := newBoundedReader(bytesReaderAt{data: data}, int64(len(data)))
				if brErr != nil {
					t.Fatal(brErr)
				}
				reason, parseErr := parsers[tc.wantFormat].parse(r)
				if parseErr != nil {
					t.Fatalf("解析器意外失败: %v", parseErr)
				}
				assertHints(tc.successHints, reason)
			} else {
				assertHints(tc.errorHints, err.Error())
			}
		})
	}
}

func TestAmbiguousErrorDetails(t *testing.T) {
	data := buildPDFZIPPolyglot()
	_, err := Detect(bytesReaderAt{data: data}, int64(len(data)))

	var ambErr *AmbiguousError
	if !errors.As(err, &ambErr) {
		t.Fatalf("期望 *AmbiguousError，实际 %T: %v", err, err)
	}
	wantFormats := map[Format]bool{FormatPDF: true, FormatZIP: true}
	if len(ambErr.Formats) != 2 {
		t.Fatalf("命中格式数 = %d，期望 2: %v", len(ambErr.Formats), ambErr.Formats)
	}
	for _, format := range ambErr.Formats {
		if !wantFormats[format] {
			t.Fatalf("意外命中格式 %s", format)
		}
		if ambErr.Reasons[format] == "" {
			t.Fatalf("格式 %s 缺少拒绝原因", format)
		}
	}
}

func TestUnknownErrorCoversEveryFormat(t *testing.T) {
	data := []byte("plain text without any supported structure")
	_, err := Detect(bytesReaderAt{data: data}, int64(len(data)))

	var unknownErr *UnknownError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("期望 *UnknownError，实际 %T", err)
	}
	for _, format := range detectionOrder {
		if unknownErr.Reasons[format] == "" {
			t.Fatalf("格式 %s 缺少失败原因", format)
		}
	}
}

func TestDetectWithExtension(t *testing.T) {
	png := buildPNG()
	if format, err := DetectWithExtension(bytesReaderAt{png}, int64(len(png)), "photo.PNG"); err != nil || format != FormatPNG {
		t.Fatalf("扩展名大小写兼容失败: format=%v err=%v", format, err)
	}

	_, err := DetectWithExtension(bytesReaderAt{png}, int64(len(png)), "photo.gif")
	var mismatch *ExtensionMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, ErrExtensionMismatch) {
		t.Fatalf("期望扩展名不匹配错误，实际 %v", err)
	}
	if mismatch.Declared != FormatGIF || mismatch.Detected != FormatPNG {
		t.Fatalf("不匹配详情错误: %+v", mismatch)
	}

	if _, err := DetectWithExtension(bytesReaderAt{png}, int64(len(png)), "photo.exe"); !errors.Is(err, ErrExtensionMismatch) {
		t.Fatalf("未知扩展名应触发不匹配错误，实际 %v", err)
	}

	polyglot := buildPDFZIPPolyglot()
	if _, err := DetectWithExtension(bytesReaderAt{polyglot}, int64(len(polyglot)), "doc.pdf"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("多义文件应先按多义拒绝，实际 %v", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	data := buildPNG()
	if _, err := Detect(bytesReaderAt{data}, -1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("负大小期望 ErrInvalidSize，实际 %v", err)
	}
	if _, err := Detect(nil, int64(len(data))); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("nil ReaderAt 期望 ErrInvalidSize，实际 %v", err)
	}
	if _, err := Detect(bytesReaderAt{data: data}, 0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("空文件期望 ErrUnknown，实际 %v", err)
	}
}

func TestMustReject(t *testing.T) {
	pdf := buildPDF([]byte("junk"))
	reject, reason := MustReject(bytesReaderAt{pdf}, int64(len(pdf)), "a.pdf")
	if reject {
		t.Fatalf("合法带垃圾前缀 PDF 不应被拒绝: %s", reason)
	}

	polyglot := buildPDFZIPPolyglot()
	reject, reason = MustReject(bytesReaderAt{polyglot}, int64(len(polyglot)), "a.pdf")
	if !reject || !bytes.Contains([]byte(reason), []byte("拒绝多义文件")) {
		t.Fatalf("多义文件必须拒绝，实际 reject=%v reason=%s", reject, reason)
	}
}

// TestCrossCheckStandardLibrary 用标准库解析器交叉验证夹具与判定结果，
// 证明测试样本本身是真实合法文件而非只为骗过本包。
func TestCrossCheckStandardLibrary(t *testing.T) {
	zipBytes := buildZIP()
	reader, err := zip.NewReader(bytesReaderAt{data: zipBytes}, int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("标准库无法打开夹具 ZIP: %v", err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != "hello.txt" {
		t.Fatalf("ZIP 夹具内容异常: %+v", reader.File)
	}

	polyglot := buildPDFZIPPolyglot()
	zipInPolyglot, err := zip.NewReader(bytesReaderAt{data: polyglot}, int64(len(polyglot)))
	if err != nil {
		t.Fatalf("标准库无法打开多义文件中的 ZIP 结构: %v", err)
	}
	if zipInPolyglot.File[0].Name != "hello.txt" {
		t.Fatalf("多义文件 ZIP 条目异常: %q", zipInPolyglot.File[0].Name)
	}

	pdf := buildPDF(nil)
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.7")) {
		t.Fatalf("PDF 夹具头异常")
	}
	if !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("PDF 夹具尾异常")
	}

	if format, err := Detect(bytesReaderAt{polyglot}, int64(len(polyglot))); !errors.Is(err, ErrAmbiguous) || format != FormatUnknown {
		t.Fatalf("多义文件判定错误: format=%v err=%v", format, err)
	}
}

// TestReaderAtBounds 确保底层 ReaderAt 只收到范围内的读取请求。
func TestReaderAtBounds(t *testing.T) {
	data := buildZIP()
	guard := &boundsGuardingReader{data: data}
	if format, err := Detect(guard, int64(len(data))); err != nil || format != FormatZIP {
		t.Fatalf("边界守卫读取器下判定失败: format=%v err=%v", format, err)
	}
}

type boundsGuardingReader struct {
	data []byte
}

func (g *boundsGuardingReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(g.data)) {
		return 0, io.EOF
	}
	return copy(p, g.data[off:]), nil
}
