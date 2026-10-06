# entitygroup

Go 实体分组库：以显式硬关系维护分组。

## 模型

- **必须同组 (`LinkSame`)**：same 边的传递闭包（连通分量）即分组。
- **不得同组 (`LinkForbid`)**：forbid 边两端不得落在同一连通分量。
- **冲突拒绝 + 路径证据**：新增 same 边若使某条 forbid 边两端连通，
  或新增 forbid 边两端已同组，返回 `*ConflictError`，携带
  BFS 路径证据（`PathA`/`PathB`/`SamePath`），本次操作不生效。
- **相似度软建议 (`Suggest`)**：仅记录，绝不自动形成硬关系、不入事务日志。
- **事务撤销 (`Undo`)**：每次成功变更记录逆操作，`Undo` 回滚最近一笔，
  回滚后自动用 `Verify()`（连通模型核对 forbid 不变量）校验一致性。

## 示例

```go
s := group.New()
s.LinkSame("a", "b")
s.LinkSame("b", "c")
s.LinkForbid("c", "d")
err := s.LinkSame("a", "d") // 拒绝：*ConflictError，证据路径 a~b~c 与 d
s.Undo()                    // 撤销最近事务并重新校验
```

## 测试

```sh
GO111MODULE=on go test ./...
```
