package polyglot

import (
	"fmt"
	"io"
)

// Detect 仅依据文件自身结构判定类型，调用方提供随机读取接口与文件大小。
//
// 返回值规则：
//   - 恰好一种格式解析成功：返回该格式；
//   - 两种及以上格式解析成功：返回 FormatUnknown 与 *AmbiguousError（errors.Is 命中 ErrAmbiguous）；
//   - 没有格式解析成功：返回 FormatUnknown 与 *UnknownError（errors.Is 命中 ErrUnknown）。
func Detect(ra io.ReaderAt, size int64) (Format, error) {
	r, err := newBoundedReader(ra, size)
	if err != nil {
		return FormatUnknown, err
	}

	var matched []Format
	reasons := make(map[Format]string)
	failures := make(map[Format]string)
	for _, format := range detectionOrder {
		reason, parseErr := parsers[format].parse(r)
		if parseErr != nil {
			failures[format] = parseErr.Error()
			continue
		}
		matched = append(matched, format)
		reasons[format] = reason
	}

	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return FormatUnknown, &UnknownError{Reasons: failures}
	default:
		return FormatUnknown, &AmbiguousError{Formats: matched, Reasons: reasons}
	}
}

// DetectWithExtension 在结构判定之外核对调用方声明的文件名/扩展名。
//
// 扩展名无法识别、或与结构判定结果不一致时，返回对应格式与
// *ExtensionMismatchError（errors.Is 命中 ErrExtensionMismatch）；
// 多义与未知内容的错误规则与 Detect 相同。
func DetectWithExtension(ra io.ReaderAt, size int64, filename string) (Format, error) {
	format, err := Detect(ra, size)
	if err != nil {
		return format, err
	}
	declared := FormatByExtension(filename)
	if declared == FormatUnknown {
		return format, &ExtensionMismatchError{Declared: declared, Detected: format}
	}
	if declared != format {
		return format, &ExtensionMismatchError{Declared: declared, Detected: format}
	}
	return format, nil
}

// MustReject 是供上传处理器使用的便捷判定：返回 true 表示该文件必须拒绝，
// 并附带可直接写审计日志的拒绝原因。
func MustReject(ra io.ReaderAt, size int64, filename string) (reject bool, reason string) {
	format, err := DetectWithExtension(ra, size, filename)
	if err != nil {
		return true, err.Error()
	}
	return false, fmt.Sprintf("接受：%s（%s）", format, format.MIME())
}
