# agglog — 聚合增量日志

把一串同键增量压成一个 `Package`：包内列出原事件 id 及各自的增量摘要，
接收方按逐事件集合模型幂等应用。

## 核心语义

- `Pack`：同键事件聚合成包；同 id 同增量折叠，同 id 异增量报 `DuplicateError`，
  跨键拒绝（`ErrMixedKeys`）。包内条目按事件 id 排序，与打包次序无关。
- `Store.Apply`：对包内每个条目查逐事件集合（key → event id → delta）：
  - 未见事件 → 应用并计入 `Receipt.Applied`；
  - 已见且增量一致 → 跳过（`Receipt.Skipped`），重放安全；
  - 已见但增量不同 → 记入 `Receipt.Conflicts`，保留原值，其余未见事件照常应用。
- `Model`：逐事件集合参考模型，一次喂一个事件，定义收敛真值。

## 性质核对（`go test ./...`）

- **包重叠**：部分已处理的包只应用未见事件（`TestApplyPartiallyProcessedPackage`），
  重叠 id 异增量报冲突且与模型冲突集逐一相同（`TestConflictingOverlapMatchesModelConflict`）。
- **重放**：同一包重复应用零新增、总量不变（`TestReplayIsIdempotent`）。
- **拆包次序**：重叠包的任意应用次序收敛到同一逐事件集合
  （`TestUnpackOrderDoesNotMatter` 全排列 + `TestStoreConvergesToPerEventModel` 随机化）。
