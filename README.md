# snapshot-exporter

Go 快照分页导出器：从固定快照分页读取数据，落盘为页文件 + 清单，支持安全的断点续导。

## 设计

- **固定快照**：每次导出运行开始时取 `Source.SnapshotID()`，整轮读取都钉在该快照上；
  源在导出期间变更会让读取返回 `ErrSnapshotExpired`，绝不混用两个快照的数据。
- **清单（manifest.json）**：记录 `snapshot_id`、`page_size`、续导游标 `next_token`、`done`
  以及每页的 `index / file / start_key / end_key / count / sha256`（页范围 + 摘要）。
- **断点续导**：恢复时逐页校验页文件的 SHA-256，只复用与清单匹配的页；
  快照 id 或页大小不匹配 → 旧清单整体作废，重新全量导出。
- **页文件损坏**：校验失败的页及其后续页全部截断，从最后一个完好页的 `end_key`
  重新向源读取——不信任磁盘上的偏移或内容。
- **原子写入**：页文件和清单均先写 `.tmp` 再 rename，崩溃不会留下半截文件。

## 核对（测试）

`go test ./...` 覆盖：

| 场景 | 测试 |
| --- | --- |
| 单次全量导出，页范围无缝平铺 | `TestFullExportCoversSnapshot` |
| 中断后续导，只补读缺失页 | `TestResumeAfterInterruption` |
| 源更新 → 旧清单页一律不复用 | `TestSourceUpdateInvalidatesManifest` |
| 页文件被篡改 → 重导该页 | `TestCorruptPageIsReExported` |
| 页文件丢失 → 重导该页 | `TestDeletedPageIsReExported` |
| 重复恢复幂等，零额外读取 | `TestRepeatedResumeIsIdempotent` |
| 中断+源更新+损坏混合后收敛到完整集合 | `TestChaosRecovery` |

每个测试最终都用 `Exporter.Collect` 重新校验全部页摘要并合并记录，
与源当前快照逐条比对，确认最终集合完整且一致。
