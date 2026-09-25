# judgment-diff-reducer

给定一个触发**判定差异**的输入（同一输入在规则版本 A / B 下判定不同），在保持差异存在的前提下，缩减无关属性、集合元素与整数值。

- 策略语言封闭：只读显式声明的属性，仅支持有限集合、整数条件与布尔运算，不接外部身份服务。
- 合法性优先：候选先过 schema 校验（免费），再过 oracle 判定差异（消耗预算）。
- 分层 delta 调试：属性移除（级联依赖字段）→ 集合元素 ddmin → 整数边界搜索。
- 每步输出证据（操作、结果、判定、规则版本、预算状态），并逐步写入 journal。
- 结束后逐个尝试声明的基本缩减操作，全部失败才给出 **1-minimal 证书**；预算耗尽只报 **local-minimum**。
- 恢复不丢候选：journal 保存候选、oracle 缓存、已耗预算与证据，`Reducer.resume` 续跑。
- 最小化幂等：对输出再次缩减是不动点。

## 运行

需要 Node.js >= 23.6（原生 TypeScript 类型擦除，无第三方依赖）。

```sh
npm run demo   # 运行缩减样例，写 artifacts/demo-journal.json
npm test       # node:test 内置测试
```

## 运行样例（节选）

初始输入 8 个属性，规则 `v2025-Q4` 判定 allow、`v2026-Q1` 判定 deny：

```
initial verdicts: {"a":true,"b":false}

step  phase         outcome                    op                                  budget
   6  attr-ddmin    accepted                   remove attributes {tags, userTier}  7/200
  16  attr-ddmin    accepted                   remove attributes {regions}         15/200
  26  int-boundary  accepted                   set requestCount = 50               25/200
  29  int-boundary  accepted                   set quotaOverride = 40              28/200
  48  certify       rejected-difference-lost   remove attribute betaOptIn          42/200
  ...

status:             1-minimal
reduced candidate:  {"requestCount":50,"betaOptIn":true,"quotaOverride":40}
valid per schema:   true
verdicts preserved: {"a":true,"b":false}

== 1-minimal certificate (5 basic ops, all rejected) ==
  - remove attribute betaOptIn  ->  rejected-difference-lost
  - remove attribute quotaOverride  ->  rejected-difference-lost
  - decrement quotaOverride by 1  ->  rejected-difference-lost
  - remove attribute requestCount  ->  rejected-difference-lost
  - decrement requestCount by 1  ->  rejected-difference-lost

idempotent re-run:  true (status 1-minimal)
```

`betaOptIn` / `quotaOverride` / `requestCount` 是必要属性：删除任一者差异即消失，故保留
（`quotaOverride` 依赖 `betaOptIn == true`，级联删除保证候选始终合法）。

## 测试结果

```
✔ reduces irrelevant attributes, set elements and integers
✔ candidate stays schema-valid and keeps the same judgment difference
✔ necessary attributes are kept: removing them kills the difference
✔ field dependency: quotaOverride requires betaOptIn == true
✔ schema constraint quotaOverride >= 10 is enforced during reduction
✔ 1-minimal certificate covers every declared basic op on the result
✔ minimization is idempotent
✔ budget exhaustion yields only a local minimum, never a certificate
✔ oracle cache: re-evaluating an input costs no extra budget
✔ journal is saved step by step and recovery never loses the candidate
✔ policy language only reads explicit attributes
✔ initial input without a difference is rejected
✔ invalid initial input is rejected
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

## 结构

- `src/model.ts` — 输入 / schema / 条件树 / 版本化规则类型
- `src/constraints.ts` — 条件求值与 schema 校验（有限集合、整数区间、字段依赖、全局约束）
- `src/oracle.ts` — 双版本判定 oracle：缓存、预算计数、可序列化
- `src/ops.ts` — 声明的基本缩减操作（删属性 / 删集合元素 / 整数减一）与级联删除
- `src/reducer.ts` — 分层 delta 调试 + 整数边界搜索 + 证据 / 预算 / journal / 1-minimal 证书
- `src/fixture.ts` — 演示场景（规则 v2025-Q4 vs v2026-Q1）
- `src/demo.ts` — 运行样例；`test/reducer.test.ts` — node:test 测试
