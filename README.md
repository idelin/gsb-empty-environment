# 日志链缺失量证明

多台探针用单调序号上报安全日志，中心需要证明丢失范围而不能把重传计成缺失。写供Go接收宿主调用的区间账本，探针定期签发最高已生成序号和启动代，收到记录可乱序，只有被签发上界覆盖的空洞才确认为缺失；重启代不能混合。支持历史区间查询和重传补齐的更正记录，用小序号全集参考核对重复承诺、冲突内容与边界。

## 模型与不变量

- 每条记录由 `(ProbeID, Gen, Seq)` 定位：`Seq` 在单次启动代内从 1 单调递增；探针重启产生新的 `Gen`，各代序号空间完全隔离，永不合并。
- 探针定期签发 `Watermark{Probe, Gen, High}`，即该代已生成的最高序号。只有落在 `[1, High]` 内且没有记录的序号才确认为缺失；`High` 之外的序号保持 `pending`，不能声称丢失。
- 记录允许乱序与重传：同内容重传记为 `duplicate`，不改变缺失集合；同序号不同内容记为 `conflict`，保留首次内容并计数。
- 水位线必须单调：回退的签发被拒绝（`stale`），重复签发记为 `redundant`，前进的签发返回本次新确认的缺失区间。
- 已确认缺失的序号被迟到重传补齐时，追加不可变的 `Correction` 更正记录，记录首次确认它的水位线与补齐时的水位线。

## 主要 API

- `NewLedger()` / `NewLedgerWithReference(limit)`：创建账本；后者同时维护小序号 `[1, limit]` 的全集参考。
- `IngestRecord(Record) (IngestResult, error)`：接收乱序/重传记录，返回 `accepted`、`duplicate`、`conflict` 以及补齐信息。
- `CommitWatermark(Watermark) (CommitResult, error)`：提交探针签发上界，返回 `advanced`、`redundant`、`stale` 与 `NewlyConfirmed` 区间。
- `QueryRange(probe, gen, from, to)`：历史区间查询，按 `present` / `missing` / `pending` 三类返回合并后的区间。
- `QueryProbe(probe)`：按启动代分别返回该探针所有代的区间视图。
- `Corrections(probe, gen)`：读取该代的重传补齐更正日志。
- `Ledger.Audit()` / `FullSetReference.AuditLedger(l)`：用独立重派生的全集参考核对重复承诺、内容冲突、水位线边界（单调轨迹一致）、present/missing 集合及全集完整性。

所有公开方法均通过互斥锁保证并发安全；账本先加锁、参考后加锁，锁序固定以避免死锁。

## 全集参考如何核对

`FullSetReference` 独立保存小序号宇宙内每次投递的计数、首见内容哈希和每次水位线签发轨迹，不依赖账本的缺口推导。审计时重新派生真值并逐项比对：

- 重复承诺：同一小序号投递次数大于 1。
- 冲突内容：同一序号出现不同哈希。
- 边界：账本水位线轨迹必须与参考观测到的严格递增签发序列逐点一致，回退/重复签发单独标记。
- 集合一致：账本的 present 集合与确认缺失区间必须等于参考派生结果。
- 全集完整：当签发上界覆盖 `[1, limit]` 后，仍有小序号从未投递则报 `incomplete-full-set`。

## 验证

```
go vet ./...
go test -race ./...
```
