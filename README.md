# segmentfsm — 段生命周期状态机（构建 → 校验 → 发布 → 回收）

仅用 Go 标准库实现。一个目录即一个存储实例，目录由调用方指定：

```
manifest.json   已提交 manifest（唯一事实来源）
journal.json    在途操作日志；不存在 = 空闲
seg-NNNNNN.dat  段文件，每代一个
*.tmp           暂存文件，永远可以安全删除
```

## 状态机

```
        Ingest(records)
             │
             ▼
   ┌──────────────────┐   写 journal(state=building)
   │ BUILDING 构建     │── 合并旧段记录 + 新记录 → seg.tmp → fsync → rename → fsync(dir)
   └────────┬─────────┘
            ▼  journal(state=built, 记录 checksum)
   ┌──────────────────┐
   │ BUILT 校验        │── 重新读盘，校验 SHA-256（捕捉构建到发布之间的位腐）
   └────────┬─────────┘
            ▼  journal(state=validated)
   ┌──────────────────┐
   │ VALIDATED 发布    │── manifest.tmp → fsync → rename → fsync(dir)  ← 提交点
   └────────┬─────────┘
            ▼  journal(state=reclaiming)
   ┌──────────────────┐
   │ RECLAIMING 回收   │── 删除旧 manifest 引用、新 manifest 不再引用的段
   └────────┬─────────┘
            ▼  删除 journal
          空闲
```

关键不变式：**发布前新段是孤儿**（manifest 不引用，崩溃后由 GC 回收）；
**发布后旧段才可回收**（新 manifest 已 fsync 落盘，旧段不再被引用）。

## 崩溃恢复

- 每次状态迁移**先**写 journal（tmp + rename + dir fsync），**后**产生下一步的效果。
  因此崩溃后 journal 永远指向"下一个待执行步骤"。
- `Open()` 加载 manifest + journal，校验完整性，然后把中断的操作**驱动到完成**。
- 每个步骤幂等：先检查效果是否已在盘上（如 manifest 已是目标代、段文件 checksum
  已匹配），是则跳过。崩溃在 "rename 之后、journal 之前" 这种窗口也能安全重做。
- 恢复完成后回收孤儿：所有 `*.tmp` + 未被 manifest 引用的 `seg-*.dat`。

## 断电保证边界

- 单个文件的耐久性依赖 **fsync(文件) → rename → fsync(目录)** 的顺序
  （`TestFsyncOrdering` 用事件探针断言该顺序）。rename 是原子的，所以断电后
  manifest 要么旧要么新，绝不混合；torn tmp 文件恢复时直接丢弃。
- **确认点**：`Ingest` 返回 nil 时，manifest 与其引用的所有段均已完整 fsync，
  数据对调用方确认。在此之前断电，恢复后回滚到旧 manifest——不丢已确认数据。
- **边界之外**：文件系统/硬件若谎报 fsync（写缓存未落盘即返回成功），任何
  用户态程序都无法保证；本实现做到的是 POSIX 语义内的正确顺序。
- manifest/journal 均带 SHA-256 自封校验，torn write 会被报为损坏而非误解析。

## 损坏即报错，绝不静默回退

- manifest 引用的段缺失或 checksum 不符 → `Open` 返回 `ErrCorruption`。
- manifest 丢失但目录里有段文件/暂存文件、且没有 journal 能解释（首代发布前
  崩溃除外）→ `ErrCorruption`，**不会**回退成空库——那会静默丢弃已确认数据。
- manifest/journal 自封校验失败 → `ErrCorruption`。

## 故障注入

`WithInjector(func(point FaultPoint) error)`：返回 error 模拟可重试的瞬时故障；
`panic(SimulatedCrash{...})` 模拟进程崩溃（绕过所有 defer，与真实崩溃一致）。
注入点见 `fault.go`：构建写 tmp / 构建 rename / 校验读 / 发布写 tmp /
发布 rename / 回收删除 / journal 写入。

## 运行样例

```
$ go run ./cmd/demo -dir /tmp/segdemo -crash-at publish.rename

== data dir: /tmp/segdemo

[gen 1 published] generation=1 segments=[{seg-000001.dat ad4c2dd8… 13}] records=[apple banana]
           files on disk: [manifest.json seg-000001.dat]

*** process crashed at "publish.rename" ***
    files left behind: [journal.json manifest.json manifest.json.tmp seg-000001.dat seg-000002.dat]

[after recovery] generation=2 segments=[{seg-000002.dat 6883ada0… 20}] records=[apple banana cherry]
           files on disk: [manifest.json seg-000002.dat]

planted orphans:    [manifest.json seg-000002.dat seg-000003.dat.tmp seg-000999.dat]
[orphans reclaimed] generation=2 segments=[{seg-000002.dat 6883ada0… 20}] records=[apple banana cherry]
           files on disk: [manifest.json seg-000002.dat]

missing referenced segment -> Open reports: segmentfsm: corruption detected:
    manifest generation 1 references missing segment "seg-000001.dat"
(no silent fallback to an empty/older state: acknowledged data is never dropped quietly)
```

## 测试结果

```
$ go test -v ./...
--- PASS: TestHappyPath                        两代发布，旧段被回收
--- PASS: TestCrashRecoveryAtEveryPoint        7 个注入点逐一硬崩溃，恢复后状态一致
--- PASS: TestTransientFaultRetry              7 个注入点逐一瞬时故障，幂等重试成功
--- PASS: TestCrashLoopUntilSuccess            连续 5 次崩溃后仍收敛
--- PASS: TestMissingReferencedSegmentIsCorruption
--- PASS: TestBitRotSegmentIsCorruption
--- PASS: TestMissingManifestNeverFallsBack
--- PASS: TestTornManifestIsCorruption
--- PASS: TestOrphanReclamation
--- PASS: TestFsyncOrdering                    fsync→rename→fsync(dir) 顺序断言
--- PASS: TestIngestRejectsNewlines
PASS
ok  	example.com/segmentfsm	3.540s
```
