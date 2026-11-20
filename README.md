# 销毁证书检查包

这是一个无依赖、可直接导入的 TypeScript 包，用来判断租户历史密钥在删除哪些存储对象后会变得不可恢复，并独立校验销毁证书。

```ts
import {
  planDestruction,
  verifyDestructionCertificate,
  type KeySnapshot,
} from "ddd-first-pass-erasure";
```

## 快照模型

`KeySnapshot` 是不可变密钥谱系：

- `objects`：至多 16 个可销毁存储对象，删除单位只能是对象 ID。
- `nodes`：至多 64 个派生节点。
- `targetNodeId`：目标密钥节点。
- `mustRemainRecoverable`：删除后仍必须可恢复的其他租户密钥节点。
- `exportedCopies`：已经离开可销毁存储范围的副本；副本即使在删除前被撤销，仍然是可用恢复事实。
- `revokedCredentials`：仅记录撤销事实，不参与可恢复性判定；撤销不会擦除密钥材料，也不会删除已导出副本。

节点规则：

- `material`：已知输入。没有 `objectId` 时是不可删除的固有输入；有 `objectId` 时随对象删除而消失。
- `stored`：必须依赖一个存储对象。
- `derived`：所有 `inputs` 都可恢复时才可恢复。
- `sealed`：必须同时恢复 `backup` 和全部 `wrappingKeys`；支持任意层数的封装备份和包装密钥。
- `threshold`：从同一轮次中选出 `required` 个分片；分片索引必须不同，且同一物理存储对象只计一次，重复别名不能凑数。
- `alias`：依赖 `source`，并受自身 `objectId` 是否存在约束。
- `export`：不可删除的已导出事实。

恢复采用最小不动点：从已知输入和导出副本出发反复应用规则，直到不再增加节点。没有任何已知输入或导出事实的循环不会自行恢复。

例如，一个需要“密封备份 + 包装密钥”两个必备条件的根密钥可以写成：

```ts
const snapshot: KeySnapshot = {
  targetNodeId: "root",
  objects: [{ id: "backup-vault" }, { id: "wrapper-vault" }],
  nodes: [
    { id: "backup", kind: "stored", objectId: "backup-vault" },
    { id: "wrapper", kind: "material", objectId: "wrapper-vault" },
    { id: "root", kind: "sealed", backup: "backup", wrappingKeys: ["wrapper"] },
  ],
};
```

## 规划最小删除集合

```ts
const plan = planDestruction(snapshot);

if (plan.status === "planned") {
  console.log(plan.deletedObjectIds);
  // 基数最小；同基数时按对象 ID 字典序最小。
}
```

`planDestruction` 的返回状态：

- `planned`：找到删除集合；`targetWitnessBefore` 给出删除前目标恢复证明，`mandatoryWitnesses` 给出所有保全密钥的恢复证明。
- `already-unrecoverable`：目标本来就在最小不动点中不可恢复；最小集合是空集。
- `impossible`：
  - `exported-copy`：目标仍可由已导出副本恢复；
  - `preservation-constraint`：任何能销毁目标的集合都会破坏保全密钥；
  - `intrinsic-recovery`：目标依赖不可删除材料，删光所有对象仍可恢复。
- `invalid-snapshot`：输入超过 16 个对象、64 个节点，或引用不一致。

便捷函数 `findMinimumDestructionSet(snapshot)` 直接返回对象 ID 数组；无解时返回 `null`，目标本来不可恢复时返回空数组。需要区分这些状态时使用 `planDestruction`。

## 独立校验销毁证书

```ts
const verification = verifyDestructionCertificate(snapshot, {
  targetNodeId: snapshot.targetNodeId,
  deletedObjectIds: ["object-a", "object-b"],
});
```

校验器不信任规划结果，会自行：

1. 在删除后重算最小不动点。
2. 检查目标确实不可恢复，并检查所有保全密钥仍可恢复。
3. 对每个被删对象逐一还原，要求目标重新可恢复，并返回 `targetWitness`。
4. 检查集合是否基数最小，并检查同基数集合中是否字典序更小。

常用拒绝码：

- `insufficient-deletion`：删除后目标仍可恢复。
- `exported-copy`：目标由已导出的副本继续可恢复。
- `preservation-constraint`：删除了其他租户仍需的共享包装密钥等对象。
- `not-minimal`：删除集合包含多余对象。
- `not-lexicographically-minimal`：集合同样最小，但不是字典序最小的那个。
- `target-already-unrecoverable`：目标删除前已经不可恢复，非空证书不是最小证书。
- `invalid-certificate` / `invalid-snapshot`：输入结构或引用无效。

## 语义边界

- 阈值分片刻意要求同一 `round`，且每个选中分片具有不同的索引和不同物理对象。多个分片条目通过相同 `objectId` 指向同一物理对象时就是别名，只计一次；把不同轮次的分片拼在一起也不能满足阈值。
- 删除只能针对 `objects`，不能删除导出副本、固有已知输入或撤销凭据。
- 撤销凭据只影响未来读取策略，不被当作文档销毁证据；已导出的副本仍可作为恢复事实。
- 共享包装密钥被切断时，规划器会优先寻找不伤害保全密钥的替代删除集合；不存在时返回 `preservation-constraint`。
- 规划复杂度受 16 个对象上限约束，最坏会检查 `2^16` 个子集；每个子集重算一次 64 节点内的最小不动点。

## 测试

```bash
npm test
```

测试包含一个小图穷举参考实现，对随机生成的多层包装、共享子密钥、阈值门、别名和循环图逐子集对照恢复结果；另有跨轮次分片误拼、别名重复、无根环、共享包装密钥误伤其他租户、导出副本和证书还原见证的定向用例。
