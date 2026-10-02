# ARIES 崩溃恢复引擎 + 稀疏数据内存紧凑优化器 (Go)

纯标准库实现，Go 1.11+。

## 结构

- `wal/` — 预写日志：定长头 + CRC32 的记录编码、追加写（LSN=字节偏移）、
  顺序扫描；崩溃留下的撕裂/损坏尾部被安全截断。
- `recovery/` — 经典 ARIES 三阶段重启恢复：
  - **Analysis**：从最近 Checkpoint 出发重建 TransactionTable 与
    DirtyPageTable（含 recLSN），识别 winners/losers；
  - **Redo**：历史重复，从 DPT 最小 recLSN 起重放 UPDATE/CLR，
    以 pageLSN 保证幂等；
  - **Undo**：按 LSN 最大堆反向扫描 loser 事务，为每个被撤销的 UPDATE
    写入 CLR（带 undoNextLSN），CLR 永不重复撤销，事务终结写 END。
- `sparse/` — 海量稀疏向量容器：按密度在 map（稀疏）与 slice（稠密）
  表示间自适应转换，带迟滞避免抖动；离群索引拒绝巨额分配自动降级；
  快照压缩在 delta-varint 与 RLE 两种编码中自适应取小。
- `cmd/demo/` — 可运行样例：构造 5 事务 WAL（2 提交 / 3 未提交）、
  checkpoint 后崩溃，执行三阶段恢复并打印每阶段结果。

## 运行

```sh
go test ./...          # 全部单元测试与验收测试
go test -race ./...    # 并发安全（race detector）
go run ./cmd/demo      # 崩溃恢复 + 稀疏优化器演示
```

## 验收场景（recovery/recovery_test.go）

WAL 含 5 个事务：T1、T3 提交，T2、T4 未提交，T5 崩溃前 abort 未完成；
checkpoint 之后崩溃，且一个含未提交数据的脏页已落盘。恢复后断言：

- Analysis 重建出 5 条 TT 记录（状态正确）与 3 页 DPT；
- Redo 重放 6 条记录（含未提交修改，已落盘页按 pageLSN 跳过）；
- Undo 回滚 4 处 loser 修改并写 4 条 CLR + 3 条 END；
- 磁盘最终状态：已提交数据持久，未提交数据全部复原；
- 二次重启恢复幂等（0 redo / 0 undo / 0 额外写盘）。
