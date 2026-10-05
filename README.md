# role-grant-solver

角色授予求解器：从各角色的权限集合中选出授予组合，使用户所需权限被完全
覆盖，且不引入任何指定敏感权限。

## 目标（按字典序）

1. **角色数最少**；
2. **多余权限最少**（授予但未被要求的权限）。

组合最多包含 25 个角色（可通过 `maxRoles` 调整）。

## 用法

```ts
import { solveGrant } from "./src/solver";

const result = solveGrant({
  roles: [
    { name: "base", permissions: ["read"] },
    { name: "editor", permissions: ["write"], inherits: ["base"] },
    { name: "superuser", permissions: ["read", "write", "danger"] },
  ],
  required: ["read", "write"],
  sensitive: ["danger"],
});

// result.status === "ok"  -> { roles: ["editor"], granted, excess }
// result.status === "unsat" -> { missingPermissions, forcedSensitive, maxRolesExceeded? }
```

- **角色继承**：`inherits` 传递闭包展开为有效权限；成环或引用未知角色抛错。
- **无解见证**：
  - `missingPermissions` — 任何角色都无法授予的所需权限；
  - `forcedSensitive` — 每个所需权限若只能由携带敏感权限的角色授予，
    给出该权限、涉及的敏感权限与全部相关角色；
  - `maxRolesExceeded` — 存在覆盖方案但超出角色数上限。

## 算法（精确，非贪心）

1. 继承闭包解析（记忆化 DFS，检测环）。
2. 预筛选：含敏感权限的角色禁用；不贡献所需权限的角色不可能出现在
   最优解中；有效权限集合完全相同的角色只保留一个代表。
3. 对角色数 k = 1..maxRoles **迭代加深**，第一个有解的 k 即最少角色数。
4. 固定 k 时用**分支限界**枚举全部覆盖组合，取多余权限最小者：
   - 后缀并集可行性剪枝（剩余角色能否补齐缺口）；
   - 多余权限下界剪枝（已不优于当前最优即剪，只剪被支配分支）。
5. 因此结果为字典序 `(角色数, 多余权限)` 的全局最优解，而非贪心近似。

## 构建与测试

```sh
npm run build   # 需要 typescript（tsc）
npm test        # 编译并运行 test/solver.test.ts
```

测试包含与穷举基准对拍的随机用例，验证最优性。
