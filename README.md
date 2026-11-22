# 离线租约回收判定包

这是一个给 TypeScript 授权宿主直接导入的判定包。它回答一个具体问题：

> 对每个符号资源区间，服务端从哪个时刻起可以安全地把同一资源交给新租户？

包内所有时间运算都是精确有理数，不经过浮点数。开区间和闭区间会保留到最终边界。

## 导入

Node 23+ 可直接运行源码：

```ts
import {
  analyzeAuthorizations,
  decideRegrant,
  leaseDigest,
} from "ddd-2008-20261122-v2";
```

仓库测试使用：

```sh
npm test
```

## 最小调用

```ts
import { decideRegrant, leaseDigest } from "./index.ts";

const parentDevice = { id: "device-a", generation: 1 };
const childDevice = { id: "device-b", generation: 1 };

const parent = {
  id: "lease-parent",
  device: parentDevice,
  resources: [{ prefix: "tenant/42" }],
  term: { start: 0, end: 10, endInclusive: false },
};

const child = {
  id: "lease-child",
  device: childDevice,
  resources: [{ prefix: "tenant/42" }],
  term: { start: 5, end: 8, endInclusive: false },
  parentId: parent.id,
  parentDigest: leaseDigest(parent),
};

const decision = decideRegrant({
  leases: [parent, child],
  returns: [
    {
      id: "return-parent",
      leaseId: parent.id,
      device: parentDevice,
      returnedAt: 6,
    },
  ],
  // 没有这个证明时，未知子租约仍可能活到父租约真实期限 10。
  completenessProofs: [
    {
      id: "complete-parent-subtree",
      leaseId: parent.id,
      device: parentDevice,
      at: 6,
      descendants: [child.id],
    },
  ],
  resource: { prefix: "tenant/42" },
  now: 7,
});
```

上面的 `safeFrom` 为 `>= 8`：父租约在 6 提前归还，但已知子租约仍有效到 8。没有完整性证明时，结果为 `>= 10`，因为签名的提前归还不能证明不存在未见子租约。

## 资源作用域

资源区间是前缀差集：

```ts
{ prefix: "tenant", excludes: ["tenant/private", "tenant/cache"] }
```

表示所有以 `tenant` 开头的资源，去掉两个排除前缀。子租约的实际作用域始终是：

```text
child.declaredResources ∩ parent.effectiveResources
```

后发子租约即使声明了更宽的父前缀，也不会扩大授权范围。分析会按所有租约、归还记录的资源边界切分成互斥符号单元，每个单元单独给出判定。

## 时钟模型

租约期限写在持有设备的本地时钟上：

```ts
clocks: [
  {
    device: { id: "device-a", generation: 1 },
    offset: { lo: -1, hi: 2 },       // local - server
    drift: { lo: "-1/100", hi: "1/100" }, // d(offset)/d(server)
  },
]
```

观测记录把某个服务端时刻与一个本地时间区间绑定：

```ts
observations: [
  {
    device: { id: "device-a", generation: 1 },
    serverTime: 100,
    deviceTime: { lo: "99/1", hi: "101/1" },
  },
]
```

实现会为同一设备的所有租约边界、归还时刻和观测端点建立一张差分约束图。任意两个本地时刻 `Li <= Lj` 必须满足：

```text
(Lj - Li) / (1 + driftHi)  <=  Tj - Ti  <=  (Lj - Li) / (1 + driftLo)
```

带绝对偏移上下界后，Bellman-Ford 同时求出每个边界的最早、最晚服务端时刻。观测之间必须存在同一套可行时钟；矛盾数据会抛 `ClockConstraintError`，不会用互相冲突的观测拼出伪见证。

同一 `id + generation` 才视为同一设备。`name` 只用于展示；重名设备互不共享时钟。新代次也是不同身份，旧代次租约不会被新代次归还记录提前终止。

## 归还与完整性

- `EarlyReturn.returnedAt` 是设备本地时间；归还后该租约不再有效。
- 支持部分资源归还。只有归还覆盖的符号资源单元会被缩短。
- 迟到归还不会把有效期延长，也不会把已经自然到期的租约“复活”。
- 永久离线设备没有归还记录，因此回收时刻由真实期限推导，绝不假定它已经过期。
- 没有有效的子树完整性证明时，提前归还只结束该租约自身；其未见子租约仍可能活到原始父期限。该风险会作为 `unseen-subtree` 阻碍链输出。
- 完整性证明必须覆盖该租约的全部已知后代，且证明时间不早于对应归还时间。

签名可以使用内置 Ed25519 校验器：

```ts
import { ed25519Verifier } from "./index.ts";

const verifier = ed25519Verifier({
  "device-a#1": publicKeyPem,
});

analyzeAuthorizations({
  // ...
  verifier,
  requireSignatures: true,
});
```

`leasePayload`、`earlyReturnPayload`、`completenessPayload` 是签名的规范负载；`leaseDigest` 绑定租约全部授权字段，包括父授权摘要。

## 输出

每个 `RegrantDecision` 包含：

- `status`
  - `reclaimed`：当前没有被认为可能有效的租约。
  - `definitely-valid`：至少有一条租约在所有可行设备时钟下都有效。
  - `possibly-valid`：只在部分可行时钟/未知子树下可能有效。
- `safeFrom`：最早安全重授边界。`inclusive=true` 表示该时刻本身安全；`false` 表示必须严格晚于该时刻。
- `lastPossibleUse`：最后一条阻碍租约可能有效的上界，保留开闭语义。
- `blockers`：阻碍回收的完整授权链（根到阻碍租约），含原因和每台设备的一套连贯时钟见证。
- `lowerBoundCertificate`：为什么不能更早回收。它包含达到最晚边界的阻碍链。
- `upperBoundCertificate`：为什么从该边界起可以回收。它列出所有可能阻碍及其结束依据。

`analysisToJSON(result)` 可直接序列化为可回读证书；所有有理数和边界都输出为字符串。

## 关键判定规则

若最后一条可能有效租约的本地期限为开区间，则 `safeFrom` 在该服务端上界处是闭的（`>= T`）。若期限包含端点，则 `safeFrom` 是开的（`> T`）。父租约提前归还不会截断子租约；但子租约仍受父租约原始真实期限约束。缺失父摘要时无法证明有限上界，判定会保守地保持永久阻碍。

## 模块

- `src/rational.ts`：精确有理数。
- `src/time.ts`：带开闭标志的时间边界和窗口。
- `src/resources.ts`：前缀差集交、差、切分和覆盖判定。
- `src/model.ts`：设备身份、租约、摘要、签名策略。
- `src/clock.ts`：偏移/漂移差分约束求解器。
- `src/analysis.ts`：资源切分、授权链、归还、完整性证明、上下界证书。
