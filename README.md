# Redis库绑定与事务穿透

受限用户只许用 0 号库，却想借 SELECT、MOVE、COPY 的 DB 参数、SWAPDB 或事务里的 SELECT 触达其他库。为Go Redis 代理写可导入的库绑定门禁：连接状态跟踪当前库，pipeline 与 MULTI/EXEC 内的 SELECT 同样生效；允许库之外的 SELECT、MOVE、SWAPDB 与带 DB 参数的 COPY 以错误应答拒绝；EVAL 与 FUNCTION 对受限用户整体拒绝。测管道、事务、命令名大小写、拒绝后的连接状态。

## 用法

```go
import "example.org/netsec/q0611"

// 受限用户：只允许 DB 0，脚本类命令整体拒绝。
gate := netsec.NewGate(netsec.RestrictedConfig())
conn := gate.Open()          // 每个客户端连接对应一个 id
defer gate.Close(conn)

// 代理每读到一条命令（RESP 数组）就调用一次 Check，按顺序处理，
// pipeline 与 MULTI/EXEC 内的命令也不例外。
args, _ := netsec.ReadCommand(reader)
if err := gate.Check(conn, args); err != nil {
    _ = netsec.WriteError(writer, err) // 直接回 "-NOPERM ...\r\n"，不向上游转发
    continue
}
// err == nil 时才把命令转发给后端 Redis。
```

## 门禁规则

- `SELECT n`：`n` 不在允许集合内时回 `NOPERM`，且当前库保持不变；允许的 `SELECT` 立即更新连接当前库，pipeline 与 `MULTI` 中同样生效。
- `MOVE key db`、`SWAPDB db1 db2`：涉及允许库之外的库即拒绝。
- `COPY source destination [DB target] [REPLACE]`：仅当 `DB` 选项指向禁止库时拒绝；同库 COPY 照常放行。
- `EVAL`、`EVALSHA`、`EVAL_RO`、`EVALSHA_RO`、`FCALL`、`FCALL_RO`、`FUNCTION`（含全部子命令）：受限用户整体拒绝。
- 命令名按大写归一化匹配，`select`、`SeLeCt` 等大小写变体同样受控。
- 参数错误（错误 arity、非法库号）回 `ERR`，与权限拒绝 `NOPERM` 通过 `Error.Kind` 区分。
- 拒绝不污染连接状态：当前库与 MULTI 标记均保持原样；`RESET` 恢复到 DB 0。

## 测试

```sh
go test ./...          # 功能测试：管道、事务、大小写、拒绝后状态
go test -race ./...    # 并发安全（含同连接多 goroutine）
```
