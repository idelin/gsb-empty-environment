# assertmerge — 并行断言归并器

把并行测试中**乱序到达**的层级断言事件归并成每棵测试树，并**按测试声明顺序**输出。
子断言可以早于父断言到达；事件先进入**有界缓冲**，直到所属测试完成（`KindDone`/`KindAbort`）
或归并器被 `Close` 时才链接成树。结束时父节点缺失的子项挂到合成的**缺口节点**（`Gap: true`）上，
绝不丢弃。

## 模型

- `Event{TestID, NodeID, ParentID, Kind, ...}`：事件流的最小单位。
  - `KindAssert`：层级断言节点，`ParentID == ""` 表示根。
  - `KindLog`：日志补充，`ParentID` 指向被补充的断言节点（失败后补日志即由此挂载）。
  - `KindDone` / `KindAbort`：测试完成 / 提前终止（幂等）。
- `New(order []string, maxBuffered int)`：`order` 为测试声明顺序；`maxBuffered <= 0` 表示无界。
- `Submit(ev)`：线程安全。缓冲满返回 `ErrBufferFull`（事件不落地，调用方可稍后重试）；
  重复事件（同 TestID+Kind+NodeID）去重并计入 `Stats.Duplicates`，首次出现生效。
- `Ready()`：取出自上次调用以来已可输出的树（严格按声明顺序；前面测试未完成则阻塞后续输出）。
- `Abort(id)` / `Close()`：单测试或全局提前终止，缺失父节点以缺口占位后照常按序输出。

## 输出保证

- 树严格按声明顺序逐棵输出；树内兄弟节点按 ID 排序，输出确定。
- 缺口节点共享同一缺失 ID：多个子项引用同一缺失父节点时只生成一个缺口。
- 目标缺失的日志同样挂到缺口节点上。
- 树输出后释放其占用的缓冲额度（`Pending()` 可观察）。

## 验证

```sh
go test ./... -race -v
```

覆盖：乱序组装与声明序输出、重复事件去重、失败后补日志、`Close`/`Abort` 提前终止产生缺口、
有界缓冲满与释放、未声明测试、并发提交。
