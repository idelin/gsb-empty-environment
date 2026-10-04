# snapdiff — 目录快照差异工具

接收两棵目录快照（节点 ID、类型、父 ID、名称、内容摘要），识别**新增、删除、改名、移动、内容变化**，
输出一份可按拓扑顺序应用的操作计划，并附带一个独立应用器，逐步校验前置条件、临时路径冲突，
最终核对结果树与目标快照完全一致。

## 运行环境

- Node.js ≥ 23（或 ≥ 22.6 加 `--experimental-strip-types`），直接运行 TypeScript，无需编译与第三方依赖。

```bash
node --test test/*.test.ts                 # 运行测试
node src/cli.ts diff  old.json new.json    # 输出变更列表 + 有序计划（JSON）
node src/cli.ts apply old.json plan.json new.json  # 应用计划并核对目标树
```

## 数据模型

```json
{ "nodes": [
  { "id": "src", "generation": 1, "type": "dir",  "parentId": "root", "name": "src",  "digest": null },
  { "id": "a",   "generation": 1, "type": "file", "parentId": "src",  "name": "a.ts", "digest": "h1" }
]}
```

- **节点身份 = `(id, generation)`**。同一 ID 在不同代际被复用时，视为不同节点（删除 + 新增），
  不会误报为改名/移动/内容变化；父节点 ID 复用（代际提升）时，未变化的子节点也会被正确识别为"移动"。
- 同一快照内 `id` 唯一；`parentId` 必须指向一个存在的目录；父链无环；路径（父链名称拼接）唯一。
  违反任一条即抛出 `SnapshotError`。

## 差异识别（`src/diff.ts`）

按 `(id, generation)` 匹配两棵快照：

- 仅在新快照 → `added`；仅在旧快照 → `deleted`（类型变化的同身份节点同样按删 + 增处理）。
- 两侧同在：名称不同 → `renamed`；父身份（父 ID + 父代际）不同 → `moved`；摘要不同 → `content-changed`。
- 移动按身份匹配，**不会**报成删除 + 新增。

## 计划生成（`src/plan.ts`）

计划是 `create / delete / move / moveToTemp / updateContent` 的有序序列，按依赖图拓扑排序：

- `delete` 依赖其旧子节点的 `delete` 或 `move`（**先搬出、后删除**，即"移动到已删除父节点需先解决"）。
- `create` / `move` 依赖目标父目录的就位（父目录先 `create` 或先 `move`）。
- `create` / `move` 依赖目标路径旧占用者的腾退（占用者先 `delete` 或先 `move`）。
- 依赖环（如同级互换名称、父目录整体换代）通过把环上某个 `move` 拆成
  `moveToTemp`（先停放到 `/.snapdiff-tmp/N` 临时路径）+ 最终 `move` 来打破。
- 移动到目标快照中不存在的父节点、目标路径被不移动也不删除的节点占用等情况，
  抛出带冲突清单的 `PlanError`。

## 独立应用器（`src/apply.ts`）

`applyPlan(oldSnapshot, plan, expectedNewSnapshot)`：

- 逐条校验并执行操作：节点存在性、父目录存在且为目录、目标路径空闲、
  删除的目录必须已腾空、**临时路径不得与任何现存路径冲突**。
- 计划结束后不允许有节点滞留在临时路径。
- 最终把结果树与目标快照逐节点核对（身份、类型、名称、摘要、父身份），不一致抛出 `ApplyError`。

## 项目结构

- `src/types.ts` — 快照、变更、计划操作的类型定义
- `src/snapshot.ts` — 快照索引与结构校验
- `src/diff.ts` — 差异识别
- `src/plan.ts` — 依赖图 + 拓扑排序 + 临时移动破环
- `src/apply.ts` — 独立应用器与目标树核对
- `src/cli.ts` — 命令行入口（`diff` / `apply`）
- `test/` — `node:test` 测试；`examples/` — 示例快照
