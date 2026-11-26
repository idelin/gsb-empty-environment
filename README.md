# Modular Ring Repair Admission

可导入的 TypeScript 模环分片修复准入组件，运行于 Node.js 23 的原生 type-stripping 测试环境。它针对 `R = Z/(p^e Z)` 处理零因子，不使用普通域秩作为唯一性或隐私判据。

## 快速使用

```ts
import {
  ModRing,
  admitOrThrow,
  generateDomainKeyPair,
  signRepairStep,
} from './index.ts';

const ring = new ModRing({ p: 2, e: 2 }); // Z/4Z
const helper = generateDomainKeyPair('helper');

// 全局变量坐标 = [secret_0, secret_1, mask_0]
const maskedShare = signRepairStep(ring, {
  id: 'm0',
  generation: 7,
  round: 0,
  sender: 'helper',
  receiver: 'target',
  localInputIndex: 0,
  coefficients: [1, 0, 1], // secret_0 + mask_0
}, helper.privateKeyPem, helper.keyId, helper.publicKeyPem);

const opening = signRepairStep(ring, {
  id: 'm1',
  generation: 7,
  round: 1,
  sender: 'helper',
  receiver: 'target',
  localInputIndex: 1,
  coefficients: [0, 0, 1], // mask_0
  dependsOn: ['m0'],
}, helper.privateKeyPem, helper.keyId, helper.publicKeyPem);

admitOrThrow({
  ring,
  secretCount: 2,
  masks: [{ id: 'mask_0', generation: 7, owner: 'helper' }],
  domains: ['helper', 'target'],
  shareMatrix: [
    [1, 0],
    [1, 0], // 缺失 target 分片对应的生成矩阵行
  ],
  target: 'target',
  steps: [maskedShare, opening],
  publicKeys: { helper: helper.publicKeyPem },
  coalitions: [{ label: 'helper', domains: ['helper'] }],
  protectedForms: 'all-secret-forms',
});
```

## 数学判据

设 `q = p^e`，线性观测矩阵为 `B`，受保护线性式为行向量 `L`。

- Smith 标准型中的非零不变因子可写为 `p^{a_i}`，其中 `0 <= a_i < e`。
- 若矩阵有 `n` 列、`r` 个非零不变因子，则 `|ker B| = p^(sum a_i + e(n-r))`。
- `L` 在给定 `Bx = b` 后的条件支持基数为 `|ker B| / |ker [B; L]|`。
- 目标分片唯一可重构当且仅当该基数为 `1`。
- 对串通集合，比较修复前矩阵 `B_before` 与追加全部可见修复消息后的 `B_after`。隐私准入要求每个指定受保护线性量都满足 `support_after >= support_before`；线性观测只会缩小支持集，因此安全情形实际为相等。

高阶零因子 `p^(e-1)` 会在模 `p` 域秩中消失，但 Smith 基数仍能捕获其只泄漏模 `p` 剩余的问题：输出像集基数是 `p`，单个观测对应的候选秘密数是 `p^(e-1)`。

## 签名承诺与作弊检测

- `signRepairStep` 使用 Ed25519 对规范化 JSON 载荷签名，载荷绑定 `id`、代次、轮次、发送域、接收域、`localInputIndex`、系数、可选消息值、依赖和 `commitmentId`。
- `signLinearCommitment` 对独立线性承诺签名。
- 相同 `commitmentId` 出现不同系数或不同开口值时，`admitRepairPlan` 返回 `equivocating-linear-commitment` 错误，并在 `equivocations` 中保留两份签名证据。
- 设置 `slotConsistency: 'sender-round-input'` 可在没有显式 `commitmentId` 时，按“代次 + 发送域 + 轮次 + 局部输入索引”检查跨接收者矛盾。
- 若不同系数在给定 `admissibleInputMap` 上诱导相同函数，组件将其识别为模环等价表示；可通过 `rejectEquivalentRingRepresentations` 改为严格拒绝。

宿主也可传入自定义同步 `verifier({ domain, payload, envelope })` 对接现有 PKI/HSM。

## 掩码策略

- 掩码坐标位于秘密坐标之后；`masks` 也可直接传掩码数量或 ID 字符串数组。
- 同一掩码 ID 首次绑定的代次不能在后续步骤中改变，否则返回 `cross-generation-mask-replay`。
- `findMaskCancellationWitnesses` 求消息行向量关于掩码列的左模核，输出能消去全部掩码的行组合。
- 默认 `zeroSumPolicy: 'leak-only'`：若零和消去导致任一受保护线性量支持收缩，返回 `zero-sum-mask-cancellation`。
- 设置 `zeroSumPolicy: 'forbid-all-derived'` 会拒绝串通视图中任何非平凡的无掩码线性导出。

串通域默认知道 `owner` 属于该域的掩码；可用 `coalitionsKnowOwnedMasks: false` 关闭，或在 coalition 中用 `knownMaskIds` 显式指定。缺失目标域默认不会把其 `shareMatrix` 行当作修复前已知分片；可用 `missingDomains` 覆盖。

## 穷举核对

`exhaustiveConditionalImageSize` 不使用 Smith 公式，而是枚举全部变量赋值并按真实观测值分组，返回每组受保护值集合的最小/最大基数以及与公式是否一致。

测试覆盖：

- `Z/4Z`、`Z/9Z`、`Z/8Z` 上全部 `2×2` 矩阵的模核基数与生成元；
- 单线性式和完整修复视图的全秘密、全掩码赋值；
- `p^(e-1)` 非单位乘子的部分模 `p` 泄漏；
- 签名矛盾组合、篡改签名、跨代掩码重放、零和对消和不一致签名值。

```bash
npm test
```

## 主要导出

- 环与 Smith：`ModRing`, `fieldRankModP`, `smithNormalForm`, `isConsistentSystem`
- 条件基数与穷举：`conditionalImageSize`, `conditionalSupportSize`, `exhaustiveConditionalImageSize`
- 准入：`admitRepairPlan`, `admitOrThrow`, `assertAdmitted`, `RepairAdmissionError`
- 门面：`ModularRepairAdmission`, `admitModularRepair`, `serializeAdmissionResult`
- 签名与承诺：`generateDomainKeyPair`, `signRepairStep`, `signLinearCommitment`, `verifyCanonical`, `detectEquivocation`
- 掩码：`MaskGenerationLedger`, `findMaskCancellationWitnesses`
- 构造工具：`createLinearForm`, `evaluateForm`, `evaluateShareMatrix`
