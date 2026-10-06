# blockingflow

两机零缓冲（阻塞式）流水车间排序库，Go 实现。

## 问题模型

- 每件工件先在机器一加工，再在机器二加工，两机采用**相同顺序**（排列式流水车间）。
- 机器间缓冲容量为零：工件在机器一加工完毕后，若机器二仍忙，工件**占住机器一**直到机器二释放（阻塞）。
- 工件数不超过 10，目标为最小化最大完工时间（makespan）；并列最优按工件 id 序列字典序取最小。
- 支持**已知停机表**：停机期间正在加工的工件暂停但仍占住机器，缓冲仍为零。
- 重规划时**已开工前缀不可重排**，仅对剩余工件按停机表重新优化顺序。

## API

```go
type Job struct{ ID, P1, P2 int }
type Interval struct{ Start, End int }          // 半开区间 [Start, End)
type Schedule struct {
    Seq      []int                              // 最优加工顺序（工件 id）
    Makespan int
    Blocks   []BlockRecord                      // 阻塞时段证据
    Pauses   []PauseRecord                      // 停机暂停证据
}

// 无停机经典情形：全排列求最优，并列按 id 序列字典序。
func Optimize(jobs []Job) (Schedule, error)

// 带停机表的重规划：prefix 为已开工工件 id（顺序固定），优化剩余顺序。
func Replan(jobs []Job, prefix []int, down1, down2 []Interval) (Schedule, error)

// 与 Replan 同解，但每个排列都用离散事件模拟评估，用于核对（阶乘级，仅小规模）。
func Verify(jobs []Job, prefix []int, down1, down2 []Interval) (Schedule, error)

// 评估指定顺序：递推式 / 离散事件模拟。
func Simulate(jobs []Job, seq []int, down1, down2 []Interval) (Schedule, error)
func SimulateEvents(jobs []Job, seq []int, down1, down2 []Interval) (Schedule, error)
```

## 实现与核对

- **递推引擎**（`blockingflow.go`）：`c1 = 完工(上一工件离开机器一时刻, P1)`，
  `d1 = max(c1, 上一工件离开机器二时刻)`，`c2 = 完工(d1, P2)`；
  其中 `完工(s, p)` 在停机表上累计 p 个单位可用机时。阻塞时段为 `(c1, d1]` 的非空部分，
  暂停时段为停机区间与加工占用区间的交集。
- **离散事件模拟**（`des.go`）：事件堆驱动，事件含机器一/二完工、停机开始/结束；
  停机开始暂停加工并作废旧完工事件，停机结束恢复加工。同时刻事件顺序为
  停机结束 < 机器二完工 < 机器一完工 < 停机开始，与递推语义严格对齐。
- **全排列核对**（`blockingflow_test.go`）：随机算例下对**每个排列**断言两引擎的
  makespan、阻塞、暂停证据完全一致；`Optimize`/`Replan` 与 `Verify` 的最优值、
  最优序列一致；10 工件上限规模（362 万排列）求最优并以事件模拟复核。

## 运行测试

```sh
go test ./...
```
