# wiremon — Protobuf Wire 解析器 + 反射校验 + 细粒度度量监控

零外部依赖（仅 Go 标准库）的三个组件：

## 组件

- **`wire/`** — 纯手工 Protocol Buffers 二进制 Wire-format 编解码
  - Varint 7 位压缩编解码（10 字节上限，溢出检测）
  - ZigZag 有符号整数变换（sint32/sint64）
  - Fixed32 / Fixed64（小端）
  - Length-delimited 嵌套消息（深度上限 `MaxDepth=100`，防栈耗尽）
  - Packed / Unpacked Repeated 数组两种形态均可解码
  - 全程边界检查：字段截断返回 `*BoundsError`（`wire.IsTruncated` 可穿透包装链判定），绝不越界 panic
  - `Unmarshal` 反射解码到结构体（`protobuf:"enc,num"` 标签），未知字段自动跳过
  - `Validate` 反射校验引擎（`validate:"required,min=,max=,minlen=,maxlen="`，嵌套递归，聚合所有违例）

- **`metrics/`** — 并发安全的度量采集与聚合导出
  - `Counter` / `Gauge`（atomic / CAS）
  - `Histogram`：桶计数 + sum/min/max + 2048 样本滑动时序窗口，精确 p50/p90/p99 分位数，`Timer` 纳秒级延迟采集
  - `HitRate` 命中率、`ErrorCodes` 错误码分类计数
  - `Registry` 带标签的命名注册表（标签键排序规范化，双重检查锁定）
  - `Snapshot` 一次性快照，导出 Prometheus 风格文本或 JSON（`+Inf` 桶安全序列化）
  - 无后台 goroutine，无泄漏（测试断言 `runtime.NumGoroutine` 前后一致）

- **`cmd/demo/`** — 运行样例：手工编码三层嵌套订单消息（含 packed repeated、fixed32/64、zigzag），
  循环解码 1000 次采集延迟分布，演示截断输入的安全拒绝，并导出文本/JSON 度量。

## 运行

```sh
go test ./...          # 全部测试
go test -race ./...    # 竞态检测
go run ./cmd/demo      # 运行样例
```

## 结构体标签示例

```go
type Order struct {
    OrderID  int64    `protobuf:"zigzag64,1" validate:"min=1"`
    Customer string   `protobuf:"string,2"  validate:"required,minlen=1,maxlen=64"`
    Amounts  []int32  `protobuf:"zigzag32,3"` // packed 或 unpacked 均可
    Address  Address  `protobuf:"message,4"`
    Freight  float64  `protobuf:"fixed64,6"`
    Checksum uint32   `protobuf:"fixed32,7"`
}
```
