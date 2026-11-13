# 带释放回调的对象图解码

`netsec` 是一个可导入的 Go 图解码包。它先完成全部结构校验，再调用宿主构造回调，避免在失败清理路径中重复销毁共享对象。

## 公开格式

- `Record{ID, Type, Children}`：每条记录有唯一 ID、类型名和按位置排列的子 ID 列表。
- `TypeTable`：`map[string][]EdgeKind`，按子 ID 的位置声明 `Own` 或 `Borrow`，边数必须与子 ID 数完全一致。
- `ID` 为 `int64`，但只接受 `[MinID, MaxID]`，即签名 32 位整数范围；根、记录和子 ID 都执行越界检查。
- 最多 `MaxNodes`（1000）条记录。

## 所有权规则

- `Own` 边把销毁责任转移给父对象；`Borrow` 边不转移。
- 从根沿 `Own` 边可达的子图必须无环，根没有 own 入边，其他每个对象恰有一个 own 入边。
- `Borrow` 可以指向自己、祖先或形成环，但目标必须存在，并且位于根的 own 可达集合中。
- 只有 own 可达对象会被构造。其他记录仍会完成 ID、类型和边声明校验。

## 构造与释放

构造顺序按根深度从深到浅；同一深度按 ID 升序，因此 own 子对象一定先于父对象构造。`Construction.Children` 保持记录中的位置顺序：own 子项带有已构造的值，borrow 子项只带稳定 ID，可以指向尚未构造的对象。

任一构造返回错误或 panic 时，已成功构造的对象立即按成功顺序逆序各销毁一次；失败对象不由包销毁，由构造回调自行负责。成功时返回 `*Graph` 根句柄，`Close` 幂等，并按同一顺序逆序逐个调用析构回调，不递归销毁子对象。

```go
records := []netsec.Record{
    {ID: 1, Type: "root", Children: []netsec.ID{2}},
    {ID: 2, Type: "leaf"},
}
types := netsec.TypeTable{"root": {netsec.Own}, "leaf": nil}

graph, err := netsec.Decode(records, types, 1, construct, destroy)
if err != nil {
    return err
}
defer graph.Close()
```

## 测试

`decode_test.go` 使用真实分配/释放计数回调覆盖共享 own 攻击、借用环、每个构造点的失败回滚、构造 panic、1000 节点边界及重复 `Close`，并断言所有非法图都没有触发构造或析构回调。

```sh
go test ./...
```
