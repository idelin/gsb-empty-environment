# protowire — 零依赖 Protobuf Wire-Format 解析器 + 自适应限流器

纯 Go 标准库实现（兼容 Go 1.11+），无任何外部依赖。

## 组件

| 包 | 职责 |
|---|---|
| `wire` | 底层编解码：Varint 7 位压缩、ZigZag 有符号映射、Fixed32/64、Length-delimited，全程边界检查 |
| `proto` | 消息解析：tag 分派、多层嵌套消息（深度上限 100）、Packed Repeated 数组解码 |
| `schema` | 反射引擎：`protobuf` tag 绑定结构体字段，`validate` tag 约束校验，`Validate()` 领域钩子 |
| `adaptive` | 自适应限流器：AIMD 阈值伸缩 + 负载感知卸载 + 抖动/冷却防雪崩，并发安全 |
| `cmd/demo` | 端到端运行样例 |

## 运行

```sh
go run ./cmd/demo     # 运行样例
go test ./...         # 单元测试
go test -race ./...   # 竞态 + 泄漏断言
```

## 自适应限流器要点

- **阈值伸缩**：延迟健康时加性增长（带 50%–100% 抖动步长），延迟超基线
  `LatencyTolerance` 倍或任务被丢弃时乘性下降（`DecreaseFactor`），
  钳制在 `[MinLimit, MaxLimit]`。
- **负载感知**：外部压力 `load ∈ [0,1]`（可 `SetLoad` 或 `MetricsFunc` 周期采样）
  按比例收缩有效阈值：`eff = limit × (1 − LoadSensitivity × load)`，提前卸负载。
- **防雪崩**：下降有 `Cooldown` 冷却期防抖；增长步长随机化避免惊群同步；
  阻塞获取的退避随负载与在途压力伸缩并加抖动。
- **无泄漏**：`Permit` 由 `sync.Once` 保证恰好归还一次；`Close` 等待采样
  goroutine 退出；测试断言 `runtime.NumGoroutine` 回到基线。

## 结构体绑定示例

```go
type Session struct {
    User   string  `protobuf:"string,1" validate:"required,minlen=1"`
    Scores []int64 `protobuf:"zigzag64,3,rep,packed"`
    Token  uint64  `protobuf:"fixed64,4"`
}
var s Session
err := schema.Unmarshal(data, &s) // 解析 + 绑定 + 校验一步完成
```

截断输入统一报告 `wire.ErrTruncated` / `*wire.TruncatedError`，绝不越界读取。
