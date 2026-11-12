package netsec

import "errors"

// 校验失败时返回的哨兵错误。调用方可用 errors.Is 判定具体原因，
// 所有错误都表示拒绝上传，存储层不得因此创建或覆盖脚本。
var (
	// ErrNameTooLong 表示脚本名按 UTF-8 编码超过 128 字节。
	ErrNameTooLong = errors.New("netsec: script name exceeds 128 UTF-8 bytes")
	// ErrNameControlChar 表示脚本名包含控制字符（含 CR、LF、TAB）。
	ErrNameControlChar = errors.New("netsec: script name contains a control character")
	// ErrNameEmpty 表示脚本名为空。
	ErrNameEmpty = errors.New("netsec: script name is empty")
	// ErrProtocol 表示 PUTSCRIPT 命令行不符合 RFC 5804 语法。
	ErrProtocol = errors.New("netsec: malformed PUTSCRIPT command")
	// ErrLiteralLength 表示 {n} 声明的长度非法或超过上限。
	ErrLiteralLength = errors.New("netsec: invalid or oversized literal length")
	// ErrTruncatedBody 表示连接在凑齐声明的 n 字节（及 CRLF 结束符）前结束。
	ErrTruncatedBody = errors.New("netsec: connection ended before full literal was read")
	// ErrTrailingData 表示脚本字面量结束后仍有同行数据，防止偷渡下一条命令。
	ErrTrailingData = errors.New("netsec: unexpected data after script literal")
	// ErrInvalidUTF8 表示脚本体不是合法 UTF-8。
	ErrInvalidUTF8 = errors.New("netsec: script body is not valid UTF-8")
	// ErrSieveSyntax 表示脚本体不符合 Sieve 词法/基本语法。
	ErrSieveSyntax = errors.New("netsec: invalid Sieve script syntax")
	// ErrRequireNotFirst 表示 require 不是脚本的第一条命令。
	ErrRequireNotFirst = errors.New("netsec: require must be the first command")
	// ErrExtensionNotAllowed 表示 require 引用了站点白名单之外的扩展。
	ErrExtensionNotAllowed = errors.New("netsec: required extension is not enabled on this site")
	// ErrRedirectLimit 表示 redirect 命令数超过调用方设定的上限。
	ErrRedirectLimit = errors.New("netsec: redirect count exceeds the per-script limit")
)
