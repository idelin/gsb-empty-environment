# 时态权限委托与继承解析器（Temporal RBAC Delegation Kernel）

带双时态约束的 RBAC 权限委托内核：角色授权与临时委托都带有明确的业务起止时间，
并且系统对事实的知悉时间（审计时间）独立记录。可查询指定用户在指定
**业务时间**与**审计时间**下的实际有效权限集合，正确处理多层委托链的
过期与提前收回。

## 运行要求

- Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译、零依赖）
- 推荐 Node >= 23（`node --test` 可直接发现 `.ts` 测试文件）

```bash
npm test    # 运行 node:test 内置测试
npm run demo # 运行验收场景示例
```

## 时态模型

每条事实（角色授权 / 委托）保存为一个版本，携带两个半开区间 `[from, to)`：

| 区间 | 含义 |
| --- | --- |
| `valid` | 业务时间（valid time）：事实在现实世界生效的期间 |
| `known` | 审计时间（transaction/knowledge time）：系统知悉该事实的期间 |

**撤回不改写历史**：`revokeDelegation` / `revokeGrant` 会把当前知悉版本在
撤回知悉时刻关闭（`known.to = knownAt`），并插入一条业务终点缩短为
`effectiveAt` 的新版本，自 `knownAt` 起可见。因此：

- 审计时间早于撤回知悉时刻的查询，仍看到撤回前的状态（as-of 查询）；
- 业务时间早于撤回生效时刻的查询，即使知悉时间很晚，权限依然有效。

## 继承解析

用户 `u` 在 `(businessTime, auditTime)` 持有角色 `R`，当且仅当：

1. 存在同时覆盖两个时间点的直接授权；或
2. 存在同时覆盖两个时间点的委托 `d -> u (R)`，且 `d` 递归地持有 `R`。

委托是否有效在**查询时**动态判定（委托方必须在该时刻确实持有角色），
因此上游委托过期或被撤回会自动使下游整链失效。解析带环检测，委托环
不会导致死循环。

## API

```ts
const kernel = new TemporalDelegationKernel();
kernel.registerRole({ id: "R", permissions: new Set(["doc:read"]) });
kernel.grantRole("A", "R", 0, Infinity, 0);        // 用户, 角色, 业务起, 业务止, 知悉时刻
kernel.delegate("A", "B", "R", 10, 50, 10);        // 委托方, 受托方, 角色, 业务起, 业务止, 知悉时刻
kernel.revokeDelegation("A", "B", "R", 25, 30);    // 委托方, 受托方, 角色, 业务生效时刻, 知悉时刻
kernel.hasRole("B", "R", 28, 35);                  // 用户, 角色, 业务时间, 审计时间
kernel.effectivePermissions("B", 28, 35);          // -> string[]
```

## 验收场景

A 委托角色 R 给 B（业务 [10, 50)）；B 在 20 又委托给 C（业务 [20, 60)）；
A 在知悉 30 提前于业务 25 撤回委托；知悉 35 查询 C 在业务 28 的权限——
判定已失效：

```
用户 C | 业务时间 28 | 知悉时间 25 -> 有效 { doc:read, doc:write }
用户 C | 业务时间 28 | 知悉时间 35 -> 已失效（无权限）   <-- 验收点
用户 C | 业务时间 24 | 知悉时间 35 -> 有效 { doc:read, doc:write }
用户 B | 业务时间 28 | 知悉时间 35 -> 已失效（无权限）
```

## 项目结构

- `src/types.ts` — 双时态区间与版本类型
- `src/kernel.ts` — 内核：授权、委托、撤回、双时态继承解析
- `test/kernel.test.ts` — `node:test` 测试（12 例，含验收场景）
- `examples/demo.ts` — 验收场景与多层过期演示
