# pbufsim — Protobuf Wire 编解码 + 确定性事件模拟与故障复现

零外部依赖（仅 Go 标准库）。

## 组成

- `wire/` — 纯手工 Protocol Buffers 二进制 wire-format 解析器
  - Varint 7 位压缩编解码（含 10 字节上限与溢出检测）
  - ZigZag 有符号整数变换
  - Fixed32/Fixed64 小端编解码
  - 嵌套 Length-delimited 消息逐层解析
  - Packed repeated 数组（varint / fixed32 / fixed64，对齐校验）
  - 反射模式校验引擎：`wire:"num,kind"` 结构体标签驱动 `Validate`/`Unmarshal`，
    嵌套深度上限 100 层，所有截断输入安全报错（绝不越界读取）
- `sim/` — 离散时间事件驱动模拟框架
  - 事件按 (time, priority, seq) 全序调度，splitmix64 种子化随机流
  - 轨迹录制（FNV-1a 哈希）支持百万步确定性回归
  - 不变量检查 + 故障注入 + `Reproduce` 逐位复现 BUG
  - `Schedule` 并发安全（互斥锁保护），测试断言无 goroutine 泄漏
- `cmd/demo/` — 端到端运行样例

## 运行

```sh
go test ./...        # 全部测试（含 100 万步回归）
go test -race ./...  # 竞态检测
go run ./cmd/demo    # 运行样例
```
