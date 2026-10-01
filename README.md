# timetravel

跨组件响应式状态快照与时间旅行控制器（纯 Go 标准库实现）。

## 特性

- **Signal**：响应式状态单元，`Set`/`Update` 即触发版本提交
- **Computed**：派生值，随任何提交或时间旅行自动重算并精准同步
- **不可变快照树**：每次变更后，Controller 记录全局所有 Signal 的不可变快照
- **时间旅行**：`Undo` / `Redo` / `GoTo(version)` 跳转至任意历史版本
- **渲染同步**：`OnChange` 与 `Subscribe` 在时间旅行后自动通知下游 UI 重渲染
- **Batch**：多次修改合并为单个版本；撤销分支上的新修改会截断 Redo 分支

## 快速开始

```go
ctrl := timetravel.NewController()

count := timetravel.NewSignal(ctrl, 0)
name := timetravel.NewSignal(ctrl, "init")
doubled := timetravel.NewComputed(ctrl, func() int { return count.Get() * 2 })

ctrl.OnChange(func(v int) { /* 触发 UI 重渲染 */ })

count.Set(1)      // v1
name.Set("alice") // v2
count.Set(2)      // v3

ctrl.Undo()       // -> v2
ctrl.Undo()       // -> v1：count=1, name="init", doubled=2
ctrl.Redo()       // -> v2：count=1, name="alice", doubled=2，无漂移
ctrl.GoTo(0)      // -> 基线快照
```

## 运行

```sh
go test ./... -v   # 运行全部测试（含验收场景）
go run ./cmd/demo  # 运行演示：3 次修改 -> 撤销 2 步 -> 校验 -> 重做 1 步
```

## 验收场景

`TestAcceptance_TimeTravel`（controller_test.go）完整覆盖验收边界：

1. 连续执行 3 次用户状态修改（v1..v3）
2. `Undo` 回退 2 步至 v1
3. 验证所有 Signal 与关联 Computed 精准恢复到第 1 步数值
4. `Redo` 前进 1 步至 v2，验证状态无漂移

## 说明

- 快照按值拷贝：数值、字符串、纯值结构体天然安全；若 `T` 含切片/映射/指针，
  请以替换代替原地修改，保证历史不可变。
- 在历史已存在后注册的新 Signal，会以初始值回填历史快照，保证跳转到任意版本
  都能还原完整一致的状态树。
