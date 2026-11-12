# 双重检查锁发布模型

惰性初始化组件可能先发布指针再完成敏感状态初始化。为Go测试宿主实现可导入的发布检查包，事件显式区分对象字段写、指针发布、获取读取与释放同步；构造先行发生图，任一读取缺少初始化先行关系即返回具体竞争对和路径。不能把现实时间先后当同步。用屏障事件交换和对象代次重用测试。

## 实现

可导入包路径：`example.org/netsec/q0398/pubcheck`。

- `pubcheck/event.go`：五类显式事件（`KindFieldWrite`、`KindPublish`、
  `KindAcquireRead`、`KindReleaseSync`、`KindAcquireSync`）、对象代次引用
  `Ref{Object, Generation}`、记录器 `Checker` 与通道/屏障两种同步模式。
- `pubcheck/hb.go`：先行发生图。边只来自同一 goroutine 的程序顺序，以及
  释放/获取事件配对产生的 synchronizes-with 边（通道 1:1 FIFO、屏障全配对）。
  `Event.Wall` 完全不参与建图，现实时间先后不构成同步。
- `pubcheck/check.go`：按“字段写 -> 发布 -> 释放 -> 获取 -> 读取”链路逐跳验证，
  任一字段读取缺少初始化先行关系即返回 `Race`（具体竞争对、字段、代次、分阶段
  诊断路径与稳定原因常量）。检查严格限定在同一对象代次内。
- `pubcheck/assert.go`：测试宿主辅助 `AssertNoRace`、`AssertRaceCount`。

## 验证

`go test ./...` 覆盖：屏障事件交换（一次释放多读取者、多释放多获取）、
通道 FIFO 配对、现实时间顺序非同步（含 Wall 时间倒挂）、对象代次重用隔离、
先发布后初始化、无释放发布、先读取后获取、多字段独立报告与图路径断言。
