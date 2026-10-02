# pbwire — 零依赖 Protobuf Wire-format 解析器 + 细粒度度量引擎

纯 Go 标准库实现（无任何外部依赖），包含两个组件：

## `wire` — 二进制紧凑变长编解码器与反射模式校验引擎

- **Varint**：7 位压缩编码，最长 10 字节，第 10 字节仅允许 bit 63，超限报 `overflow`
- **ZigZag**：`sint32/sint64` 有符号整数变换（`ZigZag32/64`、`EncodeZigZag32/64`）
- **Fixed32/64**：小端定长读取，不足字节报 `truncated`
- **Length-delimited**：长度前缀先校验再切片，声明长度超过剩余输入报 `negative_length`（安全越界防护）
- **嵌套消息**：`DecodeMessage` 通用消息树，递归深度上限（`DefaultMaxDepth=100`）防栈耗尽
- **Packed Repeated**：`PackedVarints` / `PackedFixed32` 显式解码
- **反射引擎**：`Unmarshal` 按 `pb:"<编号>,<类型>"` 结构体标签还原字段，
  支持 `varint/zigzag/bool/fixed32/fixed64/string/bytes/msg/packed`，
  未知字段跳过，标签与 wire type 不符报 `type_mismatch`
- **错误码**：所有失败返回 `*wire.Error`，`CodeOf` 提取机器可读错误码
  （`truncated/overflow/invalid_wire_type/bad_field_number/negative_length/depth_limit/type_mismatch/unsupported`）

## `metrics` — 细粒度度量监控与统计聚合导出

- `Counter` / `Gauge`：原子操作
- `Histogram`：固定桶延迟分布（count/sum/min/max/累计桶）
- `Window`：滑动时间窗，精确 P50/P90/P99，过期样本即时驱逐（内存有界）
- `HitRate`：命中率（hits/misses）
- `ErrorCodes`：按错误码聚合计数
- `Registry`：命名注册表 + `Snapshot()` 快照 + `Export()` Prometheus 风格文本导出
- 全部并发安全（`-race` 验证），不启动任何后台 goroutine（无泄漏）

`wire.Metered` 将两者连接：每次解码记录延迟直方图、滑动窗、字节吞吐、
schema 缓存命中率与错误码计数。

## 运行

```sh
export GOCACHE=$(mktemp -d)   # 沙箱环境需要可写缓存
go test -race ./...           # 全部测试（含并发与泄漏断言）
go run ./cmd/pbdemo           # 运行样例：嵌套消息解码 + 度量导出
```

## 样例输出

```
encoded stream: 51 bytes
08a94610531a0564656c696e22170a085368616e6768616910c19a0c19efbeadde049c00002a0305000e31efcdab8967452301

decoded: id=9001 delta=-42 customer="delin" city=Shanghai zip=200001 scores=[-3 0 7] checksum=0x123456789abcdef

truncated stream rejected safely: wire: declared length 3 exceeds 2 remaining bytes (code=negative_length offset=38 field=0)

--- metrics export ---
decode_calls_total 3
decode_latency_ms_bucket{le="+Inf"} 3
decode_errors_total{code="negative_length"} 1
schema cache hit rate: 0.67
```
