# cepengine — 多模式复合事件序列匹配器

基于非确定性有向无环图（NFA-DAG）的轻量 CEP 引擎，仅使用 Go 标准库。

## 能力

- 时序模式 `A -> B` 且中间不允许出现 `C`（否定守卫节点）
- 跨事件时间滑窗（B 必须在 A 之后 window 毫秒内出现，边界含端点）
- 乱序事件补偿：迟到的 C 落入已命中区间 `(A, B)` 时，精准撤销该命中并写入日志
- 模式超时丢弃：水位线推进后，超过窗口仍未闭合的 A 运行被丢弃并记录

## 模式图

```
start -> A -> negC(否定守卫) -> B -> final
```

## 运行

```sh
go test ./cep/     # 测试（含验收场景）
go run ./cmd/demo  # 演示：t=0 A, t=5 B 命中；迟到 t=2 C 撤销命中
```

## API 摘要

- `cep.NewEngine(windowMillis)` 创建引擎
- `engine.Process(ev)` 处理事件，返回 `MATCH` / `RETRACT` 通知
- `engine.Flush(watermark)` 强制推进水位线，触发超时丢弃
- `engine.Logs()` / `engine.ActiveMatches()` 审计日志与存活命中
