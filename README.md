# judgment-diff-reducer

给定失败输入与判定差异（两个策略版本对同一输入判定不一致），在**保持差异存在**的前提下缩减无关属性、有限集合元素与整数值。TypeScript 实现，零依赖，使用 Node.js 内置测试运行器验证。

## 设计

- **策略/条件 DSL**（`src/condition.ts`, `src/policy.ts`）：策略只读输入的显式属性，条件原子仅限整数比较、有限集合成员、布尔标志，组合子为 and/or/not；无任何外部身份服务。判定差异 = 两个策略版本对同一输入给出不同标签（`differentialOracle`）。
- **Schema 约束与字段依赖**（`src/schema.ts`）：字段可声明 `required` 或 `presentWhen`（依赖条件成立时必须出现，否则必须缺席），外加跨字段 invariant。缩减**优先保持输入合法**：非法候选在本地直接拒绝，不消耗 oracle 预算；合法候选才检查判定差异是否保持。
- **分层 delta 调试 + 整数边界搜索**（`src/reducer.ts`）：每轮依次为
  1. 属性层 ddmin（含单元素耗尽阶段，保证对单删除 1-minimal）；
  2. 集合层 ddmin（尊重 `minSize`）；
  3. 整数边界搜索（先试 schema 最小值，失败则在 min 与当前值之间二分翻转边界）。
  循环至不动点。删必要属性会让差异消失（或破坏依赖约束）则被拒绝，属性得以保留。
- **预算与证据**：每次 oracle 调用消耗 1 点预算；每个被接受的缩减步骤都记录 before/after、已用/剩余预算（同时写入 journal）。
- **Journal 与恢复**（`src/journal.ts`）：JSONL 追加日志，记录 `run-start`（含规则版本与预算）、每条 oracle 结果（缓存）、每步证据、每步后的最优候选检查点、最终状态。`resume: true` 重放日志：恢复缓存（**按规则版本隔离**，版本变更则缓存失效）、已耗预算与最优候选——恢复不丢候选。
- **1-minimal 证书**：不动点后逐个尝试声明的基本缩减操作（`remove-attr` / `remove-set-element` / `dec-int`），全部无法保持差异才给出 `minimal` 证书；**预算耗尽只称 `local-minimum`**，证书标记为不完整。最小化幂等：对结果再次缩减无任何步骤、结果不变。

## 运行

需要 Node.js ≥ 22.6（23.6+ 原生支持类型剥离；仓库含 `.nvmrc` 指向 23.9.0）。

```sh
npm test      # node --test（内置测试运行器）
npm run sample
```

## 运行样例（`npm run sample` 摘要）

场景：v1 允许 clearance ≥ 5 的成年 admin，v2 收紧为年龄 ≥ 21 且 clearance ≥ 7。
失败输入 `{age:35, roles:[admin,editor,viewer], mfa:true, region:'us', clearance:6}`，v1 判 `allow`、v2 判 `review`。

```
step 1 [remove-attrs] ddmin removed attributes: mfa, region        budget: used=3  left=497
step 2 [remove-set-elements] ddmin roles: kept [admin]             budget: used=5  left=495
step 3 [int-boundary] age: 35 -> 18 (flip boundary)                budget: used=11 left=489
step 4 [int-boundary] clearance: 6 -> 5 (flip boundary)            budget: used=15 left=485

reduced candidate: { "age": 18, "roles": ["admin"], "clearance": 5 }
status: minimal (fixpoint reached and every declared basic op fails to preserve the difference)
oracle calls: 22/500
1-minimal certificate: dec-int age -> 17: same; dec-int clearance -> 4: same
```

注意 `clearance` 因依赖 `roles` 含 `admin` 而不可删除，`roles`/`age` 为 required——依赖与必要属性均被保留。

预算耗尽（budget=6）：

```
candidate: { "age": 35, "roles": ["admin"], "clearance": 6 }
status: local-minimum (oracle budget exhausted; only a local minimum is claimed)
```

从该 journal 恢复（`resume: true`，budget=500）：检查点候选不丢失，复用缓存，收敛到同一最小结果 `{age:18, roles:['admin'], clearance:5}`，累计 oracle 调用 22/500。

## 测试结果（`npm test`）

```
✔ reduces attributes, set elements and integers to the 1-minimal candidate
✔ certificate: every declared basic op fails to preserve the difference
✔ dependent and necessary attributes are kept (deleting them kills validity or the diff)
✔ validity first: the oracle never sees a schema-invalid candidate
✔ minimization is idempotent
✔ budget exhaustion claims only a local minimum, never minimal
✔ journal records oracle results, rule version, steps and best checkpoints
✔ resume never loses the checkpointed candidate and converges to the same minimum
✔ cached oracle results are keyed by rule version
✔ initial input must be valid and exhibit the difference
✔ optional attribute that is necessary for the diff is kept

ℹ tests 11  ℹ pass 11  ℹ fail 0
```
