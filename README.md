# diskdeque

纯标准库实现的磁盘双端队列：两端 push/pop、按逻辑下标的随机区间读取。
元素组织成定容块，两端操作只重写边界块（copy-on-write），不发生整体搬移；
事务内对两端的修改通过一份 journal 原子生效，含空到非空的转换。

## 快速开始

```go
d, _ := diskdeque.Open("data", &diskdeque.Options{BlockCapacity: 128, MaxBlocks: 1 << 20})

tx := d.Begin()
tx.PushLeft([]byte("L"))
tx.PushRight([]byte("R"))
_, err := tx.PopRight() // ErrEmpty 或返回元素
tx.Commit()             // 两端修改一起生效；tx.Rollback() 全部丢弃

vals, _ := d.ReadRange(10, 5) // 随机区间读取，只读、不搬移磁盘数据
```

运行样例：

```sh
go run ./cmd/demo
```

## 磁盘布局

所有文件都放在调用方 `Open` 指定的目录内：

| 文件 | 内容 |
| --- | --- |
| `superblock` | 元数据：`txID`、元素总数、按左到右排列的块 ID 列表、块 ID 分配计数器；CRC32 校验 |
| `block_<id>` | 块文件：自描述的元素列表（`len` 前缀），CRC32 校验 |
| `journal` | 提交日志：本事务新块 ID 列表 + 新超级块完整镜像，CRC32 校验 |
| `*.tmp` | 上述文件的原子写入临时文件，恢复时一律清理 |

每个块最多容纳 `BlockCapacity` 个元素。块内容不变时，文件永不重写、编号永不改变；
块内容一旦改变（左入右出等），提交时分配全新编号、写入全新文件，旧文件在新超级块
生效后才被回收，因此任何时刻磁盘上的“旧超级块 + 旧块文件”都保持自洽。

块编号在 `[1, MaxBlocks)` 内环绕分配（分配计数器对 `MaxBlocks` 取模）。分配器依次跳过：

- 哨兵编号 `0`（表示“无块”）；
- 当前超级块中的活跃块编号；
- 本事务内已经分配的编号；
- 磁盘上仍存在对应文件的编号（含尚未回收的旧块）。

所以编号环绕时绝不会覆盖任何活跃块；空间不足时返回 `ErrBlockSpaceExhausted`。

## 提交流程与崩溃恢复

一个事务的提交分五个阶段：

1. 新块/改动块写入 `block_<id>.tmp` 并 `fsync`；
2. 写 `journal`（新块 ID + 新超级块镜像），`fsync` 并原子 rename，再 `fsync` 目录 ——
   **这是提交点**；
3. rename 全部块 tmp 为正式文件，`fsync` 目录；
4. tmp + rename 原子替换 `superblock`，`fsync` 目录；
5. 删除 `journal`，删除不再被引用的旧块文件，`fsync` 目录。

`Open` 时执行恢复，恢复逻辑幂等：

- **`journal` 存在且 CRC 合法**：提交点已经 durable，前滚重放——补齐未完成的 rename、
  按日志中的镜像写超级块、删除 journal。即使超级块尚未更新，事务也完整生效。
- **`journal` 缺失或损坏**：提交点之前崩溃，事务不存在——旧超级块与旧块文件依然自洽，
  清理所有 `*.tmp`。
- 最后 GC：删除任何不被超级块引用的块文件（上一步崩溃遗留的孤儿块）。

因此：提交点前故障 → 两端修改一起回滚（测试 `TestTxAtomicRollbackBothEnds`）；
提交点后任意阶段断电/崩溃 → 重新 `Open` 后事务完整生效
（测试 `TestCrashRecovery` 覆盖提交点后的三个注入点）。

## 故障注入点

通过 `SetFaultHook` 在提交各阶段注入错误，模拟进程级故障：

| 注入点 | 语义 |
| --- | --- |
| `FaultBeforeJournal` | 提交点之前：临时块已写、journal 未落盘。返回错误并清理，事务干净回滚 |
| `FaultAfterJournal` | journal 已 fsync 后“崩溃”：句柄失效，重开目录前滚 |
| `FaultAfterBlockRename` | 块已 rename、超级块未换：重开目录前滚 |
| `FaultAfterSuperblock` | 超级块已换、journal 未删：重开目录幂等前滚 |

## 空队列与最后一个元素

空队列没有任何块文件，`Len()==0` 且 `Head()==Tail()==0`（头尾一致）。
空→非空与普通提交走同一套 journal 协议。弹出最后一个元素后，块列表变空，
旧块文件被回收，头尾再次一致（测试 `TestEmptyToNonEmptyAndLastElement`）。

## 断电保证的边界

实现依赖以下标准语义，这是保证成立的边界：

- **`fsync` 必须真实落盘**。每个阶段都对文件及所在目录做了 `fsync`（rename/删除的
  持久性需要目录 fsync），但若磁盘/固件对 FLUSH 撒谎（如消费级盘的易失写缓存、
  某些网络盘），真断电时仍可能丢数据——这超出软件能保证的范围。
- **rename 原子性**。依赖 POSIX 语义：同一目录下 rename 要么是旧文件要么是新文件，
  不会出现半截文件。不保证在无原子 rename 的文件系统上的行为。
- **进程崩溃**（kill -9、panic、掉电后重启）可恢复；本程序不实现自身被恢复时的
  并发协调，因此约定**同一时刻单写者**打开同一目录。
- 文件内部撕裂由 CRC32 检出（损坏文件报错而不是返回脏数据）；但不防止存储位衰变
  之外的人为篡改，也没有备份/冗余。
- 提交点的语义是“journal 的 fsync 返回”。在此之后写入对故障持久；在此之前，
  调用方应把 `Commit` 返回错误视为未提交。

## 测试

```sh
go test -race -v ./...
```

- `TestBasicPushPop`：多块下的双端混合操作
- `TestReadRange`：随机区间读取（越界、跨块）
- `TestTxAtomicRollbackBothEnds`：同事务左入 + 右出失败，两者一起回滚
- `TestEmptyToNonEmptyAndLastElement`：空→非空、最后元素弹出后头尾一致、重开校验
- `TestCrashRecovery` / `TestCrashBeforeCommitPoint`：三个提交点后注入崩溃并前滚、
  提交点前注入回滚
- `TestWrapAroundIDs`：小编号空间 + 200 个随机事务，块 ID 多次环绕且活跃块不被覆盖
- `TestMiddleBlocksNotRewritten`：两端操作后中间块文件逐字节不变
