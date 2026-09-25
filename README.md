# 可排序层级列表：鼠标/键盘共用状态机

TypeScript 实现的可排序层级列表拖拽内核。鼠标与键盘适配器把各自输入收敛到
同一个 `(gap, depth) -> MoveSpec` 投影函数，再驱动同一个会话状态机，因此两种
输入对同一位置产生完全相同的逻辑操作轨迹与补丁。核心计算（行布局、命中检测、
落点投影）全部基于夹具给出的节点树与字形度量纯函数完成，不借浏览器布局引擎。

## 运行环境

- Node.js >= 22.6（推荐 >= 23.6，可原生运行 TypeScript，无需编译与任何依赖）
- 测试使用 Node.js 内置 `node:test`

## 运行

```sh
npm test     # node --test 'test/*.test.ts'
npm run demo # node src/demo.ts
```

## 架构

```
src/
  types.ts       树/操作/轨迹类型；MoveOp 携带 opId、sourceVersion、targetRevision
  tree.ts        不可变树：applyMove / removeSubtree / reorderChildren / isValidMove
  projection.ts  统一落点投影 projectGap(gap, depth) + 预览 computePreviewRows
  metrics.ts     夹具字形度量 -> 行盒布局、指针命中（纯计算，无 DOM）
  fixtures.ts    夹具：节点树 + 字形度量
  session.ts     鼠标/键盘共用的拖拽会话状态机（idle->dragging->awaiting-ack->settled）
  controller.ts  客户端：正式树/焦点/选择；预览不改正式树；失败重试同 opId
  server.ts      模拟服务器：校验源版本+目标版本，opId 去重保证幂等
  mouse.ts       鼠标适配器：指针坐标 -> (gap, depth) -> projectGap
  keyboard.ts    键盘适配器：方向键调 gap，[ / ] 调 depth，Enter 提交，Esc 取消
  demo.ts        运行样例
test/            node:test 测试（23 个）
```

## 关键语义

- **预览与提交分离**：拖动中预览只记录在会话上（`currentSpec`），`previewRows()`
  在克隆树上投影；正式树仅被服务器回包（applied / reorder / 其他编辑）替换。
- **禁止移入自身后代**：`isValidMove` 在会话悬停与服务器两侧双重拦截。
- **取消恢复全部状态**：`begin` 时快照焦点与选择，取消/终止时恢复；树本就不变。
- **提交校验目标及源版本**：`MoveOp.sourceVersion` 对比全树版本，
  `targetRevision` 对比目标节点结构版本，任一过期即拒绝。
- **幂等重试**：传输失败用同一 `opId` 重试，服务器 `opLog` 去重，只应用一次。
- **失败保留独立编辑**：拒绝只丢弃该 op，不回滚树，随后发生的独立编辑保留。
- **目标拖动中被删除**：服务器回包触发会话 `terminate(target-removed)`，
  后续提交安全地成为空操作。

## 运行样例（`npm run demo` 摘要）

```
=== [2] 键盘执行同一移动：逻辑操作轨迹与鼠标完全一致 ===
鼠标轨迹: begin(a2) -> preview(a2 before b) -> preview(a2 inside b) -> commit(op-1: a2 inside b, srcV=0, tgtR=0)
键盘轨迹: begin(a2) -> preview(a2 before b) -> preview(a2 inside b) -> commit(op-1: a2 inside b, srcV=0, tgtR=0)
轨迹一致: true
最终树一致: true
焦点/选择一致: true

=== [5] 两个拖拽会话竞争：先提交者胜，独立编辑保留 ===
进行中会话数: 2
M 提交后 a1 已在 b 内: true
K 被拒，c 未被移动: true
独立编辑保留: true

=== [6] 操作ID幂等：响应丢失，同一 opId 重试只应用一次 ===
传输调用次数: 2 (首次响应丢失后重试)
服务器应用次数: 1 树版本: 1
a2 只移动一次: true

=== [7] 目标在拖动中被删除：会话安全终止 ===
轨迹: begin(a1) -> preview(a1 inside b) -> terminate(target-removed)
剩余行: a a1 a2 c
无 commit 产生: true
```

## 测试结果（`npm test`）

```
ℹ tests 23
ℹ pass 23
ℹ fail 0
```

覆盖：

- `test/tree.test.ts`：后代拦截、不可变 applyMove、revision 提升、子树删除、
  重排校验、服务器源/目标版本校验、非法移动拒绝、行展开。
- `test/parity.test.ts`：鼠标与键盘相同逻辑轨迹；最终树/焦点/选择一致；
  取消结果一致；键盘 `[` 提升层级与鼠标拖到同位置产生同一补丁。
- `test/concurrency.test.ts`：双会话竞争（先提交者胜、后者 stale-source-version）、
  服务器重排回包使提交过期、独立编辑保留、opId 幂等重试只应用一次。
- `test/safety.test.ts`：预览不改正式树（引用+内容）、取消恢复全部状态、
  非法悬停拦截后提交为空操作、目标/源拖动中被删除时安全终止、状态机可复用。
