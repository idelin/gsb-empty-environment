# Broadphase：动态 AABB 树碰撞对筛选器

物理引擎**粗测阶段（Broadphase）**碰撞检测器，TypeScript 实现，零运行时依赖。

## 核心设计

- **增量动态 AABB 树**（`src/dynamicAabbTree.ts`，Box2D `b2DynamicTree` 风格）：
  - 叶节点保存实体的膨胀包围盒（fat AABB），内部节点保存子树联合包围盒；
  - 插入采用 **SAH（表面积启发式）** 选择最优兄弟节点，插入/删除后通过 **AVL 旋转** 保持平衡，树高稳定在 `O(log N)`；
  - 节点存放于连续数组、索引代替指针、freelist 复用空闲节点，热路径零分配（复用遍历栈、就地写入联合包围盒）。
- **碰撞对筛选器**（`src/broadphase.ts`）：
  - 实体移动时先由 fat AABB 余量吸收小幅位移，越界才增量重插（`O(log N)`）；
  - `collectPairs()` 仅对 moved 缓冲中的代理做树查询并去重；移动占比过半时自动切换为**树内自相交遍历**（`O(N log N + K)`）；
  - 候选对基于精确包围盒（tight AABB）过滤后产出，直接移交窄测阶段。

## 复杂度

| 操作 | 复杂度 |
| --- | --- |
| 插入 / 删除 / 重定位代理 | `O(log N)` |
| 范围查询 | `O(log N + K)` |
| 增量收集碰撞对 | `O(M log N + K)`（M 为移动代理数） |
| 全量自相交遍历 | `O(N log N + K)` |
| 暴力比对（对照） | `O(N^2)` |

## 环境要求

- Node.js **>= 23.6**（原生 TypeScript 类型擦除，无需编译、无需安装依赖）。

## 运行

```bash
npm test        # node --test：17 个单元/验收测试
npm run demo    # 500 刚体模拟演示
```

## 验收场景（`test/acceptance.test.ts` / `examples/demo.ts`）

500 个刚体同时运动（476 个网格分布互不重叠 + 12 对重叠刚体），每帧全部移动，
粗测模块需避免 `O(N^2)` 两两比对，快速筛出仅 12 对包围盒重叠候选对。

实测输出（`node examples/demo.ts`，负载较高的共享机器上；空闲机器典型值为数十微秒）：

```
=== Broadphase 粗测演示 ===
场景: 500 个刚体同时运动, 预期重叠对: 12
动态 AABB 树构建完成: 500 个代理, 树高 11 (log2(500) ≈ 9)

模拟 120 帧 (全部刚体每帧运动):
  粗测产出候选对: 12 对 (暴力比对验证: 12 对)
  候选对列表: [[492,493],[494,495],[496,497],[498,499],[488,489],[490,491],
               [476,477],[482,483],[486,487],[484,485],[478,479],[480,481]]
  collectPairs 筛选耗时:   p50 154.1us
  单帧 (move + collect):   p50 430.0us
  O(N^2) 暴力比对单帧:     p50 440.9us

OK: 粗测筛出 12 对候选, 移交窄测阶段执行精细几何相交
```

测试结果（`npm test`）：

```
ℹ tests 17
ℹ pass 17
ℹ fail 0
```

## API 概览

```ts
import { Broadphase } from "./src/index.ts";

const bp = new Broadphase<number>(0.1); // 参数为 fat AABB 余量
bp.add(id, aabb);            // 注册实体
bp.move(id, newAabb);        // 实体移动，增量更新
bp.remove(id);               // 注销实体
const pairs = bp.collectPairs();    // 增量收集本帧候选对 -> 移交窄测
const all = bp.computeAllPairs();   // 全量自相交遍历（校验/重建用）
```

## 目录结构

```
src/aabb.ts             AABB 类型与运算（含零分配的 unionInto / unionSurfaceArea）
src/dynamicAabbTree.ts  增量动态 AABB 树（SAH 插入、旋转平衡、查询、自相交遍历）
src/broadphase.ts       碰撞对筛选器（moved 缓冲、去重、tight 过滤）
examples/scene.ts       验收场景：500 刚体 + 12 对重叠
examples/demo.ts        运行样例与性能统计
test/                   node:test 单元测试与验收测试
```
