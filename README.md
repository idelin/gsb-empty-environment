# wirecodec-fair-scheduler

零外部依赖的 TypeScript 实现，包含两个组件：

1. **二进制紧凑变长编解码器 + 反射模式校验引擎** — 纯手工解析 Protocol Buffers 二进制 wire format。
2. **多租户隔离与配额公平调度器** — 基于 Deficit Round Robin (DRR) 的加权公平排队。

无 `node_modules`，无构建步骤：Node.js ≥ 22.6 直接以 `--experimental-strip-types` 运行 TypeScript。

## 运行

```bash
npm test    # Node 内置测试运行器，36 个用例
npm run demo
```

## 组件一：Protobuf Wire-format 编解码器（`src/wire/`）

| 文件 | 职责 |
| --- | --- |
| `reader.ts` | 边界检查的零拷贝读取器：Varint（7 位压缩）、ZigZag、Fixed32/64、float/double、length-delimited、tag 解析、未知字段跳过 |
| `writer.ts` / `encoder.ts` | 对应的编码器（测试与样例使用） |
| `schema.ts` | 反射模式校验引擎：按 schema 把 wire 流物化为结构体，支持多层嵌套 message、packed/unpacked repeated、未知字段安全跳过 |
| `errors.ts` | `WireError`，kind ∈ `truncation / overflow / invalid-wire-type / invalid-tag`，携带出错偏移量 |

安全特性：

- 每次读取前校验剩余长度；字段截断抛出 `WireError(kind="truncation")`，绝不越界读。
- Varint 超过 10 字节或第 10 字节超过 1 位 → `overflow`。
- 嵌套 message 声明长度超过缓冲区 → `truncation`。
- 字段号 0、保留 wire type（3/4）→ 拒绝。

```ts
import { decodeMessage } from "./src/wire/schema.ts";
const decoded = decodeMessage(buf, {
  fields: {
    1: { name: "id", type: "zigzag32" },
    2: { name: "tags", type: "zigzag32", repeated: true, packed: true },
    3: { name: "payload", type: "message", message: NestedSchema },
  },
});
```

## 组件二：多租户 DRR 公平调度器（`src/scheduler/scheduler.ts`）

- **隔离**：每租户独立 FIFO 队列、独立并发配额（`maxConcurrent`）、独立积压上限（`maxQueue`，溢出即拒绝），互不影响。
- **公平**：DRR 加权排队 — 每轮访问为租户注入 `weight × baseQuantum` 的 deficit 令牌，队首任务 cost 不超过 deficit 即可派发；剩余 deficit 结转下轮，长期服务比例严格正比于权重，与任务成本分布无关。
- **无泄漏**：配额先取后派，`finally` 中释放；任务同步抛错或异步拒绝都不会泄漏槽位；`drain()` 可断言调度器回到完全空闲。

```ts
const sched = new TenantScheduler({ maxConcurrentTotal: 8 });
sched.registerTenant("analytics", { weight: 3, maxConcurrent: 4 });
sched.registerTenant("guest", { weight: 1, maxConcurrent: 1, maxQueue: 100 });
const result = await sched.submit("analytics", { cost: 2, run: () => doWork() });
await sched.drain(); // 之后断言 inFlight === 0
```

## 测试覆盖（`test/`，Node 内置 runner）

- `wire.test.ts`：varint 边界（127/128/2³²/2⁶⁴）、zigzag 极值、fixed/float 往返、截断与溢出防护、tag 合法性、skip 边界。
- `decoder.test.ts`：三层嵌套 + packed repeated 往返、protoc 布局逐字节比对、packed/unpacked 混合、未知字段跳过、**逐字节截断扫描**（所有前缀都不会造成越界）、wire-type 不匹配。
- `scheduler.test.ts`：权重 2:1 派发窗口断言、异构成本下的成本比例公平、空闲租户不攒信用、租户/全局并发上限、防饿死、积压配额拒绝、失败任务许可释放、200 并发结果正确路由、deficit 结转与清零、注册校验、租户注销。
