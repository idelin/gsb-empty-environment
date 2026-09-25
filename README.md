# slotstore — 定长槽位记录存储（代际句柄 + 崩溃恢复）

纯 Go 标准库实现。记录存放在**调用方指定目录**下的两个文件中：

- `slots.dat`：N 个定长槽（默认 512 字节 × 1024 个）
- `header.bin`：库头（几何参数 + 自由链头），写入走 `tmp + rename` 原子替换

## 槽布局

```
[0:4]   magic "SLO1"
[4:8]   generation uint32      —— 槽每承载一个新对象 +1
[8]     state: 0=free / 1=used
[12:16] payload 长度（used 槽）
[16:]   used: payload / free: nextFree 索引（自由链节点嵌在槽内）
尾部 4B CRC32                  —— 槽尾校验，覆盖前面全部字节
```

## 核心机制

- **代际句柄**：`Handle{Slot, Gen}`。删除后槽位复用时 `Gen+1`，旧句柄
  `Get/Delete` 返回 `ErrStaleHandle`，无法访问新对象。
- **自由链 × 数据槽双向校验**：恢复时先扫描全部数据槽（以槽为权威），
  再沿持久化自由链校验：链节点必须指向空闲槽（否则 `ErrFreeListCorrupt`）、
  链不得成环（否则 `ErrFreeListCycle`）；随后用扫描结果重建自由链，
  保证每个空闲槽都在链上（崩溃丢失的链节点被找回，杜绝槽泄漏）。
- **半写槽识别**：槽尾 CRC 校验失败的槽一律视为半写/损坏槽，恢复时回收为
  空闲并抬升代际；`Get` 命中校验失败的槽返回 `ErrCorruptSlot`，
  绝不会被当作有效记录读出。
- **故障注入点**：`Options.Hook func(op FaultOp, buf []byte) ([]byte, error)`，
  在 `slot.write / slot.sync / header.write / header.sync` 四个点触发；
  返回截断的 buf 模拟撕裂写，返回 error 模拟该点崩溃。

## 写序与崩溃恢复

- `Put`：先原子更新头文件（弹出链头），再写数据槽并 fsync。
  崩溃发生在两步之间 → 槽仍是空闲，恢复扫描把它找回自由链，不泄漏。
- `Delete`：先写空闲槽并 fsync，再原子更新头文件（压入链头）。
  崩溃发生在两步之间 → 头文件里的链没包含它，恢复扫描同样找回。
- 恢复 = 全量扫描 + 校验 + 重建，因此**持久化自由链只是提示，数据槽才是权威**。

## 断电保证的边界

**保证**（前提是文件系统遵守 fsync 语义）：

- 已返回成功的 `Put/Delete`：槽写 + fsync 完成后断电，恢复后要么看到新状态、
  要么看到旧状态，绝不返回损坏记录（撕裂写被 CRC 识别并回收）。
- 头文件经 `tmp+rename` 原子替换，断电后头文件要么是旧版要么是新版，
  两者都能通过校验；即使头文件滞后，恢复也会以数据槽为准重建。
- 旧句柄在任何崩溃时序下都不会读到新对象（代际校验在读取路径上）。

**不保证**：

- 未返回成功的最后一次操作可能丢失（无 WAL、无多槽事务）。
- 单槽写在断电瞬间可能被撕裂——我们**检测并回收**，不保证该次写入存活。
- 崩溃在 `Put` 两步之间时，该槽可能已被头文件弹出但尚未写入：
  恢复后它回到自由链，等同于这次 `Put` 从未发生。
- 存储设备/文件系统虚报 fsync（硬件缓存未刷）时，以上保证随之失效。
- 代际号为 32 位，同一槽复用 2^32 次后回绕（实际场景可忽略）。

## 运行

```sh
go test -v ./...          # 全部测试（含故障注入用例）
go run ./cmd/demo /tmp/slotdemo   # 运行样例
```

## 测试结果（go1.24.2）

```
=== RUN   TestPutGetDelete                       --- PASS
=== RUN   TestGenerationBumpInvalidatesOldHandle --- PASS
=== RUN   TestPersistenceAcrossReopen            --- PASS
=== RUN   TestBadTailChecksumNotValidRecord      --- PASS
=== RUN   TestFreeListCycleDetected              --- PASS
=== RUN   TestFreeListPointsToUsedSlot           --- PASS
=== RUN   TestCrashTornSlotWriteRecovered        --- PASS
=== RUN   TestCrashBetweenHeaderAndSlotWriteNoLeak --- PASS
=== RUN   TestCrashDuringDeleteNoLeak            --- PASS
=== RUN   TestStoreFullAndReuse                  --- PASS
=== RUN   TestTooLarge                           --- PASS
PASS
ok  	slotstore	1.852s
```

## 运行样例输出

```
== 1. 打开/创建存储 ==
目录 /private/tmp/slotdemo1，空闲槽 4

== 2. 写入两条记录 ==
h1={slot:0 gen:1} h2={slot:1 gen:1}

== 3. 删除 h1，同槽写入新对象：代际增加，旧句柄失效 ==
新对象 h3={slot:0 gen:2}（与 h1 同槽，代际 +1）
旧句柄 h1 访问 -> slotstore: stale handle (slot deleted or reused by a newer generation)
新句柄 h3 访问 -> "order#1003"

== 4. 注入故障：写槽时撕裂写（模拟断电半写槽）后崩溃 ==
Put 中途崩溃 -> injected crash (power loss)（进程不 Close，模拟崩溃）

== 5. 崩溃后恢复 ==
恢复完成，空闲槽 2（半写槽已回收）
崩溃前已提交的 h2 仍可读 -> "order#1002"
旧句柄 h1 依然失效 -> slotstore: stale handle (slot deleted or reused by a newer generation)
```
