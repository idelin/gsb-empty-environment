# Global Read-View Aligner（分布式事务全局一致性读取对齐器）

分库分表集群的全局快照读取器。各分片以**独立的本地提交时间戳**产生
commit 有序的 CDC 流；对齐器协调这些流，只在全局因果闭合时发布安全
时间戳 `T_Safe` 与一致性快照，保证跨分片转账的借/贷两腿同时可见 —
钱绝不凭空消失。

## 设计要点

- **每分片水位线**：`Ingest` / `Heartbeat` 推进各分片 watermark，
  拒绝时间戳回退（CDC 流必须按 commit 顺序）。
- **因果闭合**：`RegisterTxn` 声明跨分片事务的参与分片；任一事务只要
  还有腿未到达，`Ready()` 为 false，`Snapshot()` 返回 `ErrNotClosed`，
  外部查询视图被阻止。
- **事务原子可见性**：各分片时间戳是独立时钟域，快照绝不按单一标量
  截断事件流；一个事务要么整体进入视图，要么整体在外，快照天然守恒
  （`Snapshot.Balanced()` 校验每笔转账借贷相等）。
- **T_Safe**：所有分片水位线的最小值，即每条流都已知完备的地平线。

## 运行

```sh
go test ./... -v   # 8 个测试覆盖验收边界
go run ./cmd/demo  # 转账场景演示：阻塞 -> 闭合 -> 一致性快照
```

## 文件

- `aligner/aligner.go` — 对齐器实现（仅标准库）
- `aligner/aligner_test.go` — go test 测试
- `cmd/demo/main.go` — 运行样例
