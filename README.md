# migsim — 块迁移模拟器

把共享块从旧卷搬到新卷的模拟器。迁移期间多个对象仍可增删引用、读写句柄，
通过**按块代际（generation）的地址切换**保证一致性。

## 设计

- **代际（Generation）**：块的一次物理化身，绑定固定 `(volume, addr)` 与已校验摘要。
  状态机：`copying → active → retired → reclaimed`，取消/失败的分代进入 `aborted`。
- **地址切换**：`Migrator` 冻结块内容 → 在目标卷暂存副本 → 校验摘要 →
  原子地把 `cur` 指向新分代。新引用、新读句柄总是落到当前分代。
- **句柄固定副本**：`OpenRead` 固定打开时的分代；切换后旧句柄仍读旧副本。
  旧分代在句柄计数归零时才回收物理副本（`reclaimLocked`）。
- **复制期间内容不可变**：`Write` 在冻结期返回 `ErrFrozen`；
  摘要校验失败（`ErrDigestMismatch`）直接中止，不切换、不泄漏暂存副本。
- **取消**：`Cancel` 仅在切换前生效，丢弃暂存副本并解冻；切换后返回
  `ErrAlreadySwitched`。
- **独立账本（Ledger）**：只记录对象→块引用，回执按序号去重，
  旧回执重放是幂等空操作；物理层旧地址的删除回执重放同样安全
  （地址唯一、`Delete` 幂等）。`Verify` 交叉校验账本引用与物理副本：
  活引用必须解析到存在且摘要一致的活动副本，退役分代句柄归零必须已回收等。

## 测试

`migsim_test.go` 用屏障（`BeforeSwitch` 钩子）把迁移停在"已校验、未切换"点，
注入并发流量：

- `TestSwitchConcurrentRefs`：屏障期间新增引用、重放旧回执、写拒绝、句柄固定旧分代；
  切换后新引用落到新卷、旧副本随句柄释放回收、回执重放与旧物理回执均幂等。
- `TestCancelMigration`：屏障点取消，不切换、不泄漏、解冻可写、重复取消报错。
- `TestDigestFailureBlocksSwitch`：注入副本损坏，摘要失败不得切换。
- `TestReadHandlePinnedToReplica`：句柄跨切换读旧副本，回收等待句柄释放。
- `TestConcurrentStress`：多协程增删引用/重放回执/固定读与多轮迁移（含取消）并发，
  最终由独立账本 `Verify` 确认引用与物理副本关系。

运行：

```sh
go test -race -v ./...
```
