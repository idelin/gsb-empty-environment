// Package nestedmp 提供嵌套多部件消息的流式解析与事务隔离组件。
//
// 消息以字节定义，与具体字符集无关：
//   - 行尾只接受 CRLF；
//   - 分隔行只在行首匹配两个连字号、完整边界名和 CRLF；
//   - 结束行在边界名后另有两个连字号，即 "--name--\r\n"；
//   - 边界名为 1 至 70 个 ASCII 字母数字，严格区分大小写；
//   - 同一活动栈中内外层禁止使用完全相同的边界名；
//   - 每个部件头包含类型、唯一名称与 SHA-256 摘要，空行后正文允许任意字节；
//   - 嵌套容器通过子边界头给出下一层边界。
//
// 紧邻分隔行的 CRLF 属于分隔行而不是正文。每层摘要绑定去除该层自身定界符后的
// 原始正文，容器层直接对线上原始字节（含内层定界符）计算，不使用重新编码的子对象。
package nestedmp

import "errors"

// 解析过程中产生的确定性错误。宿主回调返回的错误会被原样包装并保留。
var (
	// ErrClosed 表示解析器已结束（成功 Close、失败或取消）后仍被写入。
	ErrClosed = errors.New("nestedmp: parser already closed")
	// ErrCanceled 表示解析在运行中被显式取消。
	ErrCanceled = errors.New("nestedmp: parsing canceled")
	// ErrMemoryLimit 表示常驻内存超过宿主设定的预算。
	ErrMemoryLimit = errors.New("nestedmp: memory budget exceeded")
	// ErrSyntax 表示字节流不符合题内格式。具体原因在包装错误中给出。
	ErrSyntax = errors.New("nestedmp: malformed message")
	// ErrDigest 表示部件的 SHA-256 摘要与头声明不一致。
	ErrDigest = errors.New("nestedmp: digest mismatch")
	// ErrBoundary 表示边界名非法或活动栈中出现完全相同的边界名。
	ErrBoundary = errors.New("nestedmp: invalid boundary")
	// ErrHeader 表示部件头缺失、重复或格式非法。
	ErrHeader = errors.New("nestedmp: invalid part header")
	// ErrPreamble 表示首个分隔行之前出现了非空序言字节。
	ErrPreamble = errors.New("nestedmp: non-empty preamble")
	// ErrTrailing 表示根结束行之后仍存在非空字节（CRLF 之后的空白另算）。
	ErrTrailing = errors.New("nestedmp: trailing bytes after root close")
	// ErrEOF 表示输入在部件或定界符完整之前结束。
	ErrEOF = errors.New("nestedmp: unexpected end of input")
)
