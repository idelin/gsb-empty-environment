# assertmerge

并行断言归并器：把乱序到达的层级断言事件（子断言可能早于父断言到达）
归并成树，并按测试声明顺序输出完整树。

## 语义

- **有界缓冲**：`NewMerger(capacity, declared)` 的输入缓冲有界；
  `Submit` 在缓冲满时阻塞（背压），`TrySubmit` 返回 `ErrBufferFull`。
- **声明顺序输出**：树始终按 `declared` 顺序从 `Output()` 发出；
  未声明的测试按首次出现顺序排在最后。
- **提前流式输出**：测试 `Finish` 且完全解析（无缺失父节点、无悬空日志、
  唯一根、未失败）时立即输出，`Complete=true`。
- **失败后补日志**：失败的测试会保留到 `Close`，期间到达的补充日志
  仍归并到对应节点。
- **缺口而非丢弃**：`Close`/`Abort` 冲刷时，父节点未到达的子项挂到
  合成 `Gap` 节点（`gap:<missing-id>`，无根时为 `gap:root`）之下，
  树标记 `Complete=false`。
- **重复事件**：按 `(TestID, Kind, ID)` 幂等去重，计入 `Stats.Duplicates`。
- **提前终止**：`Abort` 立即停止接收、丢弃缓冲中未处理事件并冲刷；
  之后 `Submit`/`Finish` 返回 `ErrClosed`。

## 验证

```sh
go test ./...        # 乱序、去重、补日志、缺口、背压、提前终止
go test -race ./...
```
