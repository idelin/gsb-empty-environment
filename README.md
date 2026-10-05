# session-read-router

会话级读路由库（TypeScript）：响应中携带版本水位并记录进会话，后续读取绝不返回更旧版本（单调读 / 读己之写）。

## 核心概念

- **Session**（`src/session.ts`）：按分区记录客户端已见的最高版本水位；可序列化为 token 跨请求传递；`Session.merge(a, b)` 合并时每个分区取最大水位。
- **Cluster / ReplicaNode**（`src/store.ts`）：模拟分区 + 主库 + 多个读副本。每个副本有独立的处理延迟 `latencyMs` 与复制滞后 `replicationLagMs`；写入先在主库生效，再按滞后调度复制事件。
- **SessionRouter**（`src/router.ts`）：读取前响应版本记录回会话；只从已追平会话水位的副本读取，并有防御性不变量检查（`StaleReadError`）。
- **VirtualClock**（`src/clock.ts`）：确定性虚拟时钟，用 `advance(ms)` 模拟延迟与复制，测试无需真实等待。

## 副本落后时的显式策略（`LagPolicy`）

| 策略 | 行为 |
| --- | --- |
| `{ kind: 'wait' }` | 挂起等待（事件驱动，不轮询），直到某副本追平水位 |
| `{ kind: 'switch' }` | 立即在候选节点间切换；全部落后则抛 `NoFreshReplicaError` |
| `{ kind: 'timeout', timeoutMs }` | 等待追平但带时限，超时抛 `ReadTimeoutError` |

## 用法

```ts
const clock = new VirtualClock();
const cluster = new Cluster(clock);
cluster.addPartition('p1', {
  primaryLatencyMs: 5,
  replicas: [{ id: 'r1', latencyMs: 2, replicationLagMs: 100 }],
});
const router = new SessionRouter(cluster, clock);

const session = new Session();
await router.write(session, 'p1', 'k', 'v1');        // 写主库，版本记入会话
const res = await router.read(session, 'p1', 'k', { kind: 'wait' });
// res.version >= session 此前水位；res.version 已记录回会话

const token = session.toToken();                      // 跨请求传递
const restored = Session.fromToken(token);
const merged = Session.merge(session, restored);      // 各分区取最大水位
```

## 测试

```sh
npm test   # node --test（Node >= 23.6 原生运行 TypeScript）
```

测试覆盖：wait/switch/timeout 三种策略、多分区交错读写的单调读不变量（每次返回版本不低于该分区此前返回值，且值与版本一一对应）、会话合并取最大水位、token 往返、水位按分区隔离。

注：本环境网络受限未安装 `tsc`，代码仅使用可擦除类型语法（无 enum / 参数属性），由 Node 23 类型擦除直接运行；`tsconfig.json` 供编辑器与 CI 类型检查使用。
