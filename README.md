# snapshot-diff — 目录快照差异工具

输入两棵目录快照（节点 ID、类型、父 ID、名称、内容摘要、代际），输出差异与一份
**可按拓扑顺序直接应用**的变更计划，并附带一个**独立应用器**负责逐步核对与冲突解决。

## 数据模型

```ts
interface SnapshotNode {
  id: string;              // 节点身份：搬移/改名/改内容时保持不变
  type: 'file' | 'dir';
  parentId: string | null; // 顶层节点为 null
  name: string;
  digest?: string | null;  // 文件内容摘要；目录为 null
  generation?: number;     // 代际，默认 0；id 被回收重建时必须递增
}
```

节点身份 = `(id, generation)`。因此：

- **相同 ID 搬移** → 识别为 `move`，绝不会报成“删除 + 新增”；
- **ID 复用**（同 id、不同代际）→ 旧代际 `remove` + 新代际 `create`，与修改严格区分；
- 同 `(id, generation)` 但类型变化（文件变目录）→ 也按删除 + 新增处理。

## 差异类别

`diffSnapshots(source, target)` 返回：`added` / `removed` / `renamed` / `moved` / `contentChanged`。

## 应用计划（拓扑有序）

`buildPlan(source, target)` 产出的 `ops` 顺序保证：

1. `mkdir` 新增目录 —— 父先子后（按目标树深度排序）；
2. `rename` / `move` —— 在任何删除之前完成，**移出待删目录的节点先走**；
3. `update` 内容更新；
4. `create` 新增文件 —— 此时父目录必然已存在；
5. `remove` 删除 —— 子先父后（按源树深度倒序）。

**移动到已删除父节点**属于非法输入：目标快照中悬空的父引用会在校验阶段抛
`SnapshotError`，计划阶段另有 `PlanError` 兜底，必须先解决节点归属。

## 独立应用器

`TreeApplier` 不依赖 diff/plan 的内部结构，可独立消费计划：

- **逐步前置校验**：改名/移动/删除前核对原名、原父节点；更新前核对原内容摘要；
  任何一步不符即抛 `ApplyError` 并中止；
- **临时路径冲突解决**：目标槽位被占用时（如 a↔b 互换改名、新节点复用待删节点的名字），
  若占用者稍后会腾空（有后续 rename/move/remove），先将其挪到同目录唯一临时名，
  否则报冲突错误；
- **核对目标树**：`verifyAgainst(target)` 全量比对节点集合、名称、父级、摘要；
  `applyAndVerify(ops, target)` 一步完成应用 + 核对。

## 用法

```ts
import { buildPlan, TreeApplier } from './src/index.ts';

const { diff, ops } = buildPlan(sourceSnapshot, targetSnapshot);

const applier = new TreeApplier(sourceSnapshot);
applier.applyAndVerify(ops, targetSnapshot); // 有任何不一致都会抛错
```

## 测试

```sh
npm test        # 需要 Node >= 22（--experimental-strip-types）
npm run typecheck  # 需要本地安装 typescript（npm i）
```
