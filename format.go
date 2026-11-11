package polyglot

import (
	"path/filepath"
	"strings"
)

// Format 标识一种受支持的文件格式。
type Format uint8

const (
	// FormatUnknown 表示无法识别的格式。
	FormatUnknown Format = iota
	// FormatPNG 对应 image/png。
	FormatPNG
	// FormatGIF 对应 image/gif。
	FormatGIF
	// FormatPDF 对应 application/pdf。
	FormatPDF
	// FormatZIP 对应 application/zip。
	FormatZIP
)

// detectionOrder 固定检测顺序，保证错误信息与测试断言稳定。
var detectionOrder = []Format{FormatPNG, FormatGIF, FormatPDF, FormatZIP}

// extensionFormats 为允许的扩展名到格式的映射，键统一为小写且带点。
var extensionFormats = map[string]Format{
	".png": FormatPNG,
	".gif": FormatGIF,
	".pdf": FormatPDF,
	".zip": FormatZIP,
}

// String 返回格式的短名称。
func (f Format) String() string {
	switch f {
	case FormatPNG:
		return "PNG"
	case FormatGIF:
		return "GIF"
	case FormatPDF:
		return "PDF"
	case FormatZIP:
		return "ZIP"
	default:
		return "UNKNOWN"
	}
}

// Extension 返回该格式的规范扩展名（含点）。
func (f Format) Extension() string {
	switch f {
	case FormatPNG:
		return ".png"
	case FormatGIF:
		return ".gif"
	case FormatPDF:
		return ".pdf"
	case FormatZIP:
		return ".zip"
	default:
		return ""
	}
}

// MIME 返回该格式的 MIME 类型；未知格式返回 application/octet-stream。
func (f Format) MIME() string {
	switch f {
	case FormatPNG:
		return "image/png"
	case FormatGIF:
		return "image/gif"
	case FormatPDF:
		return "application/pdf"
	case FormatZIP:
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

// FormatByExtension 根据文件名或扩展名返回对应格式，无法识别时返回 FormatUnknown。
func FormatByExtension(name string) Format {
	ext := strings.ToLower(filepath.Ext(name))
	return extensionFormats[ext]
}

// parser 是各格式结构校验器的统一接口。
// 返回的字符串为成功时的结构定位说明，用于多义命中的拒绝原因。
type parser interface {
	parse(r *boundedReader) (string, error)
}

// parsers 与 detectionOrder 一一对应。
var parsers = map[Format]parser{
	FormatPNG: pngParser{},
	FormatGIF: gifParser{},
	FormatPDF: pdfParser{},
	FormatZIP: zipParser{},
}
