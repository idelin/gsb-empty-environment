package netsec

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"testing"
)

// 10. QP 软换行与字面硬换行混合：行尾空格在硬换行前被剥离，
// 软换行不产生输出，硬换行输出 CRLF。
func TestQPMixedLineEndings(t *testing.T) {
	raw := []byte("a \r\nb= \r\nc=\r\nd")
	m, err := MapPart(raw, EncodingQuotedPrintable, nil)
	if err != nil {
		t.Fatal(err)
	}
	// "a " 行尾空格被剥离后输出硬换行；"b= \r\n" 与 "c=\r\n" 都是软换行，
	// 故 b、c、d 直接拼接，只有第一行产生 CRLF。
	want := "a\r\nbcd"
	if string(m.Decoded) != want {
		t.Fatalf("decoded = %q, want %q", m.Decoded, want)
	}

	// 全部认证时所有实际输出字节都可信，但框架字节依旧不在依赖里。
	full, _ := MapPart(raw, EncodingQuotedPrintable, authRanges(Range{Start: 0, End: int64(len(raw))}))
	for _, b := range full.Bytes {
		if !b.Trusted {
			t.Fatalf("fully authenticated output byte %d must be trusted", b.Decoded)
		}
	}
}

// 11. 空认证集合下所有解码字节均不可信，且依赖切片非空。
func TestNothingAuthenticated(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		enc  Encoding
	}{
		{"identity", []byte("abc"), EncodingIdentity},
		{"qp", []byte("a=42c"), EncodingQuotedPrintable},
		{"base64", []byte("YWJj"), EncodingBase64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := MapPart(tc.raw, tc.enc, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Bytes) == 0 {
				t.Fatal("expected decoded output")
			}
			for _, b := range m.Bytes {
				if b.Trusted {
					t.Fatalf("byte %d trusted with empty auth set", b.Decoded)
				}
				if len(b.Deps) == 0 {
					t.Fatalf("byte %d has no dependencies recorded", b.Decoded)
				}
			}
		})
	}
}

// 12. 合法输入上，本包解码结果必须与标准库逐字节一致（QP）。
func TestQPStdlibParity(t *testing.T) {
	inputs := [][]byte{
		[]byte(""),
		[]byte("plain text"),
		[]byte("soft=\r\nbreak"),
		[]byte("soft=\nbreak"),
		[]byte("hex =E2=82=AC end"),
		[]byte("line1\r\nline2\nline3"),
		[]byte("trailing space \r\nnext"),
		[]byte("upper=41 lower=61 mixed=7f"),
		[]byte("dangling="),
		[]byte("literal=nothex\r\n"),
		[]byte("high bit \xff byte"),
	}
	for _, raw := range inputs {
		want, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("stdlib rejected %q: %v", raw, err)
		}
		m, err := MapPart(raw, EncodingQuotedPrintable, authRanges(Range{Start: 0, End: int64(len(raw))}))
		if err != nil {
			t.Fatalf("MapPart rejected %q: %v", raw, err)
		}
		if !bytes.Equal(m.Decoded, want) {
			t.Fatalf("QP parity mismatch for %q:\n got %q\nwant %q", raw, m.Decoded, want)
		}
	}
}

// 13. 合法输入上，本包解码结果必须与标准库逐字节一致（Base64），
// 包括带折行、填充与无填充的原始编码。
func TestBase64StdlibParity(t *testing.T) {
	inputs := [][]byte{
		[]byte(""),
		[]byte("YWJj"),
		[]byte("Zm9vYg=="),
		[]byte("Zm9v\r\nYg=="),
		[]byte("Zm8="),
		[]byte("Zg=="),
		[]byte("YWJjZGVmZ2g="),
		[]byte(base64.RawStdEncoding.EncodeToString([]byte("unpadded data"))),
	}
	for _, raw := range inputs {
		want, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			// Raw（无填充）输入用 RawStdEncoding 对照。
			want, err = base64.RawStdEncoding.DecodeString(string(raw))
			if err != nil {
				t.Fatalf("stdlib rejected %q: %v", raw, err)
			}
		}
		m, err := MapPart(raw, EncodingBase64, authRanges(Range{Start: 0, End: int64(len(raw))}))
		if err != nil {
			t.Fatalf("MapPart rejected %q: %v", raw, err)
		}
		if !bytes.Equal(m.Decoded, want) {
			t.Fatalf("base64 parity mismatch for %q:\n got %q\nwant %q", raw, m.Decoded, want)
		}
	}
}
