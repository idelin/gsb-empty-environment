package gateway

import (
	"context"
	"errors"
)

// ParamType 是绑定参数的声明类型。参数一律以结构化绑定传递，
// 组件永远不会把参数拼进 SQL 文本。
type ParamType string

const (
	ParamInt64  ParamType = "int64"
	ParamFloat  ParamType = "float64"
	ParamString ParamType = "string"
	ParamBytes  ParamType = "bytes"
	ParamBool   ParamType = "bool"
	ParamNull   ParamType = "null"
)

// NamedArg 是驱动侧结构化参数绑定的最小载体。
// Ordinal 从 1 开始，对应服务端语句的参数槽位。
type NamedArg struct {
	Ordinal int
	Type    ParamType
	Value   any
}

// Result 是一次异步执行的结果。
type Result struct {
	Rows     [][]any
	Affected int64
}

// DriverConnection 抽象一条物理数据库连接。
//
// 服务端语句编号（ServerStmtID）只在“当前这条物理连接”内有效；
// 连接重建后编号即作废。连接实现必须保证：
//   - Prepare/ExecStmt/CloseStmt 可被并发调用；
//   - ExecStmt 的 done 回调恰好触发一次，且可以在调用返回后再触发。
type DriverConnection interface {
	// ID 是物理连接实例的稳定标识，重建后的连接必须返回新标识。
	ID() string

	// DatabaseID 返回该连接当前绑定的数据库/租户身份。
	DatabaseID() string

	// Prepare 在服务端创建预处理语句，返回仅在本连接内有效的语句编号。
	Prepare(ctx context.Context, template string, paramTypes []ParamType) (serverStmtID uint64, err error)

	// ExecStmt 按编号执行预处理语句。args 以结构化绑定方式传递，
	// 驱动不得退化为文本拼接。完成后必须调用 done（可异步、可在返回后）。
	ExecStmt(ctx context.Context, serverStmtID uint64, args []NamedArg) (result *Result, err error)

	// CloseStmt 关闭服务端语句。在途执行未结束前不得调用。
	CloseStmt(ctx context.Context, serverStmtID uint64) error
}

// IsUnknownStatementError 判断驱动错误是否表示“服务端语句编号已失效”
// （连接重建、服务端驱逐或模式变化导致编号不再存在）。
// 组件据此做一次重准备后重试。
func IsUnknownStatementError(err error) bool {
	var unknown interface{ UnknownStatement() bool }
	return errors.As(err, &unknown) && unknown.UnknownStatement()
}

// IsSchemaChangedError 判断驱动错误是否表示模板依赖的模式已变化，
// 需要重准备语句。
func IsSchemaChangedError(err error) bool {
	var changed interface{ SchemaChanged() bool }
	return errors.As(err, &changed) && changed.SchemaChanged()
}

var (
	// ErrClosed 表示句柄或网关已关闭。
	ErrClosed = errors.New("gateway: statement handle is closed")
	// ErrInFlightInvalidated 表示在途执行期间，其底层服务端语句被重准备、
	// 关闭或连接重建作废。结果已被丢弃，由查询宿主决定是否重试。
	ErrInFlightInvalidated = errors.New("gateway: in-flight execution invalidated by re-prepare, close or connection rebuild")
	// ErrIdentityMismatch 表示调用方提供的数据库身份与句柄绑定的身份不一致。
	ErrIdentityMismatch = errors.New("gateway: database identity does not match handle binding")
	// ErrTemplateMismatch 表示模板摘要与句柄绑定的摘要不一致。
	ErrTemplateMismatch = errors.New("gateway: template digest does not match handle binding")
	// ErrConnectionMismatch 表示底层物理连接代次已变化，旧句柄上的服务端编号失效。
	ErrConnectionMismatch = errors.New("gateway: physical connection generation changed; statement must be re-prepared")
	// ErrArgCount 表示实参数量与句柄声明的参数数量不一致。
	ErrArgCount = errors.New("gateway: argument count does not match prepared statement")
	// ErrArgType 表示实参类型与句柄声明的参数类型不一致。
	ErrArgType = errors.New("gateway: argument type does not match prepared statement")
)

type argCountError struct {
	want, got int
}

func (e *argCountError) Error() string {
	return ErrArgCount.Error() + ": want " + itoa(e.want) + ", got " + itoa(e.got)
}
func (e *argCountError) Unwrap() error { return ErrArgCount }

type argTypeError struct {
	ordinal int
	want    ParamType
	got     ParamType
}

func (e *argTypeError) Error() string {
	return ErrArgType.Error() + ": ordinal " + itoa(e.ordinal) + " want " + string(e.want) + ", got " + string(e.got)
}
func (e *argTypeError) Unwrap() error { return ErrArgType }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
