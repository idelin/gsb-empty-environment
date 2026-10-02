# versionset — 崩溃一致性 Manifest 版本集管理器

LSM 树元数据持久化与原子版本演进器（纯 Go 标准库实现）。

## 设计

- **Version**：某一时刻各层 SSTable 文件列表（含文件号、大小、CRC32 校验和、
  key 范围）的不可变快照。
- **VersionEdit**：一次版本演进的原子单元（删文件 + 加文件 + nextFileNum），
  采用 tag + varint 编码。
- **MANIFEST**：VersionEdit 的追加日志。每条记录为
  `CRC32(payload) 4B | 长度 4B | payload`。
- **CURRENT**：指向当前 MANIFEST，通过 `tmp + rename + fsync(dir)` 原子切换。

## 崩溃一致性保证

- **提交点**：`LogAndApply` 先追加记录并 `fsync` MANIFEST，再切换内存版本。
  崩溃只会丢失未 fsync 的尾部。
- **撕裂写恢复**：重放时遇到 CRC 不匹配或残缺记录，截断到最后的完好偏移，
  已提交版本不受影响。
- **孤儿文件清理**：恢复时扫描目录，删除未被当前版本引用的 `*.sst`
  —— 即"Compaction 写出新文件后、提交前崩溃"留下的垃圾。
- **存活文件校验**：恢复时重算每个存活 SSTable 的 CRC32，损坏即报错。
- **顺序保证**：SSTable 内容先 fsync 落盘，元数据才允许提交；
  目录变更后 fsync 目录本身。

## 文件

| 文件 | 说明 |
|---|---|
| `manifest.go` | VersionEdit 编解码、MANIFEST 记录读写（容忍撕裂尾部） |
| `versionset.go` | Version / VersionSet、恢复、LogAndApply、孤儿清理 |
| `versionset_test.go` | go test 测试 |
| `cmd/demo/main.go` | 运行样例 |

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例：模拟 Compaction 中途崩溃并恢复
```

## 测试覆盖

- `TestEditRoundTrip`：VersionEdit 编解码往返一致
- `TestReopenRestoresVersion`：提交后重开，版本完整恢复
- `TestCrashDuringCompaction`：**验收边界** —— L0→L1 Compaction 写出新文件
  瞬间崩溃，重启后恢复到 Compaction 前版本，孤儿 SSTable 被清除，文件号回收复用
- `TestCommittedCompactionSurvivesRestart`：Compaction 提交后崩溃，新版本完整恢复
- `TestCrashBetweenCommitAndObsoleteDelete`：提交后、旧文件删除前崩溃，
  恢复时清理残留
- `TestTornManifestTail`：MANIFEST 撕裂写截断恢复，且可继续提交
- `TestLiveFileCorruptionDetected`：存活文件校验和不匹配时恢复报错

## 测试结果

```
=== RUN   TestEditRoundTrip
--- PASS: TestEditRoundTrip (0.00s)
=== RUN   TestReopenRestoresVersion
--- PASS: TestReopenRestoresVersion (0.11s)
=== RUN   TestCrashDuringCompaction
--- PASS: TestCrashDuringCompaction (0.13s)
=== RUN   TestCommittedCompactionSurvivesRestart
--- PASS: TestCommittedCompactionSurvivesRestart (0.10s)
=== RUN   TestCrashBetweenCommitAndObsoleteDelete
--- PASS: TestCrashBetweenCommitAndObsoleteDelete (0.07s)
=== RUN   TestTornManifestTail
--- PASS: TestTornManifestTail (0.09s)
=== RUN   TestLiveFileCorruptionDetected
--- PASS: TestLiveFileCorruptionDetected (0.03s)
PASS
ok  	versionset
```
