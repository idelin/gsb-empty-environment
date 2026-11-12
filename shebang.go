package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxShebangBytes 是脚本首行解释器指令允许的最大长度。
const MaxShebangBytes = 255

var (
	// ErrBadShebang 表示首行指令不符合“绝对路径 + 至多一个参数”的限定。
	ErrBadShebang = errors.New("netsec: malformed interpreter directive")
	// ErrNotAbsolute 表示解释器路径不是绝对路径。
	ErrNotAbsolute = errors.New("netsec: interpreter path must be absolute")
)

// errNoShebang 是内部信号：当前对象不是解释脚本，链条到此为止。
var errNoShebang = errors.New("netsec: no interpreter directive")

// readShebangLine 从已固定对象的偏移 0 处读取首行，不改变文件偏移。
// 若首行不是 "#!" 指令则返回 errNoShebang；超过长度限制则返回 ErrBadShebang。
func readShebangLine(f *os.File) ([]byte, error) {
	buf := make([]byte, MaxShebangBytes+1)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	if n < 2 || buf[0] != '#' || buf[1] != '!' {
		return nil, errNoShebang
	}
	line := buf[2:]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	} else if n > MaxShebangBytes {
		return nil, fmt.Errorf("%w: directive exceeds %d bytes", ErrBadShebang, MaxShebangBytes)
	}
	return bytes.TrimSuffix(line, []byte{'\r'}), nil
}

// parseShebang 解析 "#!" 之后的内容：解释器绝对路径 + 至多一个可选参数。
func parseShebang(line []byte) (path, arg string, hasArg bool, err error) {
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return "", "", false, fmt.Errorf("%w: missing interpreter path", ErrBadShebang)
	}
	if len(fields) > 2 {
		return "", "", false, fmt.Errorf("%w: at most one interpreter argument", ErrBadShebang)
	}
	path = string(fields[0])
	if !filepath.IsAbs(path) {
		return "", "", false, fmt.Errorf("%w: %q", ErrNotAbsolute, path)
	}
	if len(fields) == 2 {
		arg = string(fields[1])
		hasArg = true
	}
	return path, arg, hasArg, nil
}
