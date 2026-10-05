# sparse-pixel-collision

稀疏像素碰撞库：按对象维护稀疏像素集合，支持平移与换形，查询碰撞对象对及交叠像素数。

## 设计

- **增量索引**：内部维护「像素 → 占用对象」倒排索引和由它派生的「对象对 → 交叠像素数」表。
  平移/换形只触及发生变化的像素，代价为 O(变化像素数 × 每像素占用对象数)，
  不会每步重扫全部对象对；查询直接读取已维护的对象对表。
- **原子更新**：`setShape` / `translate` 先完整校验新形状，再一次性摘除旧形状、挂接新形状；
  `applyUpdates` 批量接口先对全部更新做校验和规划，任一更新非法则整批不生效。
  因此两个对象相互交换位置不会产生中间态或幽灵碰撞对。
- **校验**：拒绝重复像素（`DUPLICATE_PIXEL`）、负坐标（`NEGATIVE_COORDINATE`）、
  非整数坐标（`INVALID_COORDINATE`）、重复对象（`DUPLICATE_OBJECT`）、
  未知对象（`UNKNOWN_OBJECT`）及批内重复更新（`DUPLICATE_UPDATE`），
  均抛出带 `code` 的 `CollisionError`。

## 用法

```ts
import { SparsePixelCollisionWorld } from "sparse-pixel-collision";

const world = new SparsePixelCollisionWorld();
world.addObject("hero", [{ x: 0, y: 0 }, { x: 1, y: 0 }]);
world.addObject("rock", [{ x: 3, y: 0 }]);

world.translate("hero", 2, 0);          // hero 现在与 rock 交叠 1 像素
world.getCollidingPairs();              // [{ a: "hero", b: "rock", overlap: 1 }]
world.getOverlap("hero", "rock");       // 1

// 原子批量：两个对象交换位置，整批校验通过后一次性生效
world.applyUpdates([
  { id: "hero", kind: "set", pixels: [{ x: 9, y: 9 }] },
  { id: "rock", kind: "translate", dx: -3, dy: 0 },
]);
```

## 运行测试

需要 Node ≥ 22.6（TypeScript 类型擦除）；Node ≥ 23.6 开箱即用：

```sh
npm test
```

如需静态类型检查，安装 `typescript` 与 `@types/node` 后运行 `npm run typecheck`。
