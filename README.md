# session-read-router

TypeScript 实现的会话读取路由库：在带复制延迟的分区集群上提供**单调读**保证——
会话记录每次响应的版本水位，后续读取永不返回更旧版本。

## 核心概念

- `VersionVector` — 每个分区记录已见的最高版本；合并时逐分区取最大值。
- `Session` — 客户端会话。每次路由读取的响应版本都会记入会话水位
  （`recordResponse`），水位只升不降；`merge` 按分区取最大水位合并两个会话。
- `SessionRouter` — 路由读取。仅当响应版本 `>=` 会话对该分区的水位时才返回；
  副本落后时由显式策略 `StalePolicy` 决定行为：
  - `WAIT` — 轮询同一节点，直到它追平水位或超过 `timeoutMs`；
  - `SWITCH` — 依次尝试其他节点，全部落后或超时则失败；
  - `TIMEOUT` — 不等待、不换节点，立即抛出 `SessionTimeoutError`。
- `SimulatedNode` — 模拟副本节点：每分区独立版本（用 `advance` 模拟复制/写入），
  每次读取付出固定或区间随机延迟。

## 使用

```ts
import { Session, SessionRouter, SimulatedNode, StalePolicy } from "session-read-router";

const nodes = [
  new SimulatedNode("node-a", { latencyMs: [1, 5], partitions: { p0: 10 } }),
  new SimulatedNode("node-b", { latencyMs: [1, 5], partitions: { p0: 8 } }),
];
const router = new SessionRouter(nodes, { policy: StalePolicy.SWITCH, timeoutMs: 500 });

const session = new Session();
const r1 = await router.read(session, "p0"); // 记录水位
session.merge(otherSession);                 // 合并：逐分区取最大水位
const r2 = await router.read(session, "p0"); // 保证 r2.version >= r1.version
```

## 构建与测试

```sh
npm run build   # tsc -p tsconfig.json
npm test        # 编译并运行 test/simulation.test.ts
```

测试模拟节点延迟与分区复制滞后，覆盖：水位单调性、会话合并取最大、
三种策略在副本落后时的等待/换节点/超时行为，以及随机工作负载下
逐分区核对每次返回版本不回退。
