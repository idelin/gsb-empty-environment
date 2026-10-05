# segvm — 可重开的版本清单管理器（仅 Go 标准库）

一个崩溃可恢复的版本化段（segment）清单管理器。每次发布（publish）是一个事务：
重启后要么看到整套新版本，要么仍是旧版本，不存在半提交状态。

## 故障模型声明（重要）

本题**仅模拟**以下事件，**不宣称模拟真实断电**：

- 各存储操作（创建 / 写入 / 同步 fsync / 重命名 / 目录同步 / 删除）的成功或失败；
- 进程在任意两个操作之间（或一次写入中途）退出并重启。

不建模：真实掉电导致的硬件级撕裂写、设备缓存重排、已 fsync 数据丢失等。
在模拟的"进程退出"中，已成功返回的 `write` 字节被视为持久（因为它们已交给 OS 文件），
只有未完成的写入会表现为日志/段文件的不完整尾部。

## 磁盘格式

```
<dir>/
  manifest.log                 追加式版本日志（帧序列）
  segments/
    <sha256-hex>.seg           不可变内容寻址段文件
    <sha256-hex>.tmp-<nonce>   发布过程中的临时文件（崩溃后由 GC 清理）
```

### 日志帧

```
+-----------+-------------------+----------+
| uint32 BE | JSON payload      | uint32 BE|
| length    | (Record)          | CRC32(payload) |
+-----------+-------------------+----------+
```

Record（JSON）：

```json
{
  "version": 3,                 // 严格从 1 递增，步长 1
  "prev": "<hex sha256>",       // 前一条记录 payload 的 sha256；首条为 "GENESIS"
  "adds":   [{"name":"alpha","digest":"<hex sha256>","size":8}],
  "removes":["beta"]
}
```

- 每条记录携带版本号、新增段（名称→摘要+大小）、移除段名；
- `prev` 哈希链 + 版本序号链保证任何记录被篡改、丢失、重放都会在打开时被发现；
- 段内容摘要即文件名（内容寻址），相同内容天然去重、共享段不会被重复存储。

## 发布协议与退出点枚举

`Publish(adds, removes)` 的存储操作序列（对 2 个新增段 C、D 共 13 步，
`TestCrashMatrix` 对每一步之后的退出点逐一验证）：

| #  | 操作                | 崩溃后的磁盘状态                         | 恢复结果 |
|----|---------------------|------------------------------------------|----------|
| 1  | create C.tmp        | 空临时文件                               | 旧版；GC 清理 tmp |
| 2  | write C.tmp         | 部分/完整临时文件                        | 旧版；GC 清理 tmp |
| 3  | fsync C.tmp         | 完整临时文件                             | 旧版；GC 清理 tmp |
| —  | verify C.tmp        | （重读+重算 sha256，无副作用）           | — |
| 4  | rename C.tmp→C.seg  | C.seg 已就位但无版本引用                 | 旧版；GC 回收孤儿段 |
| 5  | fsync segments/     | 同上                                     | 旧版 |
| 6–10 | 段 D 同样 5 步    | 同上                                     | 旧版 |
| 11 | write 日志记录      | 记录可能完整/不完整                      | 旧版或新版（见下） |
| 12 | **fsync manifest.log（提交点）** | 新版本自此持久           | **新版** |
| 13 | fsync 根目录        | 新版本已提交                             | 新版 |

关键不变式：

- **先写段、后记账**：只有段文件写完、fsync、并重读校验摘要通过后，
  引用它的记录才会被追加。因此任何可见版本引用的段必然完整。
- **提交点唯一**：日志记录 append + fsync 是唯一的提交点。之前崩溃 → 旧版；
  之后崩溃 → 新版完整可见。第 11 步后崩溃时记录可能已完整落盘，
  恢复后看到新版同样合法（要么整套可见，要么仍是旧版）。
- **发布不删文件**：移除的段只在新版本中"取消引用"，文件留给回收器，
  因此旧版读者/快照在发布任意阶段崩溃后仍可完整读取。

## 打开时验证（Open）

1. 逐帧扫描日志：长度前缀、CRC32、JSON 可解析；
2. 版本链：版本号必须从 1 开始逐条 +1；
3. 哈希链：每条 `prev` 必须等于前一条 payload 的 sha256；
4. **日志尾部完整性**：只容忍"不完整末条"（长度前缀残缺、payload/CRC 未写完），
   截断修复后继续；**完整但校验失败**的记录（CRC 错、JSON 错、链断裂）一律报错；
5. 当前版本全部存活段：文件存在、大小一致、内容 sha256 与清单摘要一致。
   （旧版本只做结构校验，因为它们独占引用的段可能已被 GC 合法回收。）

## 回收器（GC）规则

- 保留：当前版本引用的段 ∪ 所有打开中的读快照（`Snapshot`）引用的段；
- 删除：无引用的 `*.seg`、发布中断遗留的 `*.tmp-*`；
- **未知文件**（不符合 `<64hex>.seg` 命名）：识别并报告为 orphan，
  不删除、也绝不当作有效段——所有段读取都经过摘要校验，
  伪造/外来文件无法被误认成有效段；
- 内容寻址保证：被多个名称/版本共享的段（同一摘要）只要还有一个引用就保留。

## CLI

```
segvm init  <dir>
segvm put   <dir> <name>=<file> [...]   # 发布一个新增/替换段的版本
segvm rm    <dir> <name> [...]          # 发布一个移除段的版本
segvm list  <dir>                       # 列出所有版本及其新增/移除段
segvm show  <dir> [version]             # 查看某版本的段集合
segvm cat   <dir> <name>                # 输出当前版本某段内容（校验摘要）
segvm gc    <dir>                       # 回收无引用段，报告孤儿文件
segvm check <dir>                       # 全量校验：链/摘要/日志尾部
```

## 可注入故障适配器

`FaultFS` 包装任意 `FS`，按操作类别区分注入点：
`create / write / sync / rename / dirsync / remove / truncate`。

- `FailKind + FailNth`：第 N 次该类操作返回错误（操作不生效）——模拟 I/O 错误；
- `CrashAfter = k`：第 k 个变更操作成功落盘后 panic(`ErrCrash`)——模拟进程退出；
- `TornAt = k`：第 k 个操作是写时只写一半再 panic——模拟写入中途退出。

## 测试与结果

```
$ go test ./...
ok      segvm   2.510s
```

- `TestCrashMatrix`：枚举发布全部 13 个退出点，逐一崩溃重启，
  核对无半提交、旧快照/读者可读、共享段不误删（13/13 通过）；
- `TestFaultInjectionPerOpKind`：8 个故障注入用例（含提交点 fsync 失败）；
- `TestTornWriteCrash` / `TestTornSegmentWriteCrash`：写入中途退出；
- `TestIncompleteTailToleratedAndTruncated` / `TestCorruptCompleteRecordFails` /
  `TestCorruptMiddleRecordFails` / `TestVersionChainEnforced` /
  `TestHashChainEnforced` / `TestShortGarbageTailTolerated`：打开时验证语义；
- `TestSnapshotPinsProtectFromGC` / `TestContentAddressedSharedSegment` /
  `TestOrphanIdentification` / `TestCorruptSegmentFileDetectedOnOpen` 等。

CLI 演示（`put`/`rm`/`list`/`gc`）：

```
published version 1 (2 segment(s) added)   # alpha, beta
published version 2 (1 segment(s) added)   # 替换 alpha
published version 3 (1 segment(s) removed) # 移除 alpha
OK: version 3, 1 live segment(s), chain/digests/log-tail verified
removed 2 segment file(s)                  # 两个旧 alpha 段被回收，共享的 beta 保留
```
