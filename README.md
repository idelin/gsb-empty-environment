# receiptlib

事件溯源的回执库：按工作项（item）接收执行代（epoch）内单调修订（revision）的
状态回执，支持显式 supersede 换代、乱序到达、持久化恢复。

## 状态机（汇合语义）

回执状态为 `accepted` / `running` / `succeeded` / `failed`，后两者为终态。
每个执行代内的 fold 只依赖回执集合、与到达顺序无关：

- 同 (epoch, revision) 内容不同的回执 → `ErrConflict`，拒绝入库；
  完全相同的重复回执是幂等 no-op。
- 终态回执压制任何非终态头（即使修订更低，晚到的终态仍生效）。
- 多个终态之间取最高修订；非终态之间取最高修订。
- 非终态绝不能顶掉终态头：终态之后的高修订 `running`/`accepted` 只归档。
- 所有入库回执（含被归档的）都保留在 Evidence 中，证据不丢失。

## 换代（supersede）

- 只有显式 `SupersedeDecision{From, To}` 提交后，新代才能推进业务头；
  决议必须满足 `From == 当前代 && To > 当前代`，否则 `ErrStaleDecision`。
- 决议前到达的新代回执被缓冲（Buffered），决议提交时按汇合规则回放。
- 旧代晚到的回执只写入旧代历史，不能覆盖新代业务头；若旧代达到
  `succeeded`（无论发生在决议前还是决议后），记录一条待补偿
  （PendingCompensations），每代最多一条。
- 任意时刻每项只有唯一当前代（CurrentEpoch）。

## 持久化

`Open(logPath, checkpointPath)` 打开持久化存储：所有变更先追加到 JSONL
事件日志（写后 fsync），状态由事件重放得出。`Checkpoint()` 原子写入快照，
恢复时加载快照并只重放其后的日志尾部；不带检查点路径打开即为全量重放，
两者结果一致（见 `recover_test.go`）。

## 测试

- `receipt_test.go`：冲突、低修订归档、终态粘性、换代门控、补偿、过期决议。
- `perm_test.go`：单代 6 回执 720 全排列；含换代决议的 7 事件 5040 全排列，
  核对最终状态、唯一当前代、补偿记录与证据完整性。
- `recover_test.go`：随机乱序事件流下检查点恢复 == 全量重放 == 内存参照。
