# 组织权限差异工具（org-permission-diff）

比较用户迁入新组织树前后的权限差异。权限来自**祖先组织的授权继承**与**用户显式例外**（allow/deny），
工具对每名用户、每类资源、每个动作计算**新增 / 失去 / 不变**的权限，资源路径以压缩形式表示，
并为每条变化输出**最短来源链**。

## 核心语义

- **组织身份按 `id` 而非名称**：重命名组织（`id` 不变、`name` 变化）不会产生任何权限差异。
- **有效权限** = 用户所在组织及其全部祖先的授权并集，先被 `deny` 例外剔除，再由 `allow` 例外增补。
- **例外删除不遗漏**：每个快照独立全量重算有效权限，删除 `allow` 例外会正确表现为
  「失去」（无继承兜底）或「不变」（继承兜底），删除 `deny` 例外会让被遮蔽的继承权限重新显现为「新增」。
- **最短来源链**：每条具体权限记录跳数最少的来源链（等长取字典序最小保证确定性）。
  例外链 `user -> exception` 恒短于任何继承链，因此显式例外天然优先展示。
- **路径压缩**：以资源目录为全集构建前缀树，子树全覆盖折叠为 `<path>/**`（根为 `/**`），
  叶子资源输出精确路径。

## 失去权限的来源解释

- 若 `after` 快照中存在覆盖该路径的 `deny` 例外，来源链指向该例外（权限被显式收回）；
- 否则指向 `before` 快照中原本授予它的来源链（迁出组织 / 授权被移除 / 例外被删除）。

## 运行

零依赖，要求 Node.js ≥ 23.6（原生 TypeScript 类型擦除）。

```sh
npm test     # 运行测试（node:test）
npm run demo # 运行端到端示例
```

## API

```ts
import { diffSnapshots, computeEffective, formatChain } from './src/index.ts';

const report = diffSnapshots(beforeSnapshot, afterSnapshot);
// report.users: [{ userId, resourceType, hasChanges, actions: [{ action, added, lost, unchanged }] }]
// 每个 added/lost/unchanged 条目: { path: '/docs/**', chain: [user, org..., grant|exception] }
```

输入为两个 `Snapshot`：组织树（`OrgNode{id,name,parentId}`）、用户（`User{id,orgId}`）、
授权（`Grant{id,orgId,resourceType,path,actions}`，前缀继承）、
例外（`UserException{id,userId,resourceType,path,effect,actions}`）与资源目录
（`catalog: 资源类型 -> 具体路径列表`）。

## 代码结构

- `src/types.ts` — 领域模型与 diff 输出类型
- `src/paths.ts` — 路径规范化、前缀展开、前缀树压缩
- `src/effective.ts` — 有效权限计算与最短来源链
- `src/diff.ts` — 双快照差分与结果压缩
- `tests/diff.test.ts` — 11 个用例覆盖继承、压缩、最短链、重命名、例外增删
