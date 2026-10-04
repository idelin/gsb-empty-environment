# role-grant-solver

角色授予求解器：从角色目录中选择授予组合，覆盖用户所需权限、避开敏感权限。

## 目标（字典序）

1. **角色数最少** —— 授予组合包含的角色数量最小；
2. **多余权限最少** —— 在角色数相同的前提下，授予的超出所需权限的数量最小。

求解采用**精确分支限界搜索**（枚举并剪枝所有组合），不使用贪心覆盖，
因此结果保证最优。目录规模限制为 **25 个角色**，授予组合默认同样不超过
25 个角色（可用 `maxRoles` 调整）。

## 规则

- 角色可通过 `inherits` 继承其他角色，权限取传递闭包；继承环按环上权限
  的并集解析，并以 `warnings` 报告。
- 任何（有效权限中）携带敏感权限的角色都不会被授予。
- 每个所需权限若无法被满足，返回见证（witness）：
  - `missing`：没有任何角色能授予该权限；
  - `sensitive-conflict`：该权限本身被同时声明为所需且敏感；
  - `forced-sensitive`：能授予它的每个角色都附带敏感权限，`blockedBy`
    列出每个角色及其敏感权限作为证据。
- 若最优组合超过 `maxRoles`，返回 `role-limit-exceeded` 及真实最优解作为证据。

## 用法

```ts
import { solveGrant } from './src/index.ts';

const result = solveGrant({
  roles: [
    { name: 'Base', permissions: ['login'] },
    { name: 'Admin', permissions: ['read', 'write'], inherits: ['Base'] },
    { name: 'Viewer', permissions: ['read'] },
  ],
  required: ['read', 'login'],
  sensitive: ['write'],
});
// => { ok: true, roles: ['Base', 'Viewer'], roleCount: 2, ... }
```

命令行（JSON 从文件或标准输入读取）：

```sh
npm run solve -- request.json
cat request.json | npm run solve
```

## 运行与测试

无需安装依赖：Node.js >= 23.6 原生支持 TypeScript 类型擦除，直接运行 `.ts`。

```sh
npm test        # node:test 测试套件
npm run typecheck  # 需要本机安装 typescript（可选）
```

## 输入/输出类型

见 `src/types.ts`：`GrantRequest` 为输入，`GrantResult` 为判别联合
（`ok: true` 成功 / `uncoverable` / `role-limit-exceeded` / `invalid`）。
