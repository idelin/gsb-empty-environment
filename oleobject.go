package netsec

import (
	"errors"
	"strings"
)

// ErrUnsafeOLEName 表示嵌入 OLE 对象携带的原始文件名无法清洗为安全包名。
var ErrUnsafeOLEName = errors.New("opc: unsafe OLE object package name")

const maxOLENameLength = 255

// oleReserved 是安全包名中不允许出现的字符（解码后）。
const oleReserved = `<>"/:\\|*?` +
	"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" +
	"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f" +
	"\x7f"

// dosReserved 是不允许直接作为包名的 Windows 保留设备名（不含扩展名比较）。
var dosReserved = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	"CONIN$": {}, "CONOUT$": {},
}

// SafeOLEPackageName 从嵌入 OLE 对象携带的原始文件名中提取安全包名。
//
// OLE 复合文档里的 "\1Ole10Native" 流或包显示名可能包含完整路径
// （"C:\\Users\\..\\evil.bin"、"../../etc/passwd"、UNC 路径），也可能带
// 百分号编码。该函数只保留路径的最后一段，并在解码、剥离保留字符后返回
// 可直接用作 OPC 部件名最后一段的安全文件名；无法得到安全名称时返回
// ErrUnsafeOLEName，调用方应改用受控的占位名称而不是自行猜测。
func SafeOLEPackageName(rawName string) (string, error) {
	if rawName == "" || len(rawName) > maxPartNameLength {
		return "", ErrUnsafeOLEName
	}

	// 先把可能的多层百分号编码逐层解开，防止 "%2e%2e%2f" 之类绕过点段判断。
	decoded := repeatedPercentDecode(rawName)

	// 统一两种分隔符；UNC 的前导 "\\" 与盘符都会在取最后一段时自然脱落。
	normalized := strings.ReplaceAll(decoded, "\\", "/")

	// 只取最后一段非空路径分量，丢弃所有目录信息。
	var base string
	for _, segment := range strings.Split(normalized, "/") {
		if segment != "" {
			base = segment
		}
	}
	if base == "" {
		return "", ErrUnsafeOLEName
	}

	if strings.ContainsAny(base, oleReserved) {
		return "", ErrUnsafeOLEName
	}
	// 控制字符之外，NUL 已随 oleReserved 拒绝；再排除前后导点/空格与结尾点。
	if strings.HasPrefix(base, ".") ||
		strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
		return "", ErrUnsafeOLEName
	}
	if len(base) > maxOLENameLength {
		return "", ErrUnsafeOLEName
	}

	stem := strings.ToUpper(base)
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = strings.ToUpper(base[:dot])
	}
	if _, reserved := dosReserved[stem]; reserved {
		return "", ErrUnsafeOLEName
	}
	return base, nil
}

// repeatedPercentDecode 反复解码百分号转义直到内容稳定。
//
// 格式不完整的转义按字面量保留（文件名中合法的 "%" 不应被误杀）；
// 解码出的 NUL 字节直接判定为不安全输入。
func repeatedPercentDecode(input string) string {
	current := input
	for range 8 {
		if !strings.ContainsRune(current, '%') {
			return current
		}
		var builder strings.Builder
		builder.Grow(len(current))
		changed := false
		for i := 0; i < len(current); i++ {
			ch := current[i]
			if ch != '%' || i+2 >= len(current) ||
				!isHex(current[i+1]) || !isHex(current[i+2]) {
				builder.WriteByte(ch)
				continue
			}
			decoded := fromHex(current[i+1])<<4 | fromHex(current[i+2])
			if decoded == 0 {
				return "\x00"
			}
			builder.WriteByte(decoded)
			i += 2
			changed = true
		}
		next := builder.String()
		if !changed {
			return next
		}
		current = next
	}
	return current
}
