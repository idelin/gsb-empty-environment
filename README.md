# Broadphase Collision Filter

物理引擎粗测阶段碰撞检测器：基于**增量动态 AABB 树**(Box2D `b2DynamicTree` 风格,3D 版本),
维护场景中所有移动物体的外包盒,在物体移动时增量更新,并通过**树内自相交遍历**快速产出
可能碰撞的候选实体对(Pairs),移交窄测阶段做精细几何相交。

## 结构

- `src/aabb.ts` — 3D AABB 及其运算(重叠/包含/并集/膨胀/表面积启发)
- `src/dynamic-tree.ts` — 增量动态 AABB 树
  - SAH(表面积启发)选择插入位置,AVL 旋转保持平衡
  - 叶节点存"膨胀"外包盒(fat AABB),小幅移动不触发重插 → 增量更新
  - `query` 单盒查询 + `forEachOverlappingPair` 树内自相交遍历
- `src/broadphase.ts` — Broadphase 筛选器
  - `addBody / removeBody / moveBody`:移动时仅当紧凑盒逃出膨胀盒才重插
  - `computePairs()`:自适应策略 —— 少量移动逐体查询,大量移动整树自相交遍历;
    产出按 `(bodyA, bodyB)` 规范去重的候选对(紧凑盒二次确认)

## 运行

需要 Node.js >= 22.6(`--experimental-strip-types` 直接运行 TypeScript,无构建步骤):

```sh
npm test        # Node.js 内置测试运行器 (node:test)
npm run demo    # 500 刚体 / 12 重叠对的运行样例
```

## 验收场景

500 个刚体同时运动,仅 12 对包围盒重叠:

- 每帧 500 次 `moveBody` 增量更新(约 1 µs/次量级)
- 每帧一次 `computePairs()` 树内自相交遍历,精确筛出 12 对候选(与 O(N²) 暴力结果一致)
- 对比 O(N²) 暴力两两比对(125,000 次盒测试),粗测耗时低一个数量级以上
