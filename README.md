# wireinspect

零外部依赖（仅标准库）的 Protocol Buffers 二进制 Wire-format 解析器 + 全量状态离线完整性巡检与不变式断言引擎（Go 实现）。

## 组件

- **`wire/`** — 纯手工二进制编解码器
  - Varint 7 位压缩编解码（含 10 字节上限与第 10 字节溢出检测）
  - ZigZag 有符号整数变换（sint32/sint64）
  - Fixed32/Fixed64 小端定长编解码
  - Length-delimited 嵌套消息与 Packed Repeated 数组解码
  - 反射模式解码引擎：`Unmarshal(data, &out)` 依据 `pb:"<num>,<kind>"` struct tag 还原结构体，未知字段按 protobuf 语义跳过
  - 全路径安全越界防护：任何截断输入返回 `ErrTruncated` / `ErrNegativeLength` / `ErrOverflow`，绝不越界读取
- **`inspector/`** — 形式化约束巡检与不变式断言仪
  - 悬挂引用（dangling reference）检测
  - 孤立环路检测：迭代式 Tarjan SCC + 根可达性分析（20 万节点链不爆栈）
  - 数据倾斜检测：max/mean 权重比阈值告警
  - 重复 ID、自环、负权重、不可达节点巡检
  - 不变式断言仪：`Assert(report, StandardInvariants...)` 聚合违规报告
  - `Inspect` 为纯函数，并发安全
- **`cmd/demo/`** — 运行样例

## 运行

```sh
go test ./...      # 全部测试
go test -race ./...# 竞态检测
go run ./cmd/demo  # 运行样例
```

## 运行样例输出

```
=== 1. Hand-built nested protobuf stream ===
encoded 91 bytes: 08 a9 46 12 0f 74 65 6c 65 6d 65 74 72 79 2d 62 61 74 63 68 ...
decoded: ID=9001 Title="telemetry-batch" Deltas=[-3 0 7 -128 4096] Fixed=0xdeadbeef Big=0x0123456789abcdef Active=true
nested:  &{Name:leaf Score:-8192 Values:[1 300 65536 1152921504606846983] ...} -> &{Name:root-of-leaf Score:42 ...}

=== 2. Truncation guard ===
truncated to  1/91 bytes -> wire: truncated buffer, safe bounds guard triggered
truncated to 45/91 bytes -> wire: length prefix exceeds remaining buffer
truncated to 90/91 bytes -> wire: truncated buffer, safe bounds guard triggered

=== 3. Offline integrity inspection ===
nodes=6 edges=6 reachable=3 cyclicGroups=1 skewRatio=5.38 ok=false
  [FATAL] dangling_reference: record 3 references missing record 99
  [FATAL] orphan_cycle: orphan cycle unreachable from any root: [4 5]
  [WARN] unreachable_node: record 6 is unreachable from any root
  [WARN] data_skew: weight skew ratio 5.38 exceeds threshold 3.00 (max=400 mean=74.33 over 6 nodes)

=== 4. Invariant assertion ===
assert(StandardInvariants): invariant violations: NoDanglingRefs: 1 dangling reference(s); NoOrphanCycles: 1 orphan cycle(s)
assert(clean graph): <nil>
```

## 测试结果（`go test -race -count=1 ./...`，26 项全部通过）

```
ok  wireinspect/inspector  TestDanglingReference / TestOrphanCycleVsReachableCycle / TestSelfLoop
                            TestDuplicateIDAndNegativeWeight / TestUnreachableNode / TestDataSkew
                            TestInvariantAssertions / TestDeepGraphNoStackOverflow(20万节点)
                            TestConcurrentInspectDeterministic(64协程×100轮) / TestNoGoroutineLeak
ok  wireinspect/wire        TestVarintRoundTripBoundaries(含MaxUint64) / TestVarintKnownEncodings
                            TestVarintTruncationEveryPrefix / TestVarintOverflow
                            TestZigZagRoundTrip(含MinInt64) / TestFixedRoundTripAndTruncation
                            TestTagValidation / TestConsumeBytesGuards
                            TestParseMessageNestedAndPacked / TestParseMessageTruncationEveryPrefix
                            TestParseMessageRandomGarbageNeverPanics(2万随机缓冲) / TestPackedTruncation
                            TestUnmarshalNestedPacked / TestUnmarshalTruncatedNested
                            TestUnmarshalErrors / TestUnmarshalConcurrent(64协程×200轮)
```
