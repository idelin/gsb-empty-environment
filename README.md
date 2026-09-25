# rootstore — 双槽元数据原子发布根指针

纯 Go 标准库实现：通过**两个带序号和校验和的元数据槽**原子发布根指针，
读时选择最新的完整槽；配套可注入持久化模拟器，枚举各写入边界的撕裂、
丢失与重排，验证恢复只会得到**旧的或新的完整根**。

## 文件布局（调用方指定目录）

`Open(fs FS)` 在给定目录（`OSFS{Dir}` 或 `SimFS`）中管理两个文件：

| 文件 | 内容 |
|---|---|
| `root.meta` | 2 个元数据槽，偏移 0 与 128，各 128 字节 |
| `pages.dat` | 定长 256 字节页的只增数组 |

槽格式（小端，其余补零）：magic(8) | version(8) | seq(8) | rootID(8) |
pages(8) | CRC32-Castagnoli(4)，校验覆盖前 40 字节。
页格式：magic(8) | id(8) | 子节点数(2) | 数据长(2) | 子页 id(16×8) |
负载(96) | CRC32(4)。

## 提交协议与写入边界

`Commit` 严格按序经过 4 个边界（`store.go`）：

1. **写页**：新树所有页写入 `pages.dat`，随后 `Sync` 页文件；
2. **发布前验证**：从存储重读根页及其**可达闭包**，必须完整可读，
   否则提交失败、槽不发布；
3. **发布**：向**另一个**槽做一次 128 字节 `WriteAt`（原子发布单元），
   seq = 当前 + 1；
4. **持久化**：`Sync` 元数据文件。

任意边界崩溃，恢复只会得到旧根或新根（`TestCrashAtEveryBoundary`
对每个操作前缀 × 两种崩溃模式逐一验证）。

## 序号策略（明确，不猜测）

- **写入方**：seq 为 uint64，每次提交 +1，**永不环绕**；到达
  `math.MaxUint64` 时 `Commit` 返回 `ErrSeqExhausted` 拒绝提交，
  需重建仓库（`TestSeqExhaustedPolicy`）。
- **读取方**：采用 RFC-1982 式模运算比较（差值 < 2^63 时有序），
  因此能正确排序磁盘上出现的环绕序号（如 MaxUint64 与 0），
  旧槽重放会因序号偏旧而**不被选中**（`TestSeqWrapReaderSelectsNewest`、
  `TestOldSlotReplayIgnored`）。
- **损坏槽绝不猜测为有效**：magic/version/长度/校验任一不符即弃用。
- **同序号不同内容**：两个槽各自合法但 seq 相同、内容不同 →
  `ErrSlotConflict`，错误信息同时诊断两个槽；同序号同内容视为
  幂等重复发布，正常打开（`TestSameSeqConflict`）。
- **两槽都坏** → `ErrNoValidSlot`，拒绝打开。
- **新槽半写**（撕裂）→ 校验失败，仍读旧根
  （`TestTornSlotEveryOffset` 枚举全部 0..127 撕裂点）。

## 恢复规则

`Open` 依次：两槽均无效 → 拒绝；一槽有效 → 用之；两槽有效 →
模运算选新（同 seq 见上）。选中槽的根若可达闭包不可读，回退到另一
有效槽；都不可用 → `ErrNoValidRoot`。因此恢复结果只会是
**旧完整根或新完整根**。

## 持久化模拟器与故障注入

`SimFS` 为每个文件区分三层字节：

| 层 | 含义 |
|---|---|
| written | 当前可读（OS 页缓存） |
| synced | 已交给设备（易失磁盘缓存） |
| recoverable | 断电后可恢复（真正落盘） |

默认诚实盘：`Sync` 立即硬化（synced == recoverable）；
`VolatileDiskCache=true` 时 `Sync` 只进易失缓存，`Settle` 才硬化。
注入点 `Hook func(*Op)` 拦截每个 write/sync，可构造：

- **撕裂**：截断 `op.Data`；
- **丢失**：`op.Drop = true`（写丢失 / sync 被吞 = 说谎的盘）；
- **重排**：`op.Defer = true` 入队，`Release`/`ReleaseReversed` 按序
  或逆序落盘；
- **崩溃**：`Crash(ProcessCrash)` 保留全部 written（OS 缓存仍在），
  `Crash(PowerLoss)` 只保留 recoverable。

## 进程崩溃 vs 断电的保证边界

- **进程崩溃**：已返回的写经 OS 缓存仍然可见，等价于"所有 written
  字节存活"。协议在任意点崩溃都安全；甚至元数据 sync 丢失时也能
  读到新根（`TestProcessCrashVsPowerLoss`）。
- **断电**：只有 recoverable 字节存活。保证成立依赖两条**明确假设**：
  1. `Sync` 返回成功后，之前写入的字节断电可恢复（硬件不撒谎）；
  2. `Sync` 是排序屏障：先同步的字节不晚于后写的字节恢复。
- **边界之外**（不保证，但有纵深防御）：盘吞掉 sync（丢 sync 注入）、
  两槽同时介质损坏（拒绝打开）、整库旧镜像重放（槽内信息无法检测，
  需外部单调计数器）。页 sync 被吞导致新根闭包残缺时，`Open` 的
  独立验证会回退到旧完整根（`TestVerifierDetectsCorruptClosure`）。
- 原子写假设：单次 ≤128 字节的槽写是发布单元；**不依赖其原子性**，
  只依赖撕裂必被 CRC 检出（CRC32 碰撞概率可忽略）。

## 独立验证器

`VerifyClosure(pf, root)` 从根出发遍历可达闭包，逐页校验
magic/CRC/内嵌 id，供发布前检查与恢复后独立复核使用。

## 运行

```sh
go test ./...     # 全部测试
go run ./cmd/demo # 运行样例（-dir 指定真实目录）
```

## 测试结果

```
$ go test -v ./...
--- PASS: TestCrashAtEveryBoundary (0.00s)
--- PASS: TestTornSlotEveryOffset (0.00s)
--- PASS: TestLostWriteAtEveryBoundary (0.00s)
--- PASS: TestReorderedWritesBeforeSync (0.00s)
--- PASS: TestSlotPublishIsLastWrite (0.00s)
--- PASS: TestSimLayers (0.00s)
--- PASS: TestCommitAndRecover (0.00s)
--- PASS: TestHalfWrittenSlotKeepsOldRoot (0.00s)
--- PASS: TestLostSlotWriteKeepsOldRoot (0.00s)
--- PASS: TestProcessCrashVsPowerLoss (0.00s)
--- PASS: TestBothSlotsCorruptRefuseOpen (0.00s)
--- PASS: TestSameSeqConflict (0.00s)
--- PASS: TestSeqWrapReaderSelectsNewest (0.00s)
--- PASS: TestSeqExhaustedPolicy (0.00s)
--- PASS: TestOldSlotReplayIgnored (0.00s)
--- PASS: TestLostPageWriteAbortsCommit (0.00s)
--- PASS: TestVerifierDetectsCorruptClosure (0.00s)
--- PASS: TestDeferredReorderedWrites (0.00s)
PASS
ok  	rootstore
```

## 运行样例

```
$ go run ./cmd/demo
== 1. commits on real files in /tmp/rootstore-demo...
  commit A -> seq=1 root=2 pages=3
  commit B -> seq=2 root=5 pages=6
  recovered root: seq=2 root=5 pages=6, closure verify: <nil>
== 2. torn slot publish + power loss (simulator)
  commit A -> seq=1 root=2 pages=3
  commit B (slot write torn) -> seq=2 root=5 pages=6
  recovered root: seq=1 root=2 pages=3, closure verify: <nil>
== 3. both slots corrupt
  commit A -> seq=1 root=2 pages=3
  reopen refused: no valid metadata slot: both slots corrupt, refusing to open
== 4. same sequence, different content
  commit A -> seq=1 root=2 pages=3
  identical duplicate publish:
  recovered root: seq=1 root=2 pages=3, closure verify: <nil>
  forged same-seq slot:
  reopen refused: conflicting slots with identical sequence: slot0=(seq=1 root=2 pages=3) slot1=(seq=1 root=2 pages=4)
== 5. process crash vs power loss (meta sync lost)
  process crash (OS buffers survive):
  recovered root: seq=2 root=5 pages=6, closure verify: <nil>
  power loss (only synced bytes survive):
  recovered root: seq=1 root=2 pages=3, closure verify: <nil>
```
