# Exact Privacy Release Gate

可导入的 TypeScript 隐私宿主组件，用于在浏览器单会话中核算串通站点的联合观测。所有概率、先验、后验和似然比均使用 `bigint` 归一化有理数；放行比较不经过 `number` 或浮点。

```bash
npm test
```

测试使用 Node 23 的 TypeScript type-stripping 和内置 test runner，无第三方运行时依赖。

## 核心保证

- 模块必须有签名，且 `policyGeneration` 与当前策略代次相等；未配置签名验证器时默认全部拒绝。
- 后验严格超过 `3 * prior(s)` 才违规；恰好等于三倍放行。
- 受保护等价类秘密对的有序似然比严格大于 `R` 才违规；恰好等于 `R` 放行。
- 解析到同一 `resourceId` 的候选编号先合并概率桶，观察者看不到具体候选编号。
- 跨观察者合并按 `eventId` 去重。
- 相同 `sampleId` 的多个投影只计一次抽样，并通过联合原像表求精确联合概率，不复用边际概率相乘。
- 支持空候选 `''` / `empty: true` 表示条件退避产生的空观测。
- 动态合组先构造候选状态和完整报告；拒绝时返回同一个冻结的 `FIXED_REJECTION`，不写组、不写扣费、不产生差异化错误码。
- `reviewAdaptiveStrategy` 按调度函数穷举小事件树的所有叶子轨迹。

## 快速接入

```ts
import {
  FIXED_REJECTION,
  PrivacySession,
  type PrivacyPolicyInput,
  type SignedWorkModule,
} from './src/index.ts';
import { createHmacSignatureVerifier, signModuleHmac } from './src/node-crypto.ts';

const policy: PrivacyPolicyInput = {
  secrets: ['s1', 's2'],
  priors: { s1: [1n, 10n], s2: [9n, 10n] },
  ratioCap: [2n, 1n],
  policyGeneration: 7n,
};

const unsigned = {
  id: 'binary-module',
  policyGeneration: 7n,
  resourceOf: { y1: 'yes', y2: 'yes', n: 'no' },
  tables: {
    s1: { y1: [1n, 3n], y2: [1n, 3n], n: [1n, 3n] },
    s2: { y1: [1n, 6n], y2: [1n, 6n], n: [2n, 3n] },
  },
};

const module: SignedWorkModule = {
  ...unsigned,
  signature: signModuleHmac({ ...unsigned, signature: '' }, process.env.MODULE_HMAC_SECRET!),
};

const session = new PrivacySession({
  policy,
  modules: [module],
  signatureVerifier: createHmacSignatureVerifier(process.env.MODULE_HMAC_SECRET!),
});

session.createGroup('observer-a');
const result = session.recordObservation('observer-a', {
  eventId: 'event-1',
  sampleId: 'sample-1',
  moduleId: 'binary-module',
  candidateId: 'y1',
});

if (result === FIXED_REJECTION) {
  // 对不可信调用方只能返回这个固定对象，不能透传 report.issues。
  throw new Error('release rejected');
}
```

浏览器生产环境应把 `SignatureVerifier` 接成非对称签名/WASM 验证器；`src/node-crypto.ts` 是 Node HMAC 参考适配器和测试工具。核心 `src/index.ts` 不依赖 Node 内置模块。

## 精确报告

受信任宿主可调用以下详细审计 API；这些结果不要直接返回给不可信站点：

- `evaluatePrivacy(input)`：评估一条或多条给定轨迹。
- `reviewAdaptiveStrategy(input)`：穷举自适应调度事件树。
- `session.reviewObservations(observations)`：重算任意联合轨迹。
- `session.reviewGroup(groupId)`：重算观察者组。
- `session.previewMerge(a, b)`：合组前审计，不修改状态。
- `session.diagnoseModule(module)`：查看签名、代次或概率表问题。

`PrivacyReport` 返回：

- `worstPosterior` 和 `worstPosteriorWitness`：绝对最大后验及其见证序列。
- `worstPosteriorMultiple` / `posteriorViolationWitness`：最大后验/先验倍数及三倍违规见证。
- `maxLikelihoodRatio` 和 `maxLrWitness`：最大有序似然比、秘密/等价类对和见证序列。
- `traces[].likelihood`、`posterior`、`posteriorMultiple`：每个秘密的精确 `Rational`。

若 `P(O|s2)=0` 且 `P(O|s1)>0`，`maxLikelihoodRatio` 是 `ExtendedRational.infinite()`；若两个似然都为零，该观测被标记为不可能/模型不一致并拒绝。

## 合组与计费

```ts
const denied = session.mergeGroups('observer-a', 'observer-b', {
  mergedId: 'joint-group',
  // 可选：要求整棵自适应事件树的所有叶子也合规。
  strategy,
});

if (denied === FIXED_REJECTION) {
  // session.snapshot() 与调用前深度相等。
}
```

`billingForObservations(events)` 先按 `eventId` 去重，再按 `sampleId` 合并投影。因此：

- 两个观察者携带相同 `eventId`：`eventCount = 1`。
- 两个不同 `eventId` 共享 `sampleId`：`eventCount = 2`，但 `sampleCount = 1`。

多投影联合表必须提供联合原子概率，例如：

```ts
const jointTables = [{
  id: 'correlated-draw',
  projections: ['projA', 'projB'],
  atoms: [
    {
      outputs: { projA: 'y', projB: 'y' },
      probability: { s1: [1n, 3n], s2: [1n, 6n] },
    },
    // ...其余联合原子，每个秘密下概率和必须为 1。
  ],
}];
```

观察记录通过相同 `sampleId` 关联；评估器会把候选编号 coarsen 到公开资源后求联合原像内所有原子的概率和，而不是把两个边际概率相乘。

## 测试对照

`test/privacy-gate.test.ts` 用同一个小事件树覆盖：

- 单事件恰好位于 `LR = R = 2` 边界，边际合规。
- 两个独立抽样的 `yes/yes` 使 `LR = 4 > 2`、后验倍数 `40/13 > 3`，联合超界。
- 自适应 `yes` 分支调度第二个模块，`no` 分支进入空退避；穷举得到 3 条叶子轨迹并定位违规见证。
- 动态合组被拒后，组、观察记录和计费快照完全不变。
- 相同 `eventId` 跨观察者去重。
- 相同 `sampleId` 的相关投影得到联合概率 `1/3` 与 `1/6`（LR 恰为 2），错误独立相乘会得到 4。
- 后验恰为 `3/10 = 3 * (1/10)`、LR 恰为 `27/7 = R` 时放行；`28/100` 的越界表被拒绝。

## 主要导出

- `PrivacySession` / `PrivacyGate` / `PrivacyReleaseGate` / `PrivacyHost`
- `createPrivacyGate`
- `mergeObserverGroups`
- `evaluatePrivacy`
- `releasePrivacy`
- `reviewAdaptiveStrategy`
- `releaseAdaptiveStrategy`
- `createEvaluationContext`
- `Rational` / `ExtendedRational`
- `FIXED_REJECTION`
- `billingForObservations`

根目录 `privacy-gate.ts` 和 `src/privacy-gate.ts` 均重导出 `src/index.ts`，方便按宿主仓库习惯引用。
