# 时态权限委托与继承解析器

带时态约束的 RBAC 权限委托内核（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 模型：双时态

每条授权/委托记录携带两个时间维度，均为半开区间 `[from, to)`：

- **业务时间 (valid time)**：权限在现实业务中生效的区间。
- **审计时间 (transaction time)**：系统“知悉”该事实的区间。

查询 `getEffectivePermissions(user, businessTime, auditTime)` 的语义：
“截至审计时间所掌握的信息来看，该用户在指定业务时间的实际有效权限集合”。

提前收回是**双时态更正**而非物理删除：旧版本在收回的知悉时刻关闭其审计区间，
同时写入业务截止被截短的新版本。因此同一业务时间点，在收回登记前后的查询结果不同（可审计、可回溯）。

委托链解析：受托人持有角色的前提是——委托有效 **且** 委托方在同一 `(业务时间, 审计时间)` 下实际持有该角色（递归解析，带委托环保护）。链上任意一环过期或被截断，下游全部失效。

## API

```ts
const k = new TemporalDelegationKernel();
k.defineRole('R', ['doc:read', 'doc:write']);   // 定义角色及权限
k.grantRole('A', 'R', 0, INF, 0);               // 直接授权 [validFrom, validTo), 知悉于 txFrom
k.delegate('A', 'B', 'R', 10, 50, 10);          // 委托 A->B, 业务 [10, 50), 知悉于 10
k.revokeDelegation('A', 'B', 'R', 25, 30);      // 知悉 30 登记：业务 25 起撤回
k.revokeRoleGrant('A', 'R', 25, 30);            // 直接授权的提前收回
k.hasRole('C', 'R', 28, 35);                    // 单角色判定
k.getEffectiveRoles('C', 28, 35);               // 有效角色集合
k.getEffectivePermissions('C', 28, 35);         // 有效权限集合
```

## 验收场景

A 委托 R 给 B `[10, 50)`；B 在 20 委托给 C `[20, 60)`；A 在知悉 30 登记“业务 25 撤回”。
知悉 35 查询 C 在业务 28 的权限 → **已失效**（链在业务 25 被截断）。
对照：知悉 25（撤回未登记）或业务 24（撤回生效点之前）查询 → 仍有效。

## 运行

要求 Node.js ≥ 23.6（原生类型擦除，直接运行 `.ts`，无需编译）。

```sh
npm test    # node --test test/*.test.ts
npm run demo
```
