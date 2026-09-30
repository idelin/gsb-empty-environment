# telemetry — 有界接收队列

按数据优先级与租户公平调度处理遥测事件，支持明确的丢弃策略，输出
接收 / 处理 / 丢弃守恒统计。仅使用 Go 标准库。

## 设计

- **有界队列**：总容量固定（`-capacity`），跨所有租户共享。
- **丢弃策略**（`-policy`）：
  - `reject`（默认）：队列满时拒绝新事件，计 `dropped[queue_full]`；
  - `drop-oldest`：驱逐全局最旧的排队事件，计 `dropped[queue_full]`。
- **调度**：租户间轮转（公平）；租户内高优先级优先，但连续处理
  `-high-burst`（默认 3）条高优先级后强制处理一条普通事件，持续
  高优先流不会完全饿死普通流。
- **关闭模式**（`-shutdown`）：
  - `drain`：排空队列，所有排队事件处理完后退出；
  - `stop`：立即丢弃积压（计 `dropped[shutdown]`），仅等待在途
    处理完成。
- **守恒不变式**：`received == processed + dropped`（全局及每租户）。
  被拒 / 被丢弃的记录绝不计入已处理。

## 运行

```sh
# 本地合成事件并处理
go run . -mode synth -events 300 -tenants 4 -capacity 32 -workers 4 -rate 400

# 生成夹具文件并回放
go run . -emit -events 120 -tenants 3 -seed 7
go run . -mode replay -fixture fixtures/events.jsonl -capacity 16 -workers 2

# 立即停止模式
go run . -mode synth -events 300 -capacity 32 -shutdown stop
```

## 测试

```sh
go test ./... -v
```

覆盖：竞争下守恒、高优先不饿死普通流、被拒不计已处理、drain 排空、
stop 丢弃积压、关闭后到达计入丢弃、租户公平、drop-oldest 策略、
夹具生成 / 回放守恒。
