# Zero-Bubble Pipeline Scheduler

可导入的 Go 包，用于构建、输出和验证 Zero-Bubble（ZB-H1/H2 风格）流水线并行排期，并附带标准 1F1B 基准。

核心实现只依赖 Go 标准库。

## 能力

- 将每个微批的反向传播拆成 `B(m,s)` 与 `W(m,s)`：
  - `F(m,s)`：前向。
  - `B(m,s)`：激活梯度，依赖 `F(m,s)` 与 `B(m,s+1)`，完成后立即允许上游 `B(m,s-1)` 推进。
  - `W(m,s)`：权重梯度，只依赖本地 `F(m,s)`、`B(m,s)`，用于填充阶段空档。
- 支持阶段级非均匀耗时 `TF/TB/TW`，以及阶段级 `MemF/MemW`。
- 支持全局单值或逐阶段的 `memLimit[s]` 硬上限。
- 完整生成跨阶段同步 `Send/Recv` 消息对、FIFO 顺序和双向通道校验。
- 自动审计依赖完整性、阶段资源互斥、两阶段显存生命周期、通信配对和最终 DAG 无环。
- 输出每阶段精确起止时间、每阶段峰值显存、整体峰值、气泡时间与气泡率。
- 同时构造标准 1F1B 对照排期；`Compare` 给出气泡率与 makespan 改善。

## 快速开始

```go
package main

import (
	"fmt"
	"log"

	zerobubble "prescreen.local/ps-53e209913c"
)

func main() {
	cfg := zerobubble.Config{
		P: 4, M: 8,
		TF: 1, TB: 1, TW: 1, TComm: 0.1,
		MemF: 3, MemW: 1,
		MemoryLimits: []int64{12, 12, 12, 12},
	}

	cmp, err := zerobubble.Compare(cfg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(cmp.Baseline.Gantt())
	fmt.Print(cmp.ZeroBubble.Gantt())
	fmt.Print(cmp.Report())

	if !cmp.StrictlyBetter() {
		log.Fatal("Zero-Bubble did not improve this configuration")
	}
	if err := cmp.ZeroBubble.Validate(); err != nil {
		log.Fatal(err)
	}
}
```

单独构建某一个排期：

```go
baseline, err := zerobubble.Build1F1B(cfg)
zb, err := zerobubble.BuildZeroBubble(cfg)
```

## 主要 API

| API | 说明 |
| --- | --- |
| `Config` | 阶段数、微批数、耗时、通信延迟、显存参数和逐阶段上限 |
| `StageCost` | 可选阶段覆盖：`TF/TB/TW/MemF/MemW` |
| `Build1F1B(cfg)` | 标准 1F1B；一个 backward 任务代表整体反向，耗时为 `TB+TW` |
| `BuildZeroBubble(cfg)` | 拆分 `F/B/W` 的 Zero-Bubble 排期 |
| `Build(cfg, mode)` | 按 `Mode1F1B` / `ModeZeroBubble` 分发 |
| `Compare(cfg)` | 同时构建 1F1B 和 ZB，计算气泡率/makespan 改善 |
| `Comparison.StrictlyBetter()` | 是否同时缩短 makespan 且严格降低气泡率 |
| `Schedule.Gantt()` | 精确任务区间表、阶段峰值表和紧凑甘特时间轴 |
| `Schedule.Validate()` | 完整审计，失败返回聚合错误 |
| `Audit(schedule)` | 非短路审计报告，包含各检查项、峰值显存和 DAG 规模 |
| `CheckCommunication(schedule)` | Send/Recv 配对、FIFO、双向握手和通信无死锁报告 |

### 显存配置

`MemoryLimits` 长度为 `1` 时复制到所有阶段；长度为 `P` 时逐阶段生效。单位与 `MemF/MemW` 一致，包内不做字节换算。

```go
cfg := zerobubble.Config{
	P: 3, M: 12,
	TF: 1.0, TB: 0.8, TW: 0.6, TComm: 0.05,
	MemF: 5, MemW: 2,
	MemoryLimits: []int64{18, 16, 12},
}
```

非均匀阶段：

```go
cfg.StageCosts = []zerobubble.StageCost{
	{TF: 1.2, TB: 1.0, TW: 0.8, MemF: 5, MemW: 2},
	{TF: 0.9, TB: 1.4, TW: 1.1, MemF: 6, MemW: 3},
	{TF: 1.1, TB: 0.8, TW: 0.7, MemF: 4, MemW: 2},
}
```

`StageCosts` 非空时必须恰好有 `P` 项。

## 调度模型

### 时间与资源

- 每个阶段同一时刻只执行一个计算任务。
- `F(m,s)` 在阶段 `s` 上执行 `TF[s]`。
- 跨阶段 `F(m,s-1) -> F(m,s)` 需要 `TComm`。
- `B(m,s)` 在阶段 `s` 上执行 `TB[s]`。
- 跨阶段 `B(m,s+1) -> B(m,s)` 需要 `TComm`。
- `W(m,s)` 在阶段 `s` 上执行 `TW[s]`，无跨阶段依赖。
- 区间采用半开形式 `[Start, End)`；同一时刻结束和开始允许紧邻。

### 显存生命周期

每个微批在阶段 `s` 上：

1. `F(m,s)` 启动时占用 `memF`。
2. `B(m,s)` 完成时释放部分激活，保留 `memW`。
3. `W(m,s)` 完成时释放最后的 `memW`。

任意事件时刻都要求：

```text
resident(s) <= memLimit[s]
```

排期器在启动新 `F` 前同时预留 `max(memF, memW)`，避免非常规 `memW > memF` 配置下后续 `B` 完成瞬间超限。若显存不足以再启动 `F`，会优先排空最老的已就绪 `W`。

### 排期优先级

1. 已就绪的 `B`：关键路径优先，尽快解除上游等待。
2. 已就绪且显存允许的 `F`：保持前向供给。
3. 已就绪的 `W`：填充阶段空档。
4. 若 `F` 被显存阻塞，强制先执行最老的 `W`。

`W` 不阻塞任何跨阶段依赖，因此只要本地阶段空闲且没有更高优先级任务可运行，就以 `W` 填满空档。

### 通信模型

每个跨阶段边生成一条匹配消息：

- 激活消息：`F(m,s-1) -> F(m,s)`。
- 梯度消息：`B(m,s+1) -> B(m,s)`。

`Schedule.Comms` 中每条消息包含精确的 `SendStart/SendEnd` 与 `RecvStart/RecvEnd`。校验器检查：

- 目标阶段、生产/消费任务和消息键唯一匹配。
- 同一有向链路 FIFO 的发送与接收顺序一致。
- 双向同步握手不会出现双方都在等待未 posted receive 的死锁形态。
- 任务依赖边、阶段串行边、Send/Recv 边和 FIFO 边组成的最终图严格无环。

## 指标

设 `work` 为所有计算任务耗时之和，`makespan` 为最后一个任务结束时间：

```text
capacity   = P * makespan
bubbleTime = capacity - work
bubbleRate = bubbleTime / capacity
```

- 1F1B 基准的整体 backward 耗时计为 `TB+TW`。
- ZB 排期的总计算量与 1F1B 相同，只是拆成 `TB` 与 `TW` 两个任务。
- 对存在结构性流水线气泡的常见配置，`Compare` 会显著降低该气泡率；`P=1` 或没有可填充气泡的极端配置不会伪造改善。

## 命令行示例

```bash
go run ./cmd/zbpipeline \
  -p 4 -m 8 \
  -tf 1 -tb 1 -tw 1 -tcomm 0.1 \
  -memf 3 -memw 1 -limits 12
```

`-limits` 可传单值或逗号分隔的逐阶段值：

```bash
go run ./cmd/zbpipeline -p 3 -m 16 -limits 18,16,12
```

只看对比与审计摘要：

```bash
go run ./cmd/zbpipeline -compact
```

## 测试

```bash
go test ./...
go vet ./...
```

验收测试覆盖：

- `F -> B -> W` 阶段内与跨阶段依赖。
- 严格逐阶段峰值 `<= memLimit`。
- 紧显存下在下一 `F` 前强制插入最老 `W`。
- `memW > memF` 的显存转换边界。
- Send/Recv 配对、FIFO 与双向同步握手无死锁。
- 非均匀阶段耗时/显存。
- 多组配置下气泡率与 makespan 严格优于 1F1B。
- 依赖、显存、通信被破坏时审计必须拒绝。
