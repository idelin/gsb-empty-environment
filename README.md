# slotstore — 定长槽记录存储（代际句柄 + 自由链双向校验）

纯标准库 Go 实现。记录放在**调用方指定目录**下的两个文件里：

- `superblock`：魔数 / 版本 / 槽数 / 槽容量 / 自由链头 + CRC32C
- `slots.dat`：`slotCount` 个定长槽，槽 `i` 位于偏移 `i * stride`

槽布局：`magic | state | generation | freeNext | freePrev | payloadLen | payload | crc32c`
（`state`: 0=free, 1=occupied, 2=writing；CRC 覆盖除自身外的整个槽）

## 核心语义

- **代际句柄**：`Handle{Slot, Gen}`。`Delete` 立即把槽代际 +1；同槽新对象使用
  递增后的代际，旧句柄 `Get`/`Delete` 一律返回 `ErrStaleHandle`。
- **双向校验**：超块的 `freeHead` 与槽内 `freeNext/freePrev` 前后链互相印证，
  并与槽自身 state 交叉核对。
- **自由链成环 = 硬错误**：正常崩溃窗口只会在链头拼接，绝不可能成环，
  因此恢复时检测到环（或越界链接）直接返回 `*CorruptError`，不做猜测性修复。
- **槽尾校验坏 ≠ 有效记录**：CRC/magic 校验失败或 `state=writing` 的槽
  在恢复时被清洗为空闲槽，绝不作为记录返回。

## 写入顺序与故障注入

`Put`/`Delete` 的持久化顺序：**槽写入 → fsync(data) → 超块提交 → fsync(meta)
→ 反向链修正写入**。注入点（`FaultInjector`）：

```
FaultBeforeSlotWrite   槽写入前
FaultAfterSlotWrite    槽已写未 fsync
FaultAfterSlotSync     槽已持久、超块未提交   <- 最关键的崩溃窗口
FaultAfterSuperSync    操作已完整提交
```

注入器返回错误即模拟进程被杀：存储立即进入 crashed 状态、不再做任何清理，
测试随后重新 `Open` 同一目录验证恢复结果。

## 进程崩溃恢复

恢复协议（`recover.go`）：

1. 校验超块（magic/version/CRC）。超块损坏是**致命**的（几何信息不可信）。
2. 逐个解码槽：CRC/magic 坏或 `state=writing` → 半写槽，清洗为空闲。
3. 沿 `freeHead` 走查自由链：成环 / 越界 → `*CorruptError`。
4. 以**槽状态为准**重建自由链：链上有但槽已是 occupied（槽写完、超块未提交
   的崩溃窗口）→ 摘除；槽是 free 但不在链上（Delete 的对应窗口）→ 挂回；
   backlink 不一致 → 重建修正。所有修复记入 `RecoveryReport`。

## 断电保证的边界

**保证**（假设 OS/硬件尊重 fsync）：

- 已成功返回的 `Put`/`Delete` 在进程崩溃和断电后仍然可见。
- 单次操作是原子的：崩溃后记录要么完整可见、要么不可见，绝不出现半条记录
  （槽内 CRC 兜底，撕裂写会被识别并清洗）。
- 自由链与槽状态的不一致总能被恢复收敛；结构性破坏（成环）必然报错而非静默。

**不保证**（明确边界）：

- 硬件/文件系统对 fsync 撒谎（易失回写缓存）时，任何基于 fsync 的系统都无法
  保证持久性，本库不例外。
- 超块是单副本：超块本身被撕裂（CRC 失败）视为致命损坏，需人工介入；
  如需更高保障可扩展为双副本 + 代次选择。
- 无多记录事务：跨多次 `Put`/`Delete` 的一致性由调用方负责。
- 半写槽清洗时若 CRC 已坏，代际字段只能尽力读取（raw+1）；物理上摧毁了
  代际字节的极端撕裂写无法严格保证旧句柄失效——这是单副本元数据的固有限界。
- CRC32C 是损坏检测，不是安全机制，不防御恶意篡改。

## 运行

```sh
go test ./...          # 全部测试
go run ./cmd/demo /tmp/slotstore-demo   # 端到端样例
```

### 样例输出

```
== open new store in /tmp/slotstore-demo ==
put: {Slot:0 Gen:0} {Slot:1 Gen:0} {Slot:2 Gen:0}
== delete bravo, reuse its slot ==
old handle {Slot:1 Gen:0} -> new handle {Slot:1 Gen:1} (same slot, gen+1)
stale handle h2 rejected: slotstore: stale handle (slot reused or deleted)
new handle h4 reads: "delta"
== restart: reopen and recover ==
clean shutdown: no repairs needed
live records after restart:
  slot=0 gen=0 payload="alpha"
  slot=1 gen=1 payload="delta"
  slot=2 gen=0 payload="charlie"
stale handle h2 still rejected after restart: slotstore: stale handle (slot reused or deleted)
== done ==
```

### 测试结果（go test -v）

```
--- PASS: TestPutGetRoundtrip (0.01s)
--- PASS: TestDeleteReuseSlotBumpsGeneration (0.01s)
--- PASS: TestStoreFull (0.01s)
--- PASS: TestFreeChainCycleIsError (0.00s)
--- PASS: TestBacklinkMismatchRepaired (0.01s)
--- PASS: TestBadTrailerChecksumNotAValidRecord (0.00s)
--- PASS: TestHalfWrittenSlotScrubbed (0.01s)
--- PASS: TestCrashAfterSlotSyncBeforeSuperblock (0.02s)
--- PASS: TestCrashBeforeSlotWriteLeavesNoTrace (0.00s)
--- PASS: TestDeleteCrashBetweenSlotAndSuperblock (0.00s)
--- PASS: TestFaultPointsFireInOrder (0.00s)
PASS
ok  	slotstore	4.062s
```
