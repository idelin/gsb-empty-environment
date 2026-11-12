// Package pixelbudget 在真正解码图像之前，仅解析 PNG（含 APNG）与 JPEG
// 的头部声明，用溢出安全的乘法计算单帧像素与全部帧的总像素，并在超出
// 预算时拒绝调用解码分配回调，防止“大声明、短数据”与 APNG 多帧放大攻击。
package pixelbudget

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"io"
	"math"
)

// Format 标识本包能够识别的图像容器格式。
type Format string

const (
	// FormatPNG 覆盖静态 PNG 与动态 APNG（APNG 是 PNG 的超集）。
	FormatPNG Format = "png"
	// FormatJPEG 覆盖基线与渐进式 JPEG。
	FormatJPEG Format = "jpeg"
)

// Header 是只读取图像头得到的可信声明，全程不涉及像素缓冲分配。
type Header struct {
	// Format 为嗅探到的容器格式。
	Format Format
	// Width 与 Height 是头部声明的单帧宽高，恒大于 0。
	Width  uint32
	Height uint32
	// Frames 是声明的帧数：静态 PNG/JPEG 恒为 1，APNG 取 acTL 的 num_frames。
	Frames uint32
	// FramePixels 是溢出安全计算出的单帧像素数 Width*Height。
	FramePixels uint64
	// TotalPixels 是溢出安全计算出的全部帧像素数 FramePixels*Frames。
	TotalPixels uint64
}

// Limits 描述调用方愿意接受的像素预算。零值字段（连同负数）表示不限制。
type Limits struct {
	// MaxFramePixels 限制单帧像素数（宽乘高）。
	MaxFramePixels int64
	// MaxTotalPixels 限制所有帧的像素总量（单帧像素乘帧数）。
	MaxTotalPixels int64
}

// AllocFunc 是解码分配回调。只有头部通过全部预算检查后才会被调用，
// 调用方可以在其中预分配像素缓冲或记账；返回错误会中止解码。
type AllocFunc func(h Header) error

// DecodeFunc 替换默认的 image.Decode，主要用于测试或接入自定义解码器。
// 实现必须从 r 的当前位置（图像起始处）读取完整图像。
type DecodeFunc func(r io.Reader) (image.Image, string, error)

// ReadSeeker 是 Decode 要求的输入形态：既能流式解析头部，又能回到
// 图像起始位置重新解码。*bytes.Reader 与 *os.File 均天然满足。
type ReadSeeker interface {
	io.Reader
	io.Seeker
}

// capturingReader 位于 bufio 之下游：所有被上游拉取的字节都会落入 prefix，
// 其余字节仍按需从 src 读取。Reader 接口给出的始终是完整原始流，
// 因此头部解析消费过多少字节都能无损重放。
type capturingReader struct {
	src    io.Reader
	prefix *bytes.Buffer
}

func (c *capturingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	if n > 0 {
		c.prefix.Write(p[:n])
	}
	return n, err
}

// Options 控制 Decode 的行为。
type Options struct {
	// Budget 为像素预算，必传且至少设置一项上限。
	Budget Limits
	// OnAlloc 为解码分配回调；为 nil 时不回调，但预算检查仍然生效。
	OnAlloc AllocFunc
	// Decode 自定义解码函数；为 nil 时使用注册了 png/jpeg 的 image.Decode。
	Decode DecodeFunc
}

// mulOverflowSafe 在 uint64 域内做乘法，返回乘积与“是否溢出/超过 math.MaxInt64”。
// 预算以 int64 表达，任何超过 math.MaxInt64 的乘积都无法被合法预算接纳，
// 因此统一视为溢出，调用方应直接拒绝。
func mulOverflowSafe(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	if a > math.MaxInt64/b {
		return 0, true
	}
	return a * b, false
}

// Check 按预算核对头部声明。它只做整数运算、不分配像素缓冲，
// 返回包装了 ErrSingleFrame / ErrTotalPixels 的错误，便于 errors.Is 判别。
func (h Header) Check(limits Limits) error {
	if limits.MaxFramePixels > 0 && int64(h.FramePixels) > limits.MaxFramePixels {
		return fmt.Errorf("%w: %d pixels > limit %d",
			ErrSingleFrame, h.FramePixels, limits.MaxFramePixels)
	}
	if limits.MaxTotalPixels > 0 && int64(h.TotalPixels) > limits.MaxTotalPixels {
		return fmt.Errorf("%w: %d pixels > limit %d",
			ErrTotalPixels, h.TotalPixels, limits.MaxTotalPixels)
	}
	return nil
}

// inspect 是 Inspect 的内部形态：当 capture 非 nil 时，它作为捕获层
// 保存所有从上游拉取的字节，供 DecodeReader 无损重放完整图像。
func inspect(r io.Reader, capture *bytes.Buffer) (Header, *bufio.Reader, io.Reader, error) {
	if r == nil {
		return Header{}, nil, nil, ErrEmpty
	}
	var source io.Reader = r
	if capture != nil {
		source = &capturingReader{src: r, prefix: capture}
	}
	br := bufio.NewReader(source)
	peek, err := br.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return Header{}, br, source, fmt.Errorf("peek image signature: %w", err)
	}
	if len(peek) == 0 {
		return Header{}, br, source, ErrEmpty
	}

	switch {
	case bytes.HasPrefix(peek, pngSignature):
		h, err := inspectPNG(br)
		return h, br, source, err
	case len(peek) >= 2 && peek[0] == 0xFF && peek[1] == 0xD8:
		h, err := inspectJPEG(br)
		return h, br, source, err
	default:
		return Header{}, br, source, ErrUnsupported
	}
}

// Inspect 只解析图像头部并返回尺寸/帧数声明，不做任何像素解码。
// 第二个返回值是带缓冲的读取器，其读取位置已前进到图像数据起点。
func Inspect(r io.Reader) (Header, *bufio.Reader, error) {
	h, br, _, err := inspect(r, nil)
	return h, br, err
}

// Decode 先做头部预算检查，通过后才调用分配回调并真正解码；
// 解码完成后再核对实际尺寸与头部声明，不一致一律拒绝。
// 超预算时绝不会调用 opts.OnAlloc，也不会触发底层图像解码。
// r 必须是可重新读取完整图像的读取器（如 *bytes.Reader、*os.File）；
// 内部会从图像起始位置重新喂给解码器，规避 image.Decode 的格式嗅探
// 只能看到文件开头魔数的限制。
func Decode(r ReadSeeker, opts Options) (image.Image, Header, error) {
	h, _, err := Inspect(r)
	if err != nil {
		return nil, Header{}, err
	}
	if err := h.Check(opts.Budget); err != nil {
		return nil, h, err
	}
	if opts.OnAlloc != nil {
		if err := opts.OnAlloc(h); err != nil {
			return nil, h, err
		}
	}

	// 回到图像起始位置，让 image.Decode 能看到完整的格式魔数。
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, h, fmt.Errorf("rewind reader for decode: %w", err)
	}

	decode := opts.Decode
	if decode == nil {
		decode = image.Decode
	}
	img, _, err := decode(r)
	if err != nil {
		return nil, h, err
	}

	bounds := img.Bounds()
	if int64(bounds.Dx()) != int64(h.Width) || int64(bounds.Dy()) != int64(h.Height) {
		return nil, h, fmt.Errorf("%w: header %dx%d but decoded %dx%d",
			ErrSizeMismatch, h.Width, h.Height, bounds.Dx(), bounds.Dy())
	}
	return img, h, nil
}

// DecodeReader 是 Decode 的流式版本，面向 HTTP 请求体这类不可 Seek 的
// io.Reader。头部检查与分配回调语义与 Decode 完全一致；只有预算通过后
// 才会把剩余数据读入内存缓冲（恶意大声明在这一步之前就已被拒绝）。
func DecodeReader(r io.Reader, opts Options) (image.Image, Header, error) {
	var capture bytes.Buffer
	h, _, source, err := inspect(r, &capture)
	if err != nil {
		return nil, Header{}, err
	}
	if err := h.Check(opts.Budget); err != nil {
		return nil, h, err
	}
	if opts.OnAlloc != nil {
		if err := opts.OnAlloc(h); err != nil {
			return nil, h, err
		}
	}

	// source 是捕获层：已拉取的字节保存在 capture，尚未读取的部分继续
	// 来自上游，二者按原始顺序组成完整图像，解码器看到的就是原封不动的输入。
	replay := io.MultiReader(bytes.NewReader(capture.Bytes()), source)
	decode := opts.Decode
	if decode == nil {
		decode = image.Decode
	}
	img, _, err := decode(replay)
	if err != nil {
		return nil, h, err
	}

	bounds := img.Bounds()
	if int64(bounds.Dx()) != int64(h.Width) || int64(bounds.Dy()) != int64(h.Height) {
		return nil, h, fmt.Errorf("%w: header %dx%d but decoded %dx%d",
			ErrSizeMismatch, h.Width, h.Height, bounds.Dx(), bounds.Dy())
	}
	return img, h, nil
}
