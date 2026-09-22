package crcseg

import "errors"

// 分块解析与分段合成阶段可能返回的哨兵错误。
var (
	// ErrMissingData 表示一个数据块在传输/存储层缺失，块本身不可读。
	ErrMissingData = errors.New("crcseg: missing block data")
	// ErrBadData 表示数据块可读到字节，但内容损坏（如块校验失败）。
	ErrBadData = errors.New("crcseg: bad block data")
	// ErrQuota 表示数据总长度超过调用方预先声明的长度额度，
	// 在为完整缓冲区分配内存之前返回。
	ErrQuota = errors.New("crcseg: declared length quota exceeded")
	// ErrLengthOverflow 表示两段长度之和超过 int64 可表示范围。
	ErrLengthOverflow = errors.New("crcseg: segment length overflow")
	// ErrLengthMismatch 表示替换段与被替换段长度不同。
	// 相同摘要、不同长度的段不允许混用。
	ErrLengthMismatch = errors.New("crcseg: segment length mismatch")
	// ErrIndexOutOfRange 表示替换位置不在链的段数范围内。
	ErrIndexOutOfRange = errors.New("crcseg: segment index out of range")
	// ErrInvalidSegment 表示分段元数据自相矛盾（例如长度为 0
	// 却携带非初始值摘要）。
	ErrInvalidSegment = errors.New("crcseg: invalid segment")
	// ErrInvalidParams 表示 CRC 参数非法（例如多项式为 0）。
	ErrInvalidParams = errors.New("crcseg: invalid crc parameters")
)
