# json-dag-merge

JSON 兼容树的内容摘要 DAG、差异定位与三方合并（TypeScript，零依赖，Node.js 内置测试运行器）。

## 设计

- **摘要 DAG**（`src/digest.ts`）：每个节点摘要由子节点摘要递归构成（Merkle DAG）。
  对象键无序（按排序后的键计算），数组支持两种模式：稳定 ID（`id:<id>`，默认键 `id`）
  或显式位置（`#<index>`），按路径配置 `arrayMode(path)`。哈希函数可注入（默认 SHA-256），
  以便测试强制碰撞。重复数组 ID 抛出带路径的 `JsonDagError`。
- **差异定位**（`src/diff.ts`）：摘要相等即剪枝；`verifyCollisions: true` 时对等值摘要
  仍做内容复核（碰撞复核）。后处理把 digest 相同的 remove+set 配对为 `move`；内容仍存在于
  基线的新增节点标记 `copiedFrom`（复制绝不误判为移动）。`sourceMap` 记录每个目标路径的
  来源（same / move / copy）。
- **三方合并**（`src/merge.ts`）：递归合并 base/left/right；同路径双侧不同修改、
  删除 vs 修改、双双新增不同、数组顺序冲突均产出 `Conflict{path, base, left, right, reason}`
  （冲突路径保守保留基线值）。ID 模式数组按键匹配合并：一侧移动、另一侧改属性时身份保留。
- **原子补丁**（`src/patch.ts`）：`Patch{ops, baseDigest, targetDigest}`。应用前先校验旧基线
  摘要，应用后校验目标摘要；任一步失败即按逆操作回滚全部已应用步骤（恢复所有索引与根），
  错误携带失败操作的具体路径。成功时返回 `inverse` 逆补丁与稳定 ID 索引。
- **分块载入**（`src/chunk.ts`）：`chunkify` 自底向上把子树替换为 `ChunkRef` 存入
  内容寻址的 `MemChunkStore`；`store.reads` 统计真实内容读取。diff 在摘要相等的块上直接
  剪枝（零读取），跨块子树移动仅凭摘要识别。

## 运行

```sh
npm test   # node --test test/*.test.ts（Node >= 23，原生类型剥离）
```

## 测试覆盖（test/）

- `digest.test.ts`：规范化幂等与摘要稳定、键序无关、**强制哈希碰撞仍被复核发现**、
  重复数组 ID 报错并定位到 `$​.list[2]`。
- `merge.test.ts`：**一侧删除另一侧修改 → 三方冲突值**；**数组移动 + 属性更正保留身份**
  （diff 报告 move 而非 remove+add）；复制节点不误判为移动；删除并重现的子树报告为移动。
- `patch.test.ts`：基线摘要先于修改校验；**任一步失败回滚所有索引和根**；目标摘要篡改
  回滚；**逆补丁精确还原**；`expectOld` 前置条件失败定位。
- `chunk.test.ts`：**统计实际读取块验证摘要裁剪**（5 块改 1 块仅读 2 块）；
  **跨块子树移动零读取**；未变更分块树 diff 为空且零读取。

## 实际运行结果（2026-09-25，Node v23.9.0）

```
ℹ tests 16
ℹ pass 16
ℹ fail 0
ℹ duration_ms ~400
```
