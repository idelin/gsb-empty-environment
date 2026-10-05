# sparse-pixel-collision

稀疏像素碰撞库（TypeScript，零依赖）。按对象维护稀疏像素集合，支持平移与换形，
增量查询碰撞对象对及交叠像素数。

## 设计

- **倒排索引**：`像素 -> 占据该像素的对象集合`，配合 `对象对 -> 交叠像素数` 计数器。
- **增量更新**：平移 / 换形只触及该对象实际增删的像素，插入像素时只对同像素的
  其他拥有者累加计数，移除时对称递减；绝不重扫全部对象对。
- **原子换形**：`setShape` / `translate` 采用“先校验、后变更”两阶段协议。
  新形状完整校验通过后，才撤销旧形状并插入新形状；校验失败时旧形状与全部
  碰撞计数保持不变。
- **校验**：拒绝重复像素、负坐标、非整数 / 非有限坐标（`ShapeValidationError`）。

## API

```ts
import { SparseCollisionWorld } from "./src/sparse-collision.ts";

const world = new SparseCollisionWorld();
world.addObject("hero", [{ x: 0, y: 0 }, { x: 1, y: 0 }]);
world.addObject("slime", [{ x: 1, y: 0 }, { x: 2, y: 0 }]);

world.getCollisions();        // [{ a: "hero", b: "slime", overlap: 1 }]
world.getOverlap("hero", "slime"); // 1

world.translate("slime", 5, 0);    // 原子平移
world.setShape("hero", [{ x: 9, y: 9 }]); // 原子换形
world.getCollisions();        // []
world.removeObject("slime");
```

| 方法 | 说明 |
| --- | --- |
| `addObject(id, pixels)` | 新增对象；id 冲突或形状非法时抛错，状态不变 |
| `removeObject(id)` | 移除对象及其全部碰撞记录 |
| `setShape(id, pixels)` | 原子换形；校验失败时旧形状保留 |
| `translate(id, dx, dy)` | 原子平移；目标含负坐标时抛错且位置不变 |
| `getCollisions()` | 全部碰撞对象对及交叠像素数（按 id 排序） |
| `getCollisionsFor(id)` | 与指定对象碰撞的对象对 |
| `getOverlap(a, b)` | 两个对象的交叠像素数 |
| `getPixels(id)` | 对象像素坐标快照 |

## 测试

```sh
npm test   # node --test "test/*.test.ts"（Node >= 23.6，原生运行 TS）
```

测试覆盖：增量索引与暴力重算逐步对照（含 500 步随机模糊测试）、原子失败回滚、
重复像素 / 负坐标 / 非整数坐标校验、两对象交换位置、移除清理、多对象同点重叠。
