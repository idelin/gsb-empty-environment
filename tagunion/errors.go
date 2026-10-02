package tagunion

import "fmt"

// Error 报告编解码失败，携带分支路径与字节偏移，便于精确定位。
type Error struct {
	Path   string // 分支路径，如 "obj[2]" 或 "obj[2].field(amount)"
	Offset int    // 出错位置的字节偏移（相对输入缓冲区起点）
	Msg    string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg)
	}
	return fmt.Sprintf("%s (offset %d): %s", e.Path, e.Offset, e.Msg)
}

func errf(offset int, format string, args ...any) *Error {
	return &Error{Offset: offset, Msg: fmt.Sprintf(format, args...)}
}

func withPath(path string, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		if e.Path == "" {
			e.Path = path
		} else {
			e.Path = path + "." + e.Path
		}
	}
	return err
}
