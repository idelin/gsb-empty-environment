package smtpdata

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzRoundTrip 用随机字节（强制包含结束标记）和随机读取宽度，
// 验证流式读取器与独立参考实现在任意分段下输出一致、错误一致。
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("a\r\n..b\nc\rd\r\n.\r\n"), 3, 0)
	f.Add([]byte(".\n.\r\r\nx\r\n.\r\n"), 1, 3)
	f.Add([]byte("\r\n.\r\n"), 64, 2)
	f.Add([]byte("..\n\r.\r\n.\r\n"), 2, 1)

	f.Fuzz(func(t *testing.T, raw []byte, readWidth int, cfgKind int) {
		// 保证存在完整结束标记，把随机内容放在标记之前。
		data := make([]byte, 0, len(raw)+5)
		data = append(data, raw...)
		data = append(data, endMarker...)

		if readWidth < 1 {
			readWidth = 1
		}
		if readWidth > 64 {
			readWidth = 64
		}
		cfgs := []Config{
			{},
			{BareLF: Normalize},
			{BareCR: Normalize},
			{BareLF: Normalize, BareCR: Normalize},
		}
		cfg := cfgs[((cfgKind%4)+4)%4]

		refBody, framed := refFrame(data)
		if !framed {
			t.Fatal("seed corpus must contain end marker")
		}
		wantOut, wantErr := refDecode(refBody, cfg)

		r := NewDataReader(bytes.NewReader(data), cfg)
		got, err := readAll(t, r, readWidth)

		if wantErr != nil {
			var pe *ProtocolError
			if !errors.As(err, &pe) {
				t.Fatalf("want ProtocolError, got %v", err)
			}
			if pe.Sequence != wantErr.Sequence || pe.Offset != wantErr.Offset {
				t.Fatalf("error mismatch: got %v want %v", pe, wantErr)
			}
			if !bytes.HasPrefix(wantOut, got) {
				t.Fatalf("reject prefix mismatch: got %q want-prefix-of %q", got, wantOut)
			}
			return
		}
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if !bytes.Equal(got, wantOut) {
			t.Fatalf("output mismatch\ngot  %q\nwant %q", got, wantOut)
		}
		if !r.Terminated() {
			t.Fatal("reader should be terminated")
		}
	})
}
