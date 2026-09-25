# replayable-policy-evidence

规则摘要 + 属性快照 + 求值轨迹的离线重放验证。TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`），需要 Node ≥ 23.6（直接运行 TypeScript）。

## 能力

- **证据记录**：`recordEvidence(policy, snapshot)` 冻结规则摘要（规范化策略的 SHA-256）、
  属性快照深拷贝、完整求值轨迹（每个分支、每次实际读取、短路标记）与最终三值结论。
- **离线重放**：`verifyRecord(record)` 只使用记录内冻结的快照，与规则逐步锁步重放，
  校验每个分支与读取集合，而不是只重算最终布尔值；发现差异即定位首个分歧点（路径 + 原因）。
- **历史不可变**：实时属性后续更正不影响历史判定；快照缺项识别为 `missing-evidence`。
- **共享子证明压缩**：相同子规则只求值一次，后续出现压缩为 `ref` 节点，重放时校验引用目标。
- **部分密封**：敏感字段可独立密封为哈希占位；被密封阻断的结论标记为 `unverifiable`，
  不认可来源声称的具体布尔值（`sealed-claim-not-accepted`）。
- **策略受限**：只读显式属性，仅支持有限集合成员（`in`）、整数比较（`int`）与布尔运算
  （`and`/`or`/`not`），不访问任何外部身份服务。

## 运行

```sh
nvm use          # v23.9.0（或任何 >= 23.6 的 Node）
npm test         # node --test
npm run demo     # examples/demo.ts
```

## 重放结果

`verifyRecord` 返回三种状态：

- `verified`：轨迹每个分支、每次读取、短路顺序与最终结论全部与快照重算一致。
- `unverifiable`：结构一致，但结论被密封字段阻断；不采信记录中声称的结果。
- `rejected`：发现篡改或证据缺失，`divergence` 给出首个分歧的 `path` 与 `reason`，例如：
  - 篡改规则版本 → `$.policyDigest` / `policy-digest-mismatch`
  - 删除一个读取项 → `$.children[0].children[0].reads` / `read-mismatch`
  - 调换短路顺序 → `$.children[0].children[0].id` / `node-id-mismatch`
  - 同轨迹配不同规则摘要 → `policy-digest-mismatch`
  - 伪造共享子证明引用 → `shared-subproof-mismatch`
  - 快照缺项 → `missing-evidence`

## 运行样例（`npm run demo` 节选）

```
== 2. offline replay verification ==
{ status: 'verified', result: true }

== 3. real-time attributes change afterwards; history is unaffected ==
{ status: 'verified', result: true }

== 4. tamper: bump rule version ==
{ status: 'rejected',
  divergence: { path: '$.policyDigest', reason: 'policy-digest-mismatch', ... } }

== 7. seal the sensitive tier field: conclusion becomes unverifiable ==
{ status: 'unverifiable', result: 'unverifiable' }
```

## 测试结果（`npm test`）

```
✔ offline replay verification
ℹ tests 14
ℹ pass 14
ℹ fail 0
```

覆盖：真实记录验证与共享子证明压缩、短路标记、实时属性变更免疫、规则版本篡改、
异轨迹异摘要、删除读取项、省略读取字段、调换短路顺序、伪造子证明引用、
密封字段不可验证与声称不认可、独立密封、证据缺失、最终布尔值与读取集合篡改、
分支结果不诚实重算。

## 结构

- `src/canonical.ts` — 规范化序列化与 SHA-256 摘要
- `src/rule.ts` — 规则 AST、规范化、条件求值
- `src/snapshot.ts` — 属性快照与独立字段密封
- `src/evaluate.ts` — 求值器（轨迹、短路、共享子证明压缩）与证据记录
- `src/verify.ts` — 锁步重放验证器（首个分歧定位）
- `test/replay.test.ts` — `node:test` 测试
- `examples/demo.ts` — 运行样例
