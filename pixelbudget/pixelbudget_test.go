package pixelbudget

import (
	"bytes"
	"errors"
	"image"
	"io"
	"math"
	"testing"
)

// TestMulOverflowSafe 验证乘法本身的溢出安全性：边界乘积正确，
// 超过 math.MaxInt64 的乘积返回溢出标志而不是回绕。
func TestMulOverflowSafe(t *testing.T) {
	cases := []struct {
		name     string
		a, b     uint64
		want     uint64
		overflow bool
	}{
		{name: "zero", a: 0, b: math.MaxInt64, want: 0},
		{name: "small", a: 640, b: 480, want: 307200},
		{name: "max square", a: 65535, b: 65535, want: 4294836225},
		{name: "max int64 boundary", a: math.MaxInt64, b: 1, want: math.MaxInt64},
		{name: "overflow maxuint64", a: math.MaxInt64, b: 2, overflow: true},
		{name: "overflow huge", a: 1 << 40, b: 1 << 30, overflow: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, overflow := mulOverflowSafe(tc.a, tc.b)
			if overflow != tc.overflow {
				t.Fatalf("overflow = %v, want %v", overflow, tc.overflow)
			}
			if !overflow && got != tc.want {
				t.Fatalf("product = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestHugePNGDeclaration 是“65535 乘 65535、短数据”场景：
// 头部解析必须得到精确乘积，且在预算不足时拒绝、绝不触发分配回调。
func TestHugePNGDeclaration(t *testing.T) {
	const maxDim = 65535
	data := minimalPNGWithIHDR(maxDim, maxDim)

	h, _, err := Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if h.Format != FormatPNG || h.Width != maxDim || h.Height != maxDim || h.Frames != 1 {
		t.Fatalf("unexpected header: %+v", h)
	}
	const wantPixels = uint64(maxDim) * uint64(maxDim) // 4294836225
	if h.FramePixels != wantPixels || h.TotalPixels != wantPixels {
		t.Fatalf("pixels = %d/%d, want %d", h.FramePixels, h.TotalPixels, wantPixels)
	}

	t.Run("rejected by frame budget without alloc", func(t *testing.T) {
		allocCalled := false
		_, _, err := Decode(bytes.NewReader(data), Options{
			Budget:  Limits{MaxFramePixels: 1 << 20, MaxTotalPixels: math.MaxInt64},
			OnAlloc: func(Header) error { allocCalled = true; return nil },
		})
		if !errors.Is(err, ErrSingleFrame) {
			t.Fatalf("err = %v, want ErrSingleFrame", err)
		}
		if allocCalled {
			t.Fatal("allocation callback must not run when over budget")
		}
	})

	t.Run("rejected by total budget", func(t *testing.T) {
		err := h.Check(Limits{MaxFramePixels: math.MaxInt64, MaxTotalPixels: 1 << 20})
		if !errors.Is(err, ErrTotalPixels) {
			t.Fatalf("err = %v, want ErrTotalPixels", err)
		}
	})

	t.Run("accepted when budget is large enough", func(t *testing.T) {
		if err := h.Check(Limits{MaxFramePixels: math.MaxInt64}); err != nil {
			t.Fatalf("generous budget rejected: %v", err)
		}
	})
}

// TestAPNGMultiFrame 验证 APNG 的帧数被计入总像素：单帧不超限，
// 但多帧放大后的总像素超限时同样拒绝，且不调用分配回调。
func TestAPNGMultiFrame(t *testing.T) {
	const frames = uint32(8)
	data := apngWithFrames(frames)

	h, _, err := Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if h.Format != FormatPNG || h.Width != 4 || h.Height != 3 {
		t.Fatalf("unexpected dimensions: %+v", h)
	}
	if h.Frames != frames {
		t.Fatalf("frames = %d, want %d", h.Frames, frames)
	}
	const framePixels = uint64(4 * 3)
	if h.FramePixels != framePixels || h.TotalPixels != framePixels*uint64(frames) {
		t.Fatalf("pixels = %d/%d", h.FramePixels, h.TotalPixels)
	}

	t.Run("single-frame budget passes but total budget fails", func(t *testing.T) {
		allocCalled := false
		_, _, err := Decode(bytes.NewReader(data), Options{
			// 单帧 12 像素远低于上限；总量 96 像素被 64 的总预算挡住。
			Budget:  Limits{MaxFramePixels: 64, MaxTotalPixels: 64},
			OnAlloc: func(Header) error { allocCalled = true; return nil },
		})
		if !errors.Is(err, ErrTotalPixels) {
			t.Fatalf("err = %v, want ErrTotalPixels", err)
		}
		if allocCalled {
			t.Fatal("allocation callback must not run when total budget is exceeded")
		}
	})

	t.Run("within total budget decodes first frame", func(t *testing.T) {
		allocCalled := false
		img, got, err := Decode(bytes.NewReader(data), Options{
			Budget:  Limits{MaxFramePixels: 64, MaxTotalPixels: 128},
			OnAlloc: func(Header) error { allocCalled = true; return nil },
		})
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if !allocCalled {
			t.Fatal("allocation callback should run after budget passes")
		}
		if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
			t.Fatalf("decoded bounds = %v", img.Bounds())
		}
		if got.Frames != frames {
			t.Fatalf("header frames = %d, want %d", got.Frames, frames)
		}
	})
}

// TestSizeMismatch 验证解码后的实际尺寸与头部声明不一致时被拒绝。
func TestSizeMismatch(t *testing.T) {
	data := validSmallPNG(4, 3)

	// 自定义解码器返回与 IHDR 声明不同的尺寸，模拟被篡改/歧义的图像。
	fakeDecode := func(io.Reader) (image.Image, string, error) {
		fake := image.NewRGBA(image.Rect(0, 0, 42, 99))
		return fake, "png", nil
	}

	allocCalled := false
	_, _, err := Decode(bytes.NewReader(data), Options{
		Budget:  Limits{MaxFramePixels: math.MaxInt64, MaxTotalPixels: math.MaxInt64},
		OnAlloc: func(Header) error { allocCalled = true; return nil },
		Decode:  fakeDecode,
	})
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
	// 预算通过后回调会先执行；尺寸核对失败发生在解码之后。
	if !allocCalled {
		t.Fatal("allocation callback should have run before decoding")
	}
}

// TestNormalSmallImages 验证普通小 PNG/JPEG 全链路通过并返回正确尺寸。
func TestNormalSmallImages(t *testing.T) {
	t.Run("png", func(t *testing.T) {
		data := validSmallPNG(8, 5)
		img, h, err := Decode(bytes.NewReader(data), Options{
			Budget: Limits{MaxFramePixels: 1 << 20, MaxTotalPixels: 1 << 20},
		})
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if h.Format != FormatPNG || h.Width != 8 || h.Height != 5 || h.Frames != 1 {
			t.Fatalf("unexpected header: %+v", h)
		}
		if img.Bounds().Dx() != 8 || img.Bounds().Dy() != 5 {
			t.Fatalf("decoded bounds = %v", img.Bounds())
		}
	})

	t.Run("jpeg", func(t *testing.T) {
		data := validSmallJPEG(12, 7)
		img, h, err := Decode(bytes.NewReader(data), Options{
			Budget: Limits{MaxFramePixels: 1 << 20, MaxTotalPixels: 1 << 20},
		})
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if h.Format != FormatJPEG || h.Width != 12 || h.Height != 7 || h.Frames != 1 {
			t.Fatalf("unexpected header: %+v", h)
		}
		if img.Bounds().Dx() != 12 || img.Bounds().Dy() != 7 {
			t.Fatalf("decoded bounds = %v", img.Bounds())
		}
	})
}

// TestHugeJPEGDeclaration 验证 JPEG 短数据攻击：SOF 声明 65535x65535，
// 头部即可判定超限，无需接触扫描数据。
func TestHugeJPEGDeclaration(t *testing.T) {
	const maxDim = 65535
	data := minimalJPEGWithSOF(maxDim, maxDim)

	h, _, err := Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if h.Format != FormatJPEG || h.Width != maxDim || h.Height != maxDim || h.Frames != 1 {
		t.Fatalf("unexpected header: %+v", h)
	}
	const wantPixels = uint64(maxDim) * uint64(maxDim)
	if h.FramePixels != wantPixels || h.TotalPixels != wantPixels {
		t.Fatalf("pixels = %d/%d, want %d", h.FramePixels, h.TotalPixels, wantPixels)
	}

	allocCalled := false
	_, _, err = Decode(bytes.NewReader(data), Options{
		Budget:  Limits{MaxFramePixels: 1 << 20},
		OnAlloc: func(Header) error { allocCalled = true; return nil },
	})
	if !errors.Is(err, ErrSingleFrame) {
		t.Fatalf("err = %v, want ErrSingleFrame", err)
	}
	if allocCalled {
		t.Fatal("allocation callback must not run for over-budget JPEG")
	}
}

// TestMalformedAndUnsupported 覆盖空输入、非图像与截断头部。
func TestMalformedAndUnsupported(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, _, err := Inspect(bytes.NewReader(nil)); !errors.Is(err, ErrEmpty) {
			t.Fatalf("err = %v, want ErrEmpty", err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		if _, _, err := Inspect(bytes.NewReader([]byte("GIF89a........"))); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	})

	t.Run("truncated png header", func(t *testing.T) {
		data := minimalPNGWithIHDR(16, 16)
		if _, _, err := Inspect(bytes.NewReader(data[:12])); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})

	t.Run("truncated jpeg before sof", func(t *testing.T) {
		if _, _, err := Inspect(bytes.NewReader([]byte{0xFF, 0xD8, 0xFF, 0xE0})); !errors.Is(err, ErrHeader) {
			t.Fatalf("err = %v, want ErrHeader", err)
		}
	})
}

// TestZeroDimension 确保 0 宽/高的声明被直接拒绝。
func TestZeroDimension(t *testing.T) {
	if _, _, err := Inspect(bytes.NewReader(minimalPNGWithIHDR(0, 100))); !errors.Is(err, ErrZeroDimension) {
		t.Fatalf("png zero width: err = %v, want ErrZeroDimension", err)
	}
	if _, _, err := Inspect(bytes.NewReader(minimalJPEGWithSOF(100, 0))); !errors.Is(err, ErrZeroDimension) {
		t.Fatalf("jpeg zero height: err = %v, want ErrZeroDimension", err)
	}
}
