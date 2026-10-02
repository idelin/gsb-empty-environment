# pbwire-adaptive

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器 + 反射模式校验引擎 + 自适应动态阈值伸缩与负载感知控制器（TypeScript，Node.js 内置测试运行器验证）。

- 运行时：Node.js ≥ 23.6（原生 TypeScript 类型擦除执行，无需编译、无需任何 npm 依赖）
- 测试：`node --test`（node:test + node:assert）

## 模块

| 文件 | 职责 |
| --- | --- |
| `src/varint.ts` | Varint 7 位压缩编解码（64 位 BigInt，10 字节上限）、ZigZag 32/64 有符号转换 |
| `src/reader.ts` | 边界安全游标 `WireReader`：每次读取先校验剩余字节，截断即抛 `TruncatedMessageError` |
| `src/writer.ts` | 增长式 `WireWriter`：varint / fixed32 / fixed64 / float / double / length-delimited |
| `src/schema.ts` | 反射模式校验引擎：声明式 schema 驱动编解码，支持嵌套消息、Packed Repeated、未知字段跳过、默认值、required/min/max/maxLength/自定义谓词校验、嵌套深度防护 |
| `src/adaptive.ts` | 自适应控制器：压力评分 → 乘法收缩（连击升級）+ 冷却 + 滞回带 + 连续低水位才加法增长（带抖动）+ 窗口速率限制 + 许可式准入控制 |

## 自适应控制律

压力评分（0..1，权重可调）：

```
score = w_q·queue/(queue+limit) + w_e·errorRate + w_l·lat/(lat+target)
      + w_g·lag/(lag+target) + w_m·memory        (权重归一化)
```

- `score ≥ shrinkThreshold`：乘法收缩 `limit ← floor(limit · shrinkFactor · escalation^(n-1))`，连续收缩升級、有下限；触发冷却期
- `score ≤ growThreshold`：需连续 `growStreak` 个低水位样本才加法增长一步（`growthStep` × 抖动），冷却期内禁止增长 —— 防雪崩/防惊群
- 两者之间为滞回带：不动作并重置 streak，防振荡
- 每 `windowMs` 最多 `maxAdjustmentsPerWindow` 次调整（速率限制）
- `tryAcquire()/acquire()` 许可式准入：饱和即拒绝或排队（支持超时），`dispose()` 拒绝全部等待者并清空监听器/定时器/历史环形缓冲 —— 严格无泄漏

## 运行

```bash
npm test      # node --test（57 项测试）
npm run demo  # 编解码 + 截断防护 + 雪崩场景自适应演示
```

## 测试覆盖（57 项）

- `test/varint.test.ts`：varint 边界值（0/127/128/2^32/2^53/2^64-1）、10 字节上限、溢出与截断、ZigZag 规范映射与密集往返、fixed/float/double、非法 tag/wire type
- `test/schema.test.ts`：全标量 + 5 层嵌套 + Packed Repeated 往返、packed/unpacked 双形态解码、未知字段跳过、默认值、校验引擎（required/范围/长度/自定义谓词/嵌套路径）、UTF-8 校验、深度防护
- `test/truncation.test.ts`：对合法流逐字节截断全部安全报错（Truncated/Validation，绝不静默成功）；嵌套消息与 packed 数组内部截断；5000 条随机垃圾 + 2000 条变异报文模糊测试（错误类型严格受控，无越界）；恶意超大长度前缀
- `test/adaptive.test.ts`：收缩/升級/冷却/滞回/增长 streak/抖动/上下限钳制/速率限制/雪崩场景；200 并发 worker 动态限流一致性；许可幂等释放；acquire 超时；dispose 清理；1 万样本环形缓冲有界 + 1 万次 acquire/release 零残留（无泄漏断言）
