# pbufsim — Protobuf Wire 解析器 + 反射校验引擎 + 确定性模拟框架

零外部依赖（仅 Go 标准库），四个包加一个在端到端演示：

| 包 | 职责 |
|---|---|
| `wire`    | 二进制 Wire-format 读写：Varint(7位压缩)、ZigZag、Fixed32/64、Length-delimited，全程边界检查 |
| `message` | 无模式通用消息解析：嵌套消息树、Packed Repeated 解包、截断安全 |
| `schema`  | 反射模式校验引擎：声明式描述符 → 类型化解码 + 违规收集（required/range/max-len/未知字段/嵌套深度） |
| `sim`     | 离散时间事件驱动模拟：(Time,Seq) 堆排序、种子化 RNG、FNV 哈希链、录制/重放、故障注入 |
| `cmd/demo`| 端到端演示：编码→解析→校验→截断防护→百万步模拟→故障复现 |

## 运行

```sh
go test ./... -count=1        # 全部单元测试（41 个）
go test ./... -race           # 并发竞态检测
go test ./sim -run Million    # 百万步确定性回归
go run ./cmd/demo             # 端到端演示
```

## 演示输出样例

```
=== 3. Reflection validation engine ===
  ts=1700000000 volumes=[100 250 175] deltas=[-12 3 9]
  order[0] id=7 price=9950 symbol=BTCUSD
  order[1] id=8 price=12300 symbol=ETHUSD

=== 4. Truncated stream -> safe bounds error ===
  decode(47 of 50 bytes) -> message: field 4 at offset 45:
  wire: truncated bytes at offset 46: need 3 byte(s), have 0

=== 5. Deterministic simulation: 1,000,000 steps ===
  1000000 steps in 1.006s, final hash = b9920fd1fd559a82
  rerun identical: true

=== 6. Fault injection & bug reproduction ===
  baseline vs faulty: length: 1000000 != 4092
  faulty hash = e700efc331fffd37, replay reproduces exactly: true
```

## 设计要点

- **安全越界防护**：所有读取先检查剩余字节；截断输入返回 `*wire.TruncatedError`
  （含偏移量/需求/剩余），绝不 panic、绝不越界。`TestParseTruncatedAtEveryByte`
  对每个字节前缀验证。
- **确定性契约**：事件按 `(Time, Seq)` 全序执行；唯一随机源是引擎内置
  xorshift64*（种子化）；每个事件折叠进 FNV-1a 哈希链。相同种子+初始负载+
  故障配置 ⇒ 逐位相同的执行轨迹（`sim.Replay` + `sim.Diff`）。
- **故障复现**：`FaultFunc` 在调度时改写/丢弃事件（`DropEvery`、`DelayKind`）。
  故障运行重放后轨迹逐位一致，实现 BUG 确定性复现。
- **并发安全**：`Schedule`/`Uint64n`/`Now` 由互斥锁保护，`-race` 下
  8 生产者并发调度测试通过；引擎不启动后台 goroutine，
  `TestNoGoroutineLeak` 断言无泄漏。
- **回归锚点**：`TestGoldenHash` 固定 1,000,000 步的哈希指纹
  （`e07d02726f30c6ba`），任何行为漂移都会失败。

## 测试结果（go test ./... -race -count=1）

```
ok  pbufsim/wire     2.282s   (12 tests: varint/zigzag/fixed/bytes/tag 全边界)
ok  pbufsim/message  1.473s   (12 tests: 4层嵌套、packed、逐字节截断)
ok  pbufsim/schema   1.909s   (11 tests: required/range/max-len/深度/未知字段)
ok  pbufsim/sim      141.541s (9 tests: 百万步回归、黄金哈希、故障复现、竞态、无泄漏)
```
