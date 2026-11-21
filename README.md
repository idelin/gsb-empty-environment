# 迁移授权规划包

面向 TypeScript 宿主的零依赖包，用于把旧认证会话中的在途回复迁移到新消息通道。它同时提供：

- `planMigration`：求最小重新认证实例集合、帧/通道接续映射和必要的互斥组停止集合；
- `verifyMigration`：独立重算迁移安全性，并穷举检查是否存在更小的重新认证集合；
- `enumerateCompatibleHistories`：逐条枚举帧级真实归属，供小日志验收或调试。

包入口是 `src/index.ts`，也可从仓库根目录的 `index.ts` 导入。Node 23 可直接运行：

```bash
npm test
```

## 核心语义

### 实例、连接与重新认证

实例由稳定实例号标识，并带有主体（`principal`）和可选传输连接（`connection`）。

- 重新认证按实例进行，撤销该实例全部旧许可；
- 重新认证会同时使该实例持有的许可和它发出的旧转授失效；
- 仅共享同一传输连接的其他实例不受影响；
- 接续映射只能选择未被重新认证、且仍在候选域内的实例。

### 帧级歧义

旧回执可给出候选实例集合。一个帧内所有队列、消息和后续回复只能选同一个接收实例：

```ts
frame.domain === intersection(
  frame.candidates,
  channel.candidates,
  message.candidates,
  ack.candidates,
)
```

不能出现 `q1 -> 1, q2 -> 2` 这种同一帧内按队列分别挑身份的结果。若交集为空，该帧无法接续；在允许停止时，可停止通道或由互斥组传播停止。

### 发送、确认、转授与撤销

日志可以是结构化数组，也可以直接放在 `events` / `log` 中。事件类型支持：

- `send` / `enqueue` / `message` / `reply`；
- `ack` / `confirm` / `receipt`；
- `delegate` / `grant` / `authorize` / `permit`；
- `revoke` / `revocation` / `withdraw`；
- `frame` / `channel` / `queue` / `mutex-group`。

授权检查按日志位置进行。转授链要求每个授权人在转授发生时已经拥有相应能力。撤销按 `id + generation`（或实例 + 代次）只关闭一个代次；后续代次的授权仍然有效。重新认证则关闭该实例的全部旧代次。

### 私有与公开消息

- 私有消息只有在接收实例的主体直接匹配，或该实例在消息时刻拥有有效转授链时才安全。
- 公开消息不受接收主体限制，但被指定保全的已确认公开消息必须存在一条未停止的接续通道。
- 若没有显式给出 `protectedPublicMessages` / `preservePublicMessages` / `confirmedPublicMessages`，带确认信息的公开消息默认视为需要保全。

### 必需与停止

帧和通道默认必须继续。可用 `optional: true` 或 `required: false` 标为可停止。保护公开消息、或任一必需成员，会阻止停止。

互斥组默认是原子的：只要一个成员被停止，整个组都停止。若组内有必须继续的成员，则该迁移不可行，而不是只停一部分。

## 规划 API

```ts
import {
  planMigration,
  verifyMigration,
  assertMigrationPlan,
  NoFeasiblePlanError,
} from "./src/index.ts";

const problem = {
  instances: [
    { id: "1", principal: "alice", connection: "transport-A" },
    { id: "2", principal: "mallory", connection: "transport-A" },
  ],
  frames: [
    { id: "f1", channels: ["q1", "q2"], candidates: ["1", "2"] },
    { id: "f2", channels: ["q3"], candidates: ["1", "2"] },
  ],
  messages: [
    {
      id: "m1",
      frame: "f1",
      channel: "q1",
      visibility: "private",
      audience: ["alice"],
    },
    {
      id: "m2",
      frame: "f2",
      channel: "q3",
      visibility: "private",
      audience: ["alice"],
    },
  ],
} as const;

const plan = planMigration(problem);
// {
//   reauthenticate: ["2"],
//   continuations: { q1: "1", q2: "1", q3: "1" },
//   frameContinuations: { f1: "1", f2: "1" },
//   stoppedChannels: [],
//   stoppedFrames: [],
//   stoppedGroups: []
// }

const accepted = verifyMigration(problem, plan);
if (!accepted.ok) {
  console.error(accepted.errors, accepted.witnesses);
}
```

`planMigration(input, { maxInterruptible?: number })`

- 对可打断候选实例按基数从小到大搜索；
- 同基数按稳定实例号的自然顺序决胜（`i2` 在 `i10` 前）；
- 典型上限为 18 个可打断候选，最坏需要检查 `2^18` 个子集；
- 无法满足公开消息保全或私有消息安全时抛出 `NoFeasiblePlanError`。

## 验收 API

`verifyMigration(input, plan, options)` 不调用规划器求解逻辑，而是用独立实现重算：

- 计划结构、实例是否存在、是否可打断；
- 帧内所有未停止通道是否映射到同一实例；
- 映射是否属于候选域且未被重新认证；
- 每个帧的每个相容真实归属是否都满足私有消息授权；
- 指定公开消息是否仍有未停止接续；
- 原子互斥组是否整体停止或整体继续；
- 是否存在更小的重新认证集合；
- 同基数下是否存在稳定号更小的可行集合。

返回的 `VerificationResult` 关键字段：

```ts
{
  ok: boolean,
  valid: boolean,
  safe: boolean,
  preservesPublicMessages: boolean,
  optimal: boolean,  // 不存在更小基数的集合
  canonical: boolean, // 还满足稳定号决胜
  errors: string[],
  witnesses: {
    unsafeHistories: { frame, message, instance }[],
    smallerReset: string[] | null,
    lexicographicallySmallerReset: string[] | null,
  },
  reauthenticate: string[],
  continuations: Record<string, string>,
  frameContinuations: Record<string, string>,
  stoppedChannels: string[],
  stoppedFrames: string[],
  stoppedGroups: string[],
}
```

`assertMigrationPlan` 在 `ok === false` 时抛出 `MigrationVerificationError`。只传重新认证数组也可以验收；此时验收器会独立推导一套合法接续映射。

## 输入字段

常用字段：

| 字段 | 作用 |
| --- | --- |
| `instances` | 实例号、主体、传输连接、`interruptible` |
| `frames` | 候选集合、包含的通道、`required` / `optional` |
| `channels` / `queues` | 帧归属、独立候选集合、停止标记 |
| `messages` | 帧、通道、公开/私有、受众、日志位置、回执候选 |
| `grants` / `delegations` | 授权人、接收实例、能力/受众、代次、日志位置 |
| `revocations` / `revokes` | 授权 id 或接收实例、代次、日志位置 |
| `mutexGroups` / `groups` | 帧/通道成员、是否原子 |
| `events` / `log` | 签名事件日志；也可直接使用数组作为输入 |

## 测试覆盖

`test/planner.test.ts` 覆盖：

- 两个局部歧义共享同一坏接收者，一个重新认证同时解决；
- 同一帧不能按队列分别选择实例；
- 撤销只关闭一个代次，新代次继续有效；
- 同一传输连接上不同主体互不波及；
- 原子互斥组整体停止，以及保护公开消息阻止停止；
- 稳定实例号排序；
- 多余重新认证集合不被判为最优；
- 签名事件日志输入。

`test/exhaustive.test.ts` 对小日志穷举所有重新认证子集、所有真实帧归属和所有接续选择，并与规划器结果逐例比较。
