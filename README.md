# ariesdb

Go 实现的两个组件（仅标准库，Go 1.11+）：

- **`wal`** — ARIES 风格的预写日志：UPDATE / COMMIT / ABORT / END / CLR /
  CHECKPOINT 记录，长度前缀二进制帧，崩溃截尾自动忽略。
- **`aries`** — 经典 ARIES 崩溃恢复引擎：
  - **Analysis**：从最近的 Checkpoint 重建 DirtyPageTable 与 TransactionTable；
  - **Redo**：从 DPT 最小 recLSN 起重复历史（pageLSN 比较保证幂等）；
  - **Undo**：按 LSN 最大堆反向扫描，为每条被撤销的更新写 CLR
    （undoNextLSN 链接），事务回滚完毕写 END。
- **`shard`** — 分层分片与局部性感知路由引擎：
  - key → 虚拟分片（FNV-32a 取模）；
  - 虚拟分片 → 副本集（rendezvous/HRW 哈希 + splitmix64 混合器，
    节点增删时迁移量最小，支持权重）；
  - 路由时优先选择客户端同 zone 的副本，降低跨节点/跨机房通信；
    无本地副本时确定性回退。RWMutex 保护，无后台 goroutine。

## 运行样例

```sh
go run ./cmd/demo
```

构造 5 个事务（T1、T2 已提交，T3、T4、T5 未提交），Checkpoint 后崩溃，
随后执行三阶段恢复并打印每页最终状态，最后演示局部性感知路由。

## 测试

```sh
go test ./...          # 全部单元测试
go test -race ./...    # 含竞态检测
```

覆盖：崩溃恢复验收场景（3 未提交 + 2 已提交、Checkpoint 后崩溃）、
Redo 幂等性、无 Checkpoint 日志、WAL 截尾/续写、路由边界条件
（空拓扑、单节点、副本数上限、未知 zone 回退、最小迁移、权重、
负载均衡）以及并发读写拓扑的竞态与 goroutine 泄漏断言。
