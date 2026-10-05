# org-permission-diff

组织权限差异工具：比较用户迁入新组织树**前后**的权限，按用户、按资源类型输出
**新增（added）/ 失去（lost）/ 不变（unchanged）** 的权限，并给出每条变化
的**最短来源链**。

## 模型

- **组织树**：节点有稳定 `id` 与可变的 `name`。跨快照按 `id` 匹配，
  因此**重命名组织不会被当作权限变化**（名称只影响来源链的展示）。
- **权限来源**：
  - `OrgPolicy`：挂在组织节点上，沿树向下继承（allow / deny）。
  - `UserException`：挂在用户上的显式例外（allow / deny）。
- **资源目录（catalog）**：被审计的具体资源列表 `{ type, path }`。
  路径模式支持 `a/b/c`（精确）、`a/b/*`（前缀，含 `a/b` 本身）、`*`（全部）。

## 判定优先级（同一 用户 × 资源 × 操作）

1. 用户例外（exception）优先于组织策略（policy）；
2. 组织策略中，**离用户最近**的祖先节点获胜 —— 因此报告的来源链必然是最短的
   （不存在离用户更近的决定性规则）；
3. 同一节点上，路径模式更具体的获胜；
4. 仍并列时 deny 优先（fail closed）。

## 关键正确性保证

- **删除例外不会遗漏影响**：两侧快照的有效权限都从原始数据整体重算，
  不依赖任何增量状态。删除 allow 例外 → 对应权限进入 `lost`；
  删除 deny 例外 → 被压制的继承权限重新出现，进入 `added`，
  来源链指向重新生效的祖先策略。
- **资源路径压缩**：输出按 `(action, 来源规则)` 分组后用目录做子树压缩，
  仅当某前缀下的目录资源**全部**被覆盖时才输出 `p/*`，不会夸大权限；
  整个资源类型全覆盖时压缩为 `*`；叶子资源保持精确路径。

## 输出结构

```ts
DiffReport -> users[] -> byResourceType[type] -> { added, lost, unchanged }
```

每个 `GrantGroup`：`{ action, paths: 压缩后的路径, source: 最短来源链 }`。
来源链 `hops` 从决定性规则到用户，例如：

```
policy:p1(allow read docs/*) -> org:Corp -> org:Engineering -> user:alice
exception:e1(allow read docs/secret/hr) -> user:alice
```

## 运行

需要 Node ≥ 22.6（利用原生 TypeScript 类型擦除运行；≥ 23.6 无需任何标志）。

```bash
npm test     # node --test，11 个用例
npm run demo # 端到端迁移示例（重命名 + 迁移 + 删除例外 + 新增策略）
```

有网络环境下 `npm i -D typescript @types/node && npm run typecheck` 可做完整
类型检查（`tsconfig.json` 已配置 `erasableSyntaxOnly`，与 Node 类型擦除兼容）。

## 文件

- `src/types.ts` — 领域模型与输出类型
- `src/pattern.ts` — 路径模式匹配与具体度
- `src/effective.ts` — 有效权限整体重算（含优先级与来源链生成）
- `src/compress.ts` — 基于目录的路径压缩
- `src/diff.ts` — 前后快照 diff（按 id 匹配组织/用户/规则）
- `src/demo.ts` — 示例场景
- `test/diff.test.ts` — 测试
