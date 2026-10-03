# RGA — Replicated Growable Array (Go)

基于因果树（causal tree）的协同富文本底层数组 CRDT，仅使用 Go 标准库。

## 设计

- 每个字符是一个链表节点，携带全局唯一且全序的 `ID{Seq, Site}` 与因果前驱 `Prev`。
- 节点组织为因果树：某节点的子节点即"在它之后插入"的字符。
- 并发在同一点插入时，兄弟节点按 ID **降序**排列，所有副本收敛到同一序列。
- 删除为墓碑（tombstone）；操作可交换、幂等，乱序到达时自动缓冲等待因果依赖。

## 运行

```sh
go test ./... -v   # 测试（含验收场景）
go run ./cmd/demo  # 运行样例
```

## 验收场景

两人协同编辑文档 `AB`：用户1 在 `A` 后插入 `X`，用户2 并发在 `A` 后插入 `Y`。
合并后两端稳定展示 `AXYB` 或 `AYXB`（本实现按 ID 降序，确定为 `AYXB`），
绝无字符交错或乱序。见 `TestConcurrentInsertAtSamePosition` 与 `Example`。
