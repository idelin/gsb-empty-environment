package pixelbudget

import (
	"bytes"
	"errors"
	"image"
	"io"
	"testing"
	"testing/iotest"
)

// slowReader 包装任意数据，保证每次 Read 最多返回 1 字节，
// 用于证明头部解析与重新缓冲不依赖 Seek 或大块读取。
func slowReader(data []byte) io.Reader {
	return iotest.OneByteReader(bytes.NewReader(data))
}

// TestDecodeReaderSmallImages 验证纯 io.Reader（不可 Seek）上的小图全链路。
func TestDecodeReaderSmallImages(t *testing.T) {
	cases := []struct {
		name        string
		data        []byte
		format      Format
		width       int
		height      int
		framePixels uint64
	}{
		{name: "png", data: validSmallPNG(8, 5), format: FormatPNG, width: 8, height: 5, framePixels: 40},
		{name: "jpeg", data: validSmallJPEG(12, 7), format: FormatJPEG, width: 12, height: 7, framePixels: 84},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allocCalls := 0
			img, h, err := DecodeReader(slowReader(tc.data), Options{
				Budget:  Limits{MaxFramePixels: 1 << 20, MaxTotalPixels: 1 << 20},
				OnAlloc: func(Header) error { allocCalls++; return nil },
			})
			if err != nil {
				t.Fatalf("DecodeReader failed: %v", err)
			}
			if allocCalls != 1 {
				t.Fatalf("alloc calls = %d, want 1", allocCalls)
			}
			if h.Format != tc.format || h.Width != uint32(tc.width) || h.Height != uint32(tc.height) {
				t.Fatalf("unexpected header: %+v", h)
			}
			if h.FramePixels != tc.framePixels || h.TotalPixels != tc.framePixels {
				t.Fatalf("unexpected pixel counts: %+v", h)
			}
			if img.Bounds().Dx() != tc.width || img.Bounds().Dy() != tc.height {
				t.Fatalf("decoded bounds = %v", img.Bounds())
			}
		})
	}
}

// TestDecodeReaderOverBudgetNoAlloc 验证流式输入超预算时同样不触发回调。
func TestDecodeReaderOverBudgetNoAlloc(t *testing.T) {
	data := minimalPNGWithIHDR(65535, 65535)
	allocCalled := false
	_, _, err := DecodeReader(slowReader(data), Options{
		Budget:  Limits{MaxFramePixels: 1 << 20},
		OnAlloc: func(Header) error { allocCalled = true; return nil },
	})
	if !errors.Is(err, ErrSingleFrame) {
		t.Fatalf("err = %v, want ErrSingleFrame", err)
	}
	if allocCalled {
		t.Fatal("allocation callback must not run over a streaming over-budget image")
	}
}

// TestDecodeReaderAPNG 验证流式 APNG 的帧数统计与预算拒绝。
func TestDecodeReaderAPNG(t *testing.T) {
	data := apngWithFrames(8)

	t.Run("total budget exceeded", func(t *testing.T) {
		allocCalled := false
		_, _, err := DecodeReader(bytes.NewReader(data), Options{
			Budget:  Limits{MaxFramePixels: 64, MaxTotalPixels: 64},
			OnAlloc: func(Header) error { allocCalled = true; return nil },
		})
		if !errors.Is(err, ErrTotalPixels) {
			t.Fatalf("err = %v, want ErrTotalPixels", err)
		}
		if allocCalled {
			t.Fatal("allocation callback must not run when APNG total budget is exceeded")
		}
	})

	t.Run("decodes within budget", func(t *testing.T) {
		img, h, err := DecodeReader(slowReader(data), Options{
			Budget: Limits{MaxFramePixels: 64, MaxTotalPixels: 128},
		})
		if err != nil {
			t.Fatalf("DecodeReader failed: %v", err)
		}
		if h.Frames != 8 || h.TotalPixels != 96 {
			t.Fatalf("unexpected header: %+v", h)
		}
		if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
			t.Fatalf("decoded bounds = %v", img.Bounds())
		}
	})
}

// TestDecodeReaderSizeMismatch 验证流式路径上的声明/实际尺寸核对。
func TestDecodeReaderSizeMismatch(t *testing.T) {
	data := validSmallPNG(4, 3)
	fakeDecode := func(io.Reader) (image.Image, string, error) {
		return image.NewRGBA(image.Rect(0, 0, 42, 99)), "png", nil
	}
	_, _, err := DecodeReader(bytes.NewReader(data), Options{
		Budget: Limits{MaxFramePixels: 1 << 20, MaxTotalPixels: 1 << 20},
		Decode: fakeDecode,
	})
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
}

// TestAllocCallbackError 验证分配回调自身返回错误时解码被中止。
func TestAllocCallbackError(t *testing.T) {
	data := validSmallPNG(4, 3)
	wantErr := errors.New("quota exhausted")
	_, _, err := DecodeReader(bytes.NewReader(data), Options{
		Budget:  Limits{MaxFramePixels: 1 << 20},
		OnAlloc: func(Header) error { return wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
