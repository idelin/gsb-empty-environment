package gateway

import (
	"context"
	"errors"
)

// Exec 在句柄上执行一次查询。
//
// databaseID/template 必须与句柄绑定的身份和模板摘要一致；args 通过
// 驱动结构化绑定传递，组件不会也无法退化为文本拼接。
//
// 当服务端语句编号因连接重建或模式变化失效时，组件会重新准备并重试一次。
// ctx 取消后，迟到的驱动回调会被识别为旧结果并丢弃，不会覆盖句柄状态。
func (e *Entry) Exec(ctx context.Context, databaseID, template string, args []NamedArg) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var reprepared bool
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		stmt, conn, err := e.validate(e.g, databaseID, template, args)
		if err == ErrConnectionMismatch {
			// 连接代次变化：在当前连接上重新准备后重试。
			if _, _, rerr := e.reprepare(ctx); rerr != nil {
				return nil, rerr
			}
			reprepared = false
			continue
		}
		if err != nil {
			return nil, err
		}

		res, err := e.execOnce(ctx, stmt, conn, args)
		if err == nil {
			return res, nil
		}

		// 在途语句被重准备/关闭/连接重建作废：句柄上已经是新语句，直接复用重试，
		// 不再额外重准备，也不把错误抛给查询宿主。
		if errors.Is(err, ErrInFlightInvalidated) {
			reprepared = false
			continue
		}

		// 服务端编号失效（模式变化/语句被驱逐）：重准备后最多重试一次。
		if !reprepared && (IsUnknownStatementError(err) || IsSchemaChangedError(err)) {
			reprepared = true
			if _, _, rerr := e.reprepare(ctx); rerr != nil {
				return nil, rerr
			}
			continue
		}
		return nil, err
	}
}

// execOnce 在指定的某次服务端语句上发起一次执行，并把回调结果收敛为同步返回。
// 回调按“执行时的语句指针 + 句柄当前指针”判旧：
// 若期间发生了重准备/关闭，迟到回调只做在途计数回收，结果一律丢弃。
func (e *Entry) execOnce(ctx context.Context, stmt *preparedStmt, conn DriverConnection, args []NamedArg) (*Result, error) {
	if !stmt.beginOp() {
		// 语句已作废：触发上层重准备。
		return nil, errUnknownStatement
	}
	e.enterInflight()

	type execResult struct {
		res *Result
		err error
	}
	done := make(chan execResult, 1)

	go func() {
		res, err := conn.ExecStmt(ctx, stmt.serverID, args)

		// 判旧：只有该语句仍是句柄当前语句时，结果才有效。
		e.mu.Lock()
		current := e.stmt == stmt && !e.closed
		e.mu.Unlock()

		stmt.endOp()
		e.leaveInflight()

		if current {
			done <- execResult{res, err}
		}
		// 旧回调：结果丢弃，绝不覆盖新句柄。
	}()

	select {
	case <-ctx.Done():
		// 不等待驱动回调；回调到达后会因判旧或计数回收被安全丢弃。
		return nil, ctx.Err()
	case <-stmt.abort:
		// 语句在在途期间被重准备/关闭/连接重建作废：丢弃结果，交宿主处理。
		return nil, ErrInFlightInvalidated
	case r := <-done:
		return r.res, r.err
	}
}
