# cpath — 带时钟误差的关键路径分析工具

分析任务起止、资源等待（排队）与依赖完成事件，在**时钟存在已知区间误差**的前提下，
求总耗时关键路径的可能范围，给出一条可实现的最短解释和一条最长解释，
并区分瓶颈是「计算慢」还是「排队慢」。矛盾的时间约束会被精确定位，绝不输出负耗时。

## 原理

每个事件的真实时刻是变量 `x_i`，时钟误差给出区间 `lo_i <= x_i <= hi_i`。
所有时序知识统一翻译成差分约束 `x_v - x_u <= w`：

| 约束来源 | 差分约束 |
|---|---|
| 事件时钟区间 | `x_i <= hi_i`，`x_i >= lo_i` |
| 任务执行时长 ≥ 0 | `x_start <= x_end` |
| 排队时间 ≥ 0 | `x_request <= x_acquire` |
| 获得资源才能开始 | `x_acquire <= x_start` |
| 依赖完成才能开始 | `x_after <= x_before` |

在此系统上：

- **可行性**：Bellman-Ford。负环即一组互相矛盾的约束，工具打印整个环及约束和来定位冲突。
- **总耗时范围**：`max = d(begin→end)`（最短路），`min = -d(end→begin)`，是精确界而非简单相减。
- **可实现解释**：把对应界作为新约束加入后再求一组可行解，得到每个事件落在其时钟区间内的
  完整时间线（witness），并在其上还原关键链，按 计算 / 排队 / 就绪等待 / 依赖传递 分解。
- **强制负耗时**：若系统可行但约束链强制 `x_end < x_begin`，打印该约束链并提示检查区间或依赖方向。

## 用法

```sh
go build -o cpath .
./cpath examples/pipeline.json              # 可行：输出范围 + 最短/最长解释
./cpath examples/conflict.json              # 矛盾：定位负环，退出码 2
./cpath examples/forced_negative_span.json  # 强制负耗时：定位约束链，退出码 2
cat input.json | ./cpath -                  # 从标准输入读
go test ./...                               # 运行测试
```

退出码：`0` 可行；`2` 检测到矛盾；`1` 输入/内部错误。

## 输入格式

```json
{
  "events": [
    {"name": "build.req", "time": 100.0, "err": 0.5},
    {"name": "build.end", "interval": [104.5, 105.5]}
  ],
  "tasks": [
    {"name": "build", "request": "build.req", "acquire": "build.acq",
     "start": "build.start", "end": "build.end"}
  ],
  "dependencies": [
    {"after": "build.end", "before": "test.req", "label": "build->test"}
  ],
  "span": {"begin": "build.req", "end": "deploy.end"}
}
```

- `events`：`time`+`err`（区间 `[time-err, time+err]`）或直接 `interval: [lo, hi]`。
- `tasks`：`start`/`end` 必填；`request`/`acquire` 可选，用于刻画资源排队。
- `dependencies`：`after` 事件完成后 `before` 事件才能发生。
- `span`：总耗时关键路径的起点与终点事件。

## 输出说明

- **总耗时范围**：`[最短, 最长]`，在全部时钟误差与约束下的精确可能区间。
- **最短/最长解释**：各给一条可实现时间线（所有事件取值均在自身时钟区间内），
  关键链按 计算 / 排队 / 就绪等待 / 依赖传递 分解，并据此判定「计算慢」或「排队慢」。
- **矛盾定位**：打印构成负环的每条约束及其来源（事件区间 / 任务时长 / 依赖），
  以及约束和，指明需要修正哪一处。
