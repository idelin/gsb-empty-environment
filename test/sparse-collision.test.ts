import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SparseCollisionWorld,
  ShapeValidationError,
  ObjectNotFoundError,
  ObjectAlreadyExistsError,
  type Pixel,
  type CollisionPair,
} from "../src/sparse-collision.ts";

/** 暴力重算：直接根据各对象像素集合统计全部碰撞对，作为增量索引的对照。 */
function bruteForce(shapes: Map<string, Pixel[]>): CollisionPair[] {
  const owners = new Map<string, Set<string>>();
  for (const [id, pixels] of shapes) {
    for (const p of pixels) {
      const key = p.x + "," + p.y;
      let set = owners.get(key);
      if (!set) {
        set = new Set();
        owners.set(key, set);
      }
      set.add(id);
    }
  }
  const counts = new Map<string, number>();
  for (const set of owners.values()) {
    const ids = [...set].sort();
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const key = ids[i] + "" + ids[j];
        counts.set(key, (counts.get(key) ?? 0) + 1);
      }
    }
  }
  return [...counts.entries()]
    .map(([key, overlap]) => {
      const [a, b] = key.split("");
      return { a, b, overlap };
    })
    .sort((p, q) =>
      p.a < q.a ? -1 : p.a > q.a ? 1 : p.b < q.b ? -1 : p.b > q.b ? 1 : 0,
    );
}

function rect(x0: number, y0: number, w: number, h: number): Pixel[] {
  const out: Pixel[] = [];
  for (let y = y0; y < y0 + h; y++) {
    for (let x = x0; x < x0 + w; x++) {
      out.push({ x, y });
    }
  }
  return out;
}

test("无重叠时不产生碰撞对", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 2, 2));
  world.addObject("b", rect(5, 5, 2, 2));
  assert.deepEqual(world.getCollisions(), []);
  assert.equal(world.getOverlap("a", "b"), 0);
});

test("重叠对象报告正确的交叠像素数", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 3, 3)); // 9 像素
  world.addObject("b", rect(2, 2, 3, 3)); // 与 a 交叠 (2,2) 1 像素
  assert.deepEqual(world.getCollisions(), [{ a: "a", b: "b", overlap: 1 }]);
  assert.equal(world.getOverlap("b", "a"), 1); // 参数顺序无关
  world.addObject("c", rect(1, 1, 2, 2)); // 与 a 交叠 4，与 b 交叠 1
  assert.deepEqual(world.getCollisions(), [
    { a: "a", b: "b", overlap: 1 },
    { a: "a", b: "c", overlap: 4 },
    { a: "b", b: "c", overlap: 1 },
  ]);
});

test("同一像素上的三个对象产生三个对象对", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", [{ x: 4, y: 4 }]);
  world.addObject("b", [{ x: 4, y: 4 }]);
  world.addObject("c", [{ x: 4, y: 4 }]);
  assert.deepEqual(world.getCollisions(), [
    { a: "a", b: "b", overlap: 1 },
    { a: "a", b: "c", overlap: 1 },
    { a: "b", b: "c", overlap: 1 },
  ]);
});

test("增量平移：每步与暴力重算结果一致", () => {
  const world = new SparseCollisionWorld();
  const shapes = new Map<string, Pixel[]>();
  world.addObject("a", rect(0, 0, 4, 4));
  world.addObject("b", rect(10, 0, 4, 4));
  world.addObject("c", rect(5, 10, 4, 4));
  shapes.set("a", rect(0, 0, 4, 4));
  shapes.set("b", rect(10, 0, 4, 4));
  shapes.set("c", rect(5, 10, 4, 4));

  const move = (id: string, dx: number, dy: number) => {
    world.translate(id, dx, dy);
    shapes.set(
      id,
      shapes.get(id)!.map((p) => ({ x: p.x + dx, y: p.y + dy })),
    );
    assert.deepEqual(world.getCollisions(), bruteForce(shapes));
  };

  for (let i = 0; i < 10; i++) move("b", -1, 0); // b 逐步穿过 a
  assert.equal(world.getOverlap("a", "b"), 16); // 完全重合
  for (let i = 0; i < 10; i++) move("c", 0, -1); // c 逐步靠近
  for (let i = 0; i < 6; i++) move("a", 1, 1); // a 移开后不再与 b 重叠
  assert.equal(world.getOverlap("a", "b"), 0);
});

test("换形增量生效并与暴力重算一致", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 4, 4));
  world.addObject("b", rect(2, 2, 4, 4));
  const shapes = new Map<string, Pixel[]>([
    ["a", rect(0, 0, 4, 4)],
    ["b", rect(2, 2, 4, 4)],
  ]);
  assert.deepEqual(world.getCollisions(), bruteForce(shapes));

  world.setShape("a", rect(3, 3, 2, 2)); // 完全落入 b 内
  shapes.set("a", rect(3, 3, 2, 2));
  assert.deepEqual(world.getCollisions(), bruteForce(shapes));
  assert.equal(world.getOverlap("a", "b"), 4);

  world.setShape("a", [{ x: 100, y: 100 }]); // 远离
  shapes.set("a", [{ x: 100, y: 100 }]);
  assert.deepEqual(world.getCollisions(), bruteForce(shapes));
});

test("非法换形原子失败：旧形状与碰撞计数不变", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 2, 2));
  world.addObject("b", rect(1, 1, 2, 2));
  const before = world.getCollisions();
  assert.deepEqual(before, [{ a: "a", b: "b", overlap: 1 }]);

  assert.throws(
    () => world.setShape("a", [{ x: 1, y: 1 }, { x: -5, y: 0 }]),
    ShapeValidationError,
  );
  // 状态完全保持
  assert.deepEqual(world.getCollisions(), before);
  assert.deepEqual(
    world.getPixels("a").sort((p, q) => p.y - q.y || p.x - q.x),
    rect(0, 0, 2, 2),
  );
});

test("非法平移原子失败：负坐标目标被拒绝且位置不变", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", [{ x: 1, y: 0 }]);
  assert.throws(() => world.translate("a", -2, 0), ShapeValidationError);
  assert.deepEqual(world.getPixels("a"), [{ x: 1, y: 0 }]);
  assert.throws(() => world.translate("a", 0.5, 0), ShapeValidationError);
  assert.deepEqual(world.getPixels("a"), [{ x: 1, y: 0 }]);
});

test("重复像素被拒绝", () => {
  const world = new SparseCollisionWorld();
  assert.throws(
    () => world.addObject("a", [{ x: 1, y: 1 }, { x: 1, y: 1 }]),
    /duplicate pixel/,
  );
  assert.equal(world.hasObject("a"), false); // 未残留
  world.addObject("a", [{ x: 0, y: 0 }]);
  assert.throws(
    () => world.setShape("a", [{ x: 2, y: 2 }, { x: 2, y: 2 }]),
    /duplicate pixel/,
  );
  assert.deepEqual(world.getPixels("a"), [{ x: 0, y: 0 }]);
});

test("负坐标与非整数坐标被拒绝", () => {
  const world = new SparseCollisionWorld();
  assert.throws(() => world.addObject("a", [{ x: -1, y: 0 }]), ShapeValidationError);
  assert.throws(() => world.addObject("a", [{ x: 0, y: -1 }]), ShapeValidationError);
  assert.throws(() => world.addObject("a", [{ x: 0.5, y: 0 }]), ShapeValidationError);
  assert.throws(() => world.addObject("a", [{ x: NaN, y: 0 }]), ShapeValidationError);
  assert.throws(() => world.addObject("a", [{ x: Infinity, y: 0 }]), ShapeValidationError);
  assert.equal(world.size, 0);
});

test("两个对象交换位置后索引保持一致", () => {
  const world = new SparseCollisionWorld();
  const shapeA = rect(0, 0, 3, 3);
  const shapeB = rect(10, 10, 3, 3);
  world.addObject("a", shapeA);
  world.addObject("b", shapeB);
  world.addObject("c", rect(1, 1, 1, 1)); // 与 a 的初始位置重叠

  // 通过换形互换位置
  world.setShape("a", shapeB);
  world.setShape("b", shapeA);
  const shapes = new Map<string, Pixel[]>([
    ["a", shapeB],
    ["b", shapeA],
    ["c", rect(1, 1, 1, 1)],
  ]);
  assert.deepEqual(world.getCollisions(), bruteForce(shapes));
  assert.equal(world.getOverlap("a", "c"), 0); // a 已离开
  assert.equal(world.getOverlap("b", "c"), 1); // b 占据了 a 的旧位置

  // 再通过平移换回来
  world.translate("a", -10, -10);
  world.translate("b", 10, 10);
  shapes.set("a", shapeA);
  shapes.set("b", shapeB);
  assert.deepEqual(world.getCollisions(), bruteForce(shapes));
  assert.equal(world.getOverlap("a", "c"), 1);
  assert.equal(world.getOverlap("b", "c"), 0);
});

test("移除对象清理其全部碰撞记录", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 3, 3));
  world.addObject("b", rect(1, 1, 3, 3));
  world.addObject("c", rect(2, 2, 3, 3));
  assert.equal(world.getCollisions().length, 3);
  world.removeObject("b");
  assert.deepEqual(world.getCollisions(), [{ a: "a", b: "c", overlap: 1 }]);
  world.removeObject("a");
  world.removeObject("c");
  assert.deepEqual(world.getCollisions(), []);
  assert.equal(world.size, 0);
});

test("对象 id 冲突与缺失的错误", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }]);
  assert.throws(() => world.addObject("a", [{ x: 1, y: 1 }]), ObjectAlreadyExistsError);
  assert.throws(() => world.removeObject("ghost"), ObjectNotFoundError);
  assert.throws(() => world.translate("ghost", 1, 1), ObjectNotFoundError);
  assert.throws(() => world.setShape("ghost", []), ObjectNotFoundError);
  assert.throws(() => world.getPixels("ghost"), ObjectNotFoundError);
});

test("getCollisionsFor 只返回相关对象对", () => {
  const world = new SparseCollisionWorld();
  world.addObject("a", rect(0, 0, 2, 2));
  world.addObject("b", rect(1, 1, 2, 2));
  world.addObject("c", rect(50, 50, 2, 2));
  assert.deepEqual(world.getCollisionsFor("a"), [{ a: "a", b: "b", overlap: 1 }]);
  assert.deepEqual(world.getCollisionsFor("c"), []);
});

test("随机操作模糊测试：增量索引始终等于暴力重算", () => {
  // 确定性伪随机数（线性同余）
  let seed = 0x2f6e2b1;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

  const world = new SparseCollisionWorld();
  const shapes = new Map<string, Pixel[]>();
  const ids = ["o0", "o1", "o2", "o3", "o4", "o5"];

  const randomShape = (): Pixel[] => {
    const w = 1 + Math.floor(rand() * 4);
    const h = 1 + Math.floor(rand() * 4);
    return rect(
      Math.floor(rand() * 20),
      Math.floor(rand() * 20),
      w,
      h,
    );
  };

  for (let step = 0; step < 500; step++) {
    const id = pick(ids);
    const op = rand();
    if (op < 0.3 || !world.hasObject(id)) {
      const shape = randomShape();
      if (world.hasObject(id)) {
        world.setShape(id, shape);
      } else {
        world.addObject(id, shape);
      }
      shapes.set(id, shape);
    } else if (op < 0.8) {
      const dx = Math.floor(rand() * 7) - 3;
      const dy = Math.floor(rand() * 7) - 3;
      const moved = shapes
        .get(id)!
        .map((p) => ({ x: p.x + dx, y: p.y + dy }));
      if (moved.every((p) => p.x >= 0 && p.y >= 0)) {
        world.translate(id, dx, dy);
        shapes.set(id, moved);
      } else {
        assert.throws(() => world.translate(id, dx, dy), ShapeValidationError);
      }
    } else {
      world.removeObject(id);
      shapes.delete(id);
    }
    assert.deepEqual(
      world.getCollisions(),
      bruteForce(shapes),
      `step ${step} 后索引与暴力重算不一致`,
    );
  }
});
