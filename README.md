# json-dag-merge

JSON 树的内容摘要 DAG、差异定位与三方合并（TypeScript，零依赖，Node.js 内置测试）。

## 设计

- **摘要 DAG**（`src/digest.ts`）：`buildDigest` 自底向上构造摘要 DAG。对象键无序（键排序后参与摘要），数组有序。哈希函数可插拔（`HashFn`），默认 FNV-1a。`digestsEqual` 在摘要相等时做**碰撞复核**（`contentEqual` 结构化深比较），因此即使哈希被强制碰撞也能发现不同内容。`normalizeTree` 幂等且不改摘要。
- **差异定位**（`src/diff.ts`）：`diffTrees(a, b)` 输出定位到具体路径的操作序列（`set`/`insert`/`remove`/`move`）。数组支持两种模式：`position`（显式位置）与 `id`（稳定 ID，默认键 `id`，重复 ID 抛 `DuplicateArrayIdError` 并定位数组路径）。ID 模式下重排由模拟生成 `move` 序列；`detectMoves` 把“删除子树 + 同摘要新增子树”改写为 `move`——复制不会产生删除，因此**不会被误判为移动**。
- **三方合并**（`src/merge.ts`）：`merge3(base, left, right)` 递归合并，输出结果树、基线→结果的**原子补丁**、冲突列表（含**三方值**，`undefined` 表示该侧不存在）与**来源映射**（`kept`/`modified`/`added`/`moved`/`copied`）。一侧删除另一侧修改 → `delete-vs-modify` 冲突；数组顺序与基线仲裁，单侧重排不冲突；新增子树与基线既有子树同内容时记为 `copied` 而非移动。
- **原子补丁**（`src/patch.ts`）：`applyPatch` 先校验旧基线摘要（`expectBaseDigest`，先于任何修改），逐步应用并记录逆操作日志，任一步失败即逆序回滚，恢复所有数组下标与根；可选 `expectTargetDigest` 应用后校验，不符同样回滚。`invertPatch` 由基线求逆补丁用于还原。所有失败抛 `PatchApplyError`/`DigestMismatchError`，均携带具体路径。
- **分块载入**（`src/chunked.ts`）：`ChunkStore` 把深度为 `blockDepth` 整数倍的子树作为块，manifest 保存块摘要。`diffChunked` 沿摘要不等的块递归，相等则剪枝不读块，`reads` 统计实际读取块数；`verifyCollisions` 可强制读块做碰撞复核。

## 运行测试

需要 Node.js ≥ 22.6（原生 TypeScript 类型擦除 + `node:test`）：

```sh
node --test test/*.test.ts   # 或 npm test
```

## 实际运行结果

环境：Node.js v23.9.0（macOS arm64），2026-09-25 运行，退出码 0：

```
✔ 统计实际读取块以验证摘要裁剪          # reads=3 < totalBlocks=37，未变更树 0 读取
✔ 跨块子树移动                          # 识别为单个 move，被移动子树 0 读取
✔ 重复数组ID报错并定位到数组路径         # DuplicateArrayIdError at $.items
✔ 强制摘要函数碰撞仍发现不同内容（碰撞复核）
✔ 规范化幂等且摘要与键序无关
✔ 一侧删除另一侧修改 → 冲突并携带三方值
✔ 数组移动同时属性更正应保留身份（稳定ID模式）
✔ 复制节点不被误作移动
✔ 补丁任一步失败 → 回滚所有数组下标与根
✔ 逆补丁还原
✔ 旧基线与目标摘要校验先于修改
ℹ tests 11  ℹ pass 11  ℹ fail 0
```
