# manifestmgr — 可重开的版本清单管理器(仅 Go 标准库)

## 功能

- 版本化清单:每版记录**版本号、新增段、移除段、各段内容摘要(SHA-256)**,并带 `prev_digest` 链。
- **原子发布**:一个事务发布后要么整套可见,要么仍是旧版;提交点为日志 fsync。
- **可注入故障的文件适配器**(`FaultFS`):区分 write / append / sync / rename / dirsync / remove
  六类操作,支持按操作类别与序号注入失败、模拟进程崩溃(`CrashAfter`)与日志撕裂追加(torn append)。
  仅模拟操作成败与进程重启,不模拟真实断电。
- **先校验后引用**:段写完、fsync、读回校验摘要后,才允许提交引用它的新版本。
- **打开即恢复**:校验递增版本链、段存在性、摘要、日志尾部完整性;
  只容忍不完整末条(自动截断),完整坏条报错。
- **安全回收器**:不删除活动读快照或当前版本引用的文件;未知孤儿文件可识别、不删除、不误认为有效段。

## 使用

```sh
go build ./cmd/manifest
manifest init   <dir>
manifest put    <dir> <file>...     # 新增段并提交新版本
manifest rm     <dir> <seg-id>...   # 移除段并提交新版本
manifest show   <dir>               # 当前版本
manifest log    <dir>               # 全部版本历史
manifest cat    <dir> <seg-id>      # 输出段内容(经快照读取并校验摘要)
manifest verify <dir>               # 重开并完整校验
manifest gc     <dir>               # 回收未引用段
```

## 库 API 摘要

```go
m, _ := manifestmgr.Create(fs)          // 或 manifestmgr.Open(fs) 重开
v, _ := m.Publish(adds, removes)        // 原子提交新版本
snap := m.AcquireSnapshot()             // 固定读快照(GC 保护)
data, _ := snap.Read(segID); snap.Close()
rep, _ := m.GC()                        // rep.Deleted / Kept / Orphans
```

`fs` 为 `manifestmgr.Adapter`:`OSFS`(真实磁盘)、`MemFS`(内存,便于模拟重启)、
`FaultFS`(故障注入包装)。

## 格式与恢复协议

见 [FORMAT.md](FORMAT.md)。

## 测试结果

```
$ go test ./...
ok      manifestmgr
?       manifestmgr/cmd/manifest        [no test files]
```

关键用例:

- `TestCrashAtEveryPublishStep`:枚举发布协议全部 8 个退出点(7 个可变操作 + 无崩溃基线),
  逐一崩溃重启,核对无半提交、旧版完好、共享段存活;
- `TestTornTailTolerated` / `TestTornCommitAppend`:不完整末条(含提交帧撕裂追加)被容忍并截断;
- `TestCorruptCompleteRecordRejected` / `TestCorruptFirstRecordRejected` / `TestAbsurdLengthRejected`:完整坏条报错;
- `TestVersionChainGapRejected` / `TestInconsistentSegmentTableRejected`:版本链与段表一致性;
- `TestSnapshotPinsProtectFromGC` / `TestSharedSegmentNeverDeleted`:快照与共享段保护;
- `TestOrphanIdentification`:未知孤儿文件可识别、不删除、不误认为有效段;
- `TestSegmentCorruptionDetected` / `TestSegmentMissingDetected`:段摘要与存在性校验。
