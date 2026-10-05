# migsim — 块迁移模拟器

模拟共享块从旧卷到新卷的在线迁移：迁移期间多个对象仍可增删引用、
读写句柄，迁移按块代际（generation）切换地址，回收等待旧句柄释放。

## 设计

- **代际地址切换**：每个块维护 `gen -> ReplicaRef` 映射。`BeginMigration`
  快照源摘要，`Migrate` 复制、校验后通过 `ApplyReceipt` 把当前代从
  `gen` 切到 `gen+1`，指向新卷副本。
- **句柄固定副本**：`Open` 固定当时的代与物理副本；切换后旧句柄仍读旧
  副本，新句柄读新副本。`Reclaim` 只释放无句柄固定的非当前代副本。
- **复制期不可变**：块内容创建后不可写；提交前重新校验源摘要，源被改
  动（`ErrSourceMutated`）或副本摘要不匹配（`ErrDigestMismatch`）都不
  切换，暂存副本被清理。
- **回执幂等**：`ApplyReceipt` 对 `ToGen <= 当前代` 的旧回执直接忽略，
  重放不会回退或重复切换。
- **取消**：`CancelMigration` 在提交前生效，并发的 `Migrate` 观察到取消
  后返回 `ErrCancelled`；提交后取消报 `ErrNotMigrating`，绝不回退。
- **独立账本**：`Ledger` 只消费事件流，重建「引用 ↔ 物理副本」关系，
  `Verify` 对照卷实际内容确认：活引用的当前代副本存在、被固定代的副本
  未释放、无泄漏副本、摘要一致。

## 测试（并发屏障）

`Store.CopyHook` 在复制中途（读完源、写暂存前）提供屏障，测试借此精确
交织迁移与引用变更：

- `TestSwitchPinsHandlesAndReclaims` — 复制中增删引用；切换前后句柄各
  自固定；旧副本等句柄关闭后才回收。
- `TestReceiptReplayIsNoOp` — 切换后及二次迁移后重放旧回执均为无操作。
- `TestCancelDuringCopy` — 屏障处取消，无切换、无残留，可重试。
- `TestDigestMismatchPreventsSwitch` — 注入副本损坏，摘要不匹配不切换。
- `TestDeleteDuringMigration` — 复制中删光引用，迁移取消、块删除。
- `TestConcurrentRefsDuringMigration` — 16 goroutine 并发增删引用与读写
  句柄，迁移完成后账本核验（配合 `-race`）。

## 运行

```sh
go test ./... -race -count=1
```
