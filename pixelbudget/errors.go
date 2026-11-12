package pixelbudget

import "errors"

// 头部解析或像素预算检查阶段返回的错误，均可通过 errors.Is 判别。
var (
	// ErrEmpty 表示输入为空，无法嗅探图像格式。
	ErrEmpty = errors.New("pixelbudget: empty input")
	// ErrUnsupported 表示输入不是本包支持的 PNG/JPEG 图像。
	ErrUnsupported = errors.New("pixelbudget: unsupported image format")
	// ErrHeader 表示图像头损坏或被截断，无法取得可信的尺寸声明。
	ErrHeader = errors.New("pixelbudget: malformed image header")
	// ErrZeroDimension 表示头部声明的宽或高为 0。
	ErrZeroDimension = errors.New("pixelbudget: zero width or height in header")
	// ErrSingleFrame 表示单帧像素（宽乘高）超出限制，或乘法溢出。
	ErrSingleFrame = errors.New("pixelbudget: single-frame pixel count exceeds budget")
	// ErrTotalPixels 表示所有帧的像素总量（单帧像素乘帧数）超出限制或溢出。
	ErrTotalPixels = errors.New("pixelbudget: total pixel count exceeds budget")
	// ErrSizeMismatch 表示解码后的实际尺寸与头部声明不一致。
	ErrSizeMismatch = errors.New("pixelbudget: decoded dimensions do not match header")
)
