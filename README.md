# versionset-demo

崩溃一致性 Manifest / VersionSet 管理器（LSM 树元数据持久化与原子版本演进），纯 Go 标准库实现。

## 设计

- **VersionEdit**（`versionset/version_edit.go`）：版本演进的原子单元，记录各层新增/删除的
  SSTable 文件及每个文件的 CRC32 校验和、key 范围、大小。序列化为带帧记录：
  `[4B 长度][4B CRC32][JSON payload]`，可检测撕裂写（torn write）。
- **VersionSet**（`versionset/version_set.go`）：
  - `LogAndApply`：先把记录追加到 MANIFEST 并 `fsync`，再切换内存中的 Version —— 崩溃只会
    丢失未提交的 edit，已提交的 edit 必然持久。
  - `Open`（恢复）：读 `CURRENT` 找到 MANIFEST，顺序回放全部合法记录（撕裂的尾部记录被丢弃），
    重建最新 Version；随后删除磁盘上所有未被引用的 `*.sst` 孤儿文件，并将文件号推进到磁盘
    已见最大值之后，保证文件号单调不复用。
  - `VerifyChecksums`：按 manifest 中记录的 CRC32 校验所有存活 SSTable。
- **Compaction**（`versionset/compaction.go`）：`WriteSST` 先落盘输出文件（durable 但未被任何
  版本引用），再用一次 `LogAndApply` 原子完成「删 L0 输入 + 加 L1 输出」。崩溃于两步之间时，
  恢复流程会清理孤儿文件，系统回到 Compaction 前的版本。

## 运行测试

```sh
GO111MODULE=on go test ./versionset/ -v
```

覆盖场景：干净关闭后恢复、**L0→L1 Compaction 写出新文件瞬间崩溃**（验收边界）、已提交
Compaction 的持久性、manifest 撕裂尾部、SSTable 校验和不匹配、文件号跨重启单调。

## 运行样例

```sh
GO111MODULE=on go run ./cmd/demo
```

演示完整验收流程：建库 → 写 L0 → Compaction 写输出文件 → 模拟崩溃 → 重启恢复（丢弃孤儿
SSTable、恢复旧版本）→ 重做 Compaction 并提交 → 再次重启确认新版本持久。
