# timetravel

跨组件响应式状态快照与时间旅行控制器（Go，仅标准库）。

## 特性

- `Signal[T]`：响应式状态单元，`Set` 即提交一次全局不可变快照
- `Computed[T]`：派生值，按状态版本惰性重算，Undo/Redo 后自动失效同步
- `Controller`：管理所有 Signal 的**不可变快照树**（支持分支历史）
- `Undo(n)` / `Redo(n)` / `GoTo(id)`：跳转至任意历史快照，恢复全部 Signal
- `Subscribe(fn)`：每次状态迁移后通知下游（UI 自动重渲染同步）
- `Batch(fn)`：多次写合并为一次快照提交

## 运行

```sh
go test ./... -v   # 验收测试
go run ./cmd/demo  # 运行样例
```

## 验收用例

`TestAcceptance_UndoRedoNoDrift`：连续 3 次用户修改 → `Undo(2)` →
所有 Signal 与 Computed 精确恢复到第 1 步数值 → `Redo(1)` → 状态无漂移。
