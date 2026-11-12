# Cap'n Proto RPC导出表隔离

攻击者对Cap'n Proto RPC服务端猜导出ID发Call，或用多余的Release把引用计数减成负数。给Go服务端写供消息分发器调用的导出表：ID按连接隔离，Release超过计数拒绝，释放的ID在对端确认前不得重分配；衰减后的能力只暴露调用方的方法白名单，目标不在本连接导出表则断开连接。用猜ID、超额Release和ID复用验证。

## 实现

`ExportTable` 是每条 RPC 连接独占的导出表，不能跨连接共享。消息分发器使用 `Dispatch` / `DispatchFrom` 处理 Call：

```go
table := netsec.NewExportTable(connID)

id, err := table.Export(cap, methodRead, methodList) // 未列出方法默认拒绝
res, err := table.Dispatch(ctx, netsec.Call{
    Target: id,
    Method: methodRead,
    Params: params,
})
if netsec.IsDisconnectRequired(err) {
    // 未知目标或跨连接引用：立即关闭该连接
}

// 对端 Release 消息：count 超过当前计数时整个操作被拒绝，计数不变。
if err := table.Release(id, count); err != nil {
    // 协议违规，不执行任何部分扣减
}

// 对端确认旧引用不会再出现后，才允许复用该数字 ID。
if err := table.ConfirmRelease(id); err != nil { /* ... */ }
```

安全性质：

- `ExportID` 只在其所属连接表中有效；`DispatchFrom` 还会拒绝串表调用。
- `Release` 先比较后扣减，溢出时原子拒绝；重复释放不会把计数减成负数。
- 计数归零的 ID 进入隔离区，`Resolve` 立即视为未知目标；只有 `ConfirmRelease` 后才回到可复用池。
- `Export` 使用默认拒绝的方法白名单，返回的 `Export` 视图在 `Call` 时再次校验不可变内部白名单。
- `Dispatch` 遇到不在本连接导出表中的目标会返回 `ErrUnknownExport` 并关闭表；`IsDisconnectRequired` 可供上层统一处理。

验证命令：

```sh
go test ./...
go test -race ./...
```
