机密工作负载从旧芯片迁到新芯片时，两个宿主的证明都可能有效，但不能让两个实例同时持有新一代解封能力。本仓库实现供 TypeScript 密钥代理导入的迁移验收包。

## 使用

```ts
import {
  evaluateRelease,
  MigrationAcceptanceEngine,
  DurableMigrationStore,
} from "./src/index.ts";

const decision = evaluateRelease({
  workloadId: "wl-secret",
  generation: 7,
  events,
  policy: { digest: "policy-v1", ownerThreshold: 1 },
  closedDevices: ["chip-old"],
});

if (decision.status === "release") {
  // decision.targetKey / decision.targetChipId
}
```

也可以使用按位置传参、事件数组形式，或状态化引擎：

```ts
evaluateRelease("wl-secret", 7, events, { policy });
evaluateRelease(events, { workloadId: "wl-secret", generation: 7, policy });

const engine = new MigrationAcceptanceEngine({ policy });
engine.addAll(events);
const result = engine.release("wl-secret", 7);
```

## 判定语义

- 每份证明绑定芯片身份、工作负载持久 ID、实例启动计数、调试位、运行时公钥、nonce、TCB 版本向量和策略摘要。缺失绑定、调试开启、摘要不匹配、nonce 重放会拒绝。
- TCB 按偏序比较：数字分量必须逐项不降，组件集合默认必须一致，额外/缺失分量或不同 epoch 均视为不可比。较新的分量不能替代缺失或更旧的分量。
- 新证明本身不能证明旧实例已停止。释放需要新证明、新芯片移交接收、旧实例停用、旧公钥撤销和所有者/恢复确认各自成立。
- 停用、撤销、移交和确认可以任意顺序到达。每条签名记录只在其声明的动作下生效；动作交叉复用、载荷变化或签名无效会拒绝。
- 旧实例在停用后以更高启动计数复活，或同启动计数轮换临时公钥而未被新停用/撤销覆盖时，判定为等待。
- 同一工作负载同一代次存在两个完整迁移事务时只产生一个释放；确定性胜者之外的并发迁移被拒绝，并记录冲突事务。
- 证据按 `migrationId` 隔离。合并部分证据时，另一事务的移交/确认不会填补当前事务缺失的步骤。
- 恢复确认按“物理芯片 + 实际管理域”做最大二分匹配：同芯片多证书只计一次，同管理域多证书只计一次。
- `dependencies`/`minimalDependencies` 是释放所需的最小签名记录集合；`evidence` 包含当前事务考虑过的全部记录，`causalDependencies` 是因果闭包。
- 宿主输入的因果前缀必须闭合；缺失父事件会产生 `wait`，不会凭空补步骤。

## 持久化与崩溃

`DurableMigrationStore` 使用追加式 JSONL 日志和原子替换快照。以下边界均可注入一次性崩溃：

`before-append`、`after-journal-append`、`before-checkpoint`、`before-snapshot`、`after-snapshot-write`、`before-journal-truncate`、`after-journal-truncate`、`after-checkpoint`。

重新打开后从快照与日志合并事件并按 id 去重，尾部半条日志会被忽略。释放租约仅在判定成功后随快照持久化；若在租约持久化前崩溃，重放同一事件集合仍得到同一确定性目标。

## 对照与测试

`referenceEvaluate`/`recomputeFromEventSet` 从完整事件集合重算，`compareWithReference` 用于与状态化引擎对照。测试覆盖乱序到达、旧实例复活、临时公钥轮换、并发双迁移、TCB 不可比、动作绑定签名、部分证据合并、恢复门限去重和各持久化边界崩溃。

```bash
npm test
```
