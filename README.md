# proto-wire-metrics

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器 + 细粒度度量监控引擎（TypeScript，Node.js ≥ 23.6 原生类型擦除运行，无需编译步骤）。

## 模块

- `src/wire.ts` — 手工实现的 Wire-format 原语：Varint（7 位压缩，完整 uint64 经 `bigint`）、ZigZag 有符号转换、Fixed32/64、Float/Double、Length-delimited、Tag 解析与未知字段跳过。每次读取均做边界检查，截断/越界抛出带稳定错误码的 `DecodeError`。
- `src/decoder.ts` — 反射模式校验引擎：按运行时 Schema 描述符把二进制流还原为结构体，支持多层嵌套消息、Packed/非 Packed Repeated、严格模式与嵌套深度防护。
- `src/metrics.ts` — 度量引擎：Counter / Gauge / 延迟直方图（桶分布 + p50/p90/p99 插值）/ 命中率 / 错误码计数，高精度 `performance.now` 计时，JSON 快照与 Prometheus 风格文本导出，序列容量防护避免标签基数泄漏。
- `src/instrumented.ts` — 解码器与度量的集成层：自动采集每次解码的延迟、成功/错误计数、字段命中率与最大嵌套深度。

## 运行

```bash
npm test        # node --test "test/*.test.ts"（44 个用例）
npm run example # 端到端演示：手工编码 -> 反射解码 -> 截断防护 -> 度量导出
```

## 错误码

`ERR_TRUNCATED`（字段/流截断，安全越界防护）、`ERR_INVALID_VARINT`（>10 字节或超 64 位）、`ERR_INVALID_WIRE_TYPE`、`ERR_INVALID_TAG`、`ERR_NESTING_TOO_DEEP`、`ERR_GROUP_MISMATCH`。

## 并发与泄漏保证

所有度量变更均为不跨 `await` 的同步临界区，异步任务交错不会造成计数撕裂；`MetricRegistry` 以 `maxSeriesPerKind` 限制标签基数，`reset()` 后 `seriesCount === 0`。测试覆盖 64 任务 × 500 次交错递增的一致性断言与 100 轮churn 的无泄漏断言。
