# 合并验收包（Merge Acceptance）

面向 TypeScript 配置宿主的**可导入、失败闭合（fail-closed）**多供应商配置合并验收器。
纯函数、零运行时依赖，可直接被 Node 22.6+ 的类型擦除执行，也可由打包器 / `tsc` 消费。

```ts
import { acceptMerge, publishAccepted, doc, obj, leaf } from 'ps-00e3a4139a';

const result = acceptMerge({ branches, signatures, grants, /* ... */ });
if (!result.ok) {
  // result.errors: 确定性排序、带 JSON Pointer / nodeId / facet 的位置
  throw result.errors;           // 不发布任何内容
}
await host.publish(result);       // result.tree + 每叶四项溯源
```

或使用宿主导出：

```ts
const result = await publishAccepted(host, input); // 验收失败时 host 绝不会被调用
```

运行自测：`npm test`（Node 内置 test runner，原生擦除 TS 类型）。
真值演示：`npm run demo`。

## 输入模型

- `documents`：签名所锚定的**原始树**，只用于确定签名范围与原始位置，不参与合并/发布。
- `branches`：带 `priority`（越大越高）、可选 `when.labels` 条件的已解析有限树。
- `templates`：共享模板树，由 `kind:'ref'` 节点引用，可在不同 `tenant` / `role` 下展开。
- `aliases`：键别名/改名规则，带签发者 `attribution` 与生效父位置 `at`。
- `signatures`：`{ sigId, issuer, docId, nodeId }`，范围 = 原树中该节点 + **原有后代**的闭包。
- `revocations`：被撤销的 `sigId`（也可用签名内联 `revoked:true`）。
- `grants`：`{ issuer, path, facets, tenant?, role? }`，`path` 支持 `/a/b`、`/a/*`、`/a/**`，
  `facets` 为 `'name' | 'value' | 'parent' | 'existence'` 或 `'*'`，`issuer:'*'` 为开放授权（仅测试用）。
- `protectedKeys`：`{ path, owner, allowDeleteFrom? }`，跨作用域墓碑保护。
- `deletions`：定向**撤回**某条署名贡献（区别于阻断继承的墓碑），用于“删除后再继承/暴露”。

小树可用 `doc/obj/leaf/nil/tomb/ref` DSL 构造，节点用 `{ id, issuer, sig }` 固定不可变 ID 与归属。

## 三个关键区分

1. **签名范围 ≠ 输出位置授权**。签名只证明原树子树；改名、移动、别名折叠、模板展开后，
   每个输出叶的每个面仍需“该位置 + 该用途”的 `grant`。
2. **`null` / 缺失 / 墓碑**：
   - 显式 `null`：键存在且值为 `null`，是一个值决定；
   - 缺失（无该意见）：透明，落到更低优先级分支；
   - 墓碑：高优先级决定性删除标记，阻断下层继承，并受跨作用域保护约束。
3. **墓碑 vs 撤回（`deletions`）**：墓碑阻断继承；定向撤回只移除某条署名覆盖，
   重算后可能**暴露另一分支值**，此时会记录 `ExposureRecord` 并复核暴露来源与签名撤销状态。

## 每叶四项溯源

`result.provenance[i]` 对每个输出叶给出四个 `FacetDecision`：

| 字段 | 决定内容 | 典型来源 |
| --- | --- | --- |
| `name` | 输出键名 | 别名/改名规则签发者，或原边 |
| `valueDecision` | 叶值 | 胜出节点 / 模板内容签名 |
| `parent` | 父子挂载关系 | 放置/移动/模板引用的部署签发者 |
| `existence` | 存在性 | 最终存在意见（删除链上的决定也单独授权） |

每个决定都包含 `issuer`、`via`（`moved`/`renamed`/`alias`/`template`/`delete`…）、
`evidence[]`（签名范围校验）与 `authorized` / `violations`。四项全部授权，叶才授权；
任一面失败 → 整个发布 `ok:false`，零发布。

## 确定性错误位置

错误按 `pointer | facet | nodeId | code` 稳定排序去重。主要错误码：

- `TEMPLATE_CYCLE`（`detail.chain` 给出确定环链，位置为闭合环的引用节点）
- `ALIAS_COLLISION`（位置为父对象 pointer + `facet:'name'`，`detail.sources` 排序列出冲突源键）
- `UNAUTHORIZED_FACET` / `SIGNATURE_SCOPE` / `SIGNATURE_REVOKED` / `SIGNATURE_NOT_FOUND`
- `CROSS_SCOPE_TOMBSTONE` / `PROTECTED_KEY`
- `UNKNOWN_TEMPLATE` / `UNKNOWN_NODE` / `NODE_ID_CONTENT_CONFLICT` / `INVALID_INPUT`

## 需求 → 真值场景 / 测试

`src/truth/scenarios.ts` 内置 13 个规范真值场景（`CANONICAL_SCENARIOS`），
由 `test/scenarios.test.ts` 逐个执行；`src/truth/oracle.ts` 是独立的扁平真值解释器，
`test/truth-oracle.test.ts` 穷举 4³=64 种“值/空/墓碑/缺失”组合交叉验证。

| 题目要求的小树验证 | 场景 id |
| --- | --- |
| 签名子树被搬动 | `moved-subtree-rejects-new-parent`、`moved-subtree-allowed-location`、`moved-subtree-new-child-not-covered` |
| 同名键在不同父节点 | `same-name-different-parents`（另见 `test/provenance.test.ts`） |
| 跨作用域墓碑 | `cross-scope-tombstone-blocked`、`same-scope-tombstone-allowed`（及 `test/multivendor.test.ts` 高优先级/委派对照） |
| 删除再继承 / 暴露 + 撤销 | `delete-then-reinherit`、`delete-exposes-revoked-source` |
| 共享模板跨租户/多角色 | `shared-template-cross-tenant-isolated`、`shared-template-cross-tenant-allowed`、`template-recursion-cycle-located` |
| 空值覆盖（三态对照） | `null-override-three-states` |
| 别名碰撞 | `alias-collision-located` |
| 失败零发布 | `test/publish.test.ts`（host spy 断言 0 次调用） |

## 公开入口

- `acceptMerge(input)` → `Publication | Rejection`（纯函数，不抛策略错误）
- `publishAccepted(host, input)` → 验收通过才 `host.publish(publication)`
- 别名：`acceptConfigMerge` / `verifyMerge` / `evaluateMerge` / `publishMerge`
- DSL：`doc` `docFromPaths` `branch` `obj` `leaf` `nil` `tomb` `ref` `del`
- 高级/白盒：`Authority`、`MergeEngine`、`truthOracle`、`exhaustiveStateTruth`、`CANONICAL_SCENARIOS`

导入方式：包名 `ps-00e3a4139a`、`./src/index.ts`、NodeNext 风格 `./src/index.js` 垫片、
或仓库根 `index.ts` 均可。
