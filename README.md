# kvsm — 多分区确定性事务状态机

基于本地确定性日志的多分区键值状态机，仅使用 Go 标准库。

## 设计

- **确定性日志**（`log.go`）：追加式日志，全局递增 `Seq` 定义唯一确定顺序；快照后按切片点截断，内存有界。
- **乱序容忍**（`engine.go` `Deliver`）：有界重排缓冲区将乱序到达的事件（乱序网络包）恢复为日志顺序后再应用；重复投递幂等；缓冲区满则背压报错，保证内存有界。
- **两阶段回放**：`PREPARE` 登记待决事务，`COMMIT`/`ABORT` 触发依赖求解。
- **依赖拓扑排序**：冲突事务（同分区同键）按 `(prepareSeq, txnID)` 构成依赖边；提交若被更早 prepare 的冲突事务阻塞则挂起，按拓扑序确定性地生效，后到 prepare 覆盖先到者。
- **可审计决胜证据**（`Evidence`）：每次挂起/解除/中止都记录规则、胜负方、分区、键、双方 prepare 序号与说明，且跨回放确定一致。
- **崩溃恢复**（`snapshot.go`）：快照捕获切片点状态（含待决事务与挂起提交），JSON 持久化；恢复后从 `LastSeq+1` 重放日志后缀，状态与全量回放一致。

## 运行

```sh
go test ./... -v     # 测试
go run cmd/demo/main.go   # 运行样例
```

## 验收边界覆盖

- `TestDeterministicUnderPacketReordering`：T1/T2 跨分区 0/1 并发交错，注入 200 种随机乱序投递，最终状态全部一致。
- `TestSnapshotSlicePointReplay` / `TestCrashRecoveryFromDisk`：快照切片点后重放日志后缀（含落盘恢复），状态与全量回放完全一致。
- `TestConflictEvidenceIsAuditable` / `TestAbortResolvesConflict`：未决冲突输出可审计决胜证据。
- `TestBoundedMemory`：重排缓冲区与日志截断的内存上界。
