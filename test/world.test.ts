import test from "node:test";
import assert from "node:assert/strict";

import {
  CollisionError,
  SparsePixelCollisionWorld,
} from "../src/index.ts";
import type { CollisionPair, ObjectUpdate, Pixel } from "../src/index.ts";

function pairSet(pairs: readonly CollisionPair[]): Map<string, number> {
  return new Map(pairs.map((p) => [`${p.a}|${p.b}`, p.overlap]));
}

function bruteForcePairs(world: SparsePixelCollisionWorld, ids: readonly string[]): Map<string, number> {
  const shapes = ids.filter((id) => world.hasObject(id)).map((id) => [id, world.getPixels(id)] as const);
  const result = new Map<string, number>();
  for (let i = 0; i < shapes.length; i += 1) {
    for (let j = i + 1; j < shapes.length; j += 1) {
      const [idA, pixelsA] = shapes[i];
      const [idB, pixelsB] = shapes[j];
      const keysB = new Set(pixelsB.map((p) => `${p.x},${p.y}`));
      let overlap = 0;
      for (const p of pixelsA) if (keysB.has(`${p.x},${p.y}`)) overlap += 1;
      if (overlap > 0) {
        const [lo, hi] = idA < idB ? [idA, idB] : [idB, idA];
        result.set(`${lo}|${hi}`, overlap);
      }
    }
  }
  return result;
}

function assertWorldMatchesBruteForce(world: SparsePixelCollisionWorld, ids: readonly string[]): void {
  assert.deepEqual(pairSet(world.getCollidingPairs()), bruteForcePairs(world, ids));
}

test("reports colliding pairs with exact overlap pixel counts", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }]);
  world.addObject("b", [{ x: 1, y: 0 }, { x: 2, y: 0 }, { x: 5, y: 5 }]);
  world.addObject("c", [{ x: 9, y: 9 }]);

  assert.equal(world.getOverlap("a", "b"), 2);
  assert.equal(world.getOverlap("b", "a"), 2);
  assert.equal(world.getOverlap("a", "c"), 0);
  assert.deepEqual(world.getCollidingPairs(), [{ a: "a", b: "b", overlap: 2 }]);
  assert.deepEqual(world.getCollisions("a"), [{ a: "a", b: "b", overlap: 2 }]);
  assert.deepEqual(world.getCollisions("c"), []);
});

test("translate moves a shape and updates collisions incrementally", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("hero", [{ x: 0, y: 0 }, { x: 1, y: 0 }]);
  world.addObject("rock", [{ x: 3, y: 0 }, { x: 4, y: 0 }]);
  assert.equal(world.getCollidingPairs().length, 0);

  world.translate("hero", 2, 0);
  assert.deepEqual(world.getPixels("hero"), [{ x: 2, y: 0 }, { x: 3, y: 0 }]);
  assert.equal(world.getOverlap("hero", "rock"), 1);

  world.translate("hero", 0, 1);
  assert.equal(world.getCollidingPairs().length, 0);
});

test("setShape replaces the old shape atomically", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }]);
  world.addObject("b", [{ x: 0, y: 0 }, { x: 10, y: 10 }]);
  assert.equal(world.getOverlap("a", "b"), 1);

  world.setShape("a", [{ x: 10, y: 10 }, { x: 11, y: 10 }]);
  assert.equal(world.getOverlap("a", "b"), 1);
  assert.deepEqual(world.getPixels("a"), [{ x: 10, y: 10 }, { x: 11, y: 10 }]);

  world.setShape("a", [{ x: 99, y: 99 }]);
  assert.equal(world.getCollidingPairs().length, 0);
});

test("removeObject drops the object and all of its pairs", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }, { x: 1, y: 1 }]);
  world.addObject("b", [{ x: 0, y: 0 }, { x: 1, y: 1 }]);
  world.addObject("c", [{ x: 1, y: 1 }]);
  assert.equal(world.pairCount, 3);

  world.removeObject("c");
  assert.deepEqual(world.getCollidingPairs(), [{ a: "a", b: "b", overlap: 2 }]);
  assert.throws(() => world.removeObject("c"), (err: unknown) => err instanceof CollisionError && err.code === "UNKNOWN_OBJECT");
});

test("rejects duplicate pixels without mutating the world", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }]);

  assert.throws(
    () => world.addObject("bad", [{ x: 1, y: 1 }, { x: 1, y: 1 }]),
    (err: unknown) => err instanceof CollisionError && err.code === "DUPLICATE_PIXEL",
  );
  assert.equal(world.hasObject("bad"), false);

  assert.throws(
    () => world.setShape("a", [{ x: 2, y: 2 }, { x: 2, y: 2 }]),
    (err: unknown) => err instanceof CollisionError && err.code === "DUPLICATE_PIXEL",
  );
  assert.deepEqual(world.getPixels("a"), [{ x: 0, y: 0 }]);
});

test("rejects negative coordinates without mutating the world", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }, { x: 1, y: 0 }]);

  assert.throws(
    () => world.addObject("bad", [{ x: -1, y: 0 }]),
    (err: unknown) => err instanceof CollisionError && err.code === "NEGATIVE_COORDINATE",
  );
  assert.throws(
    () => world.translate("a", 0, -1),
    (err: unknown) => err instanceof CollisionError && err.code === "NEGATIVE_COORDINATE",
  );
  assert.deepEqual(world.getPixels("a"), [{ x: 0, y: 0 }, { x: 1, y: 0 }]);
});

test("rejects non-integer coordinates and translations", () => {
  const world = new SparsePixelCollisionWorld();
  assert.throws(
    () => world.addObject("bad", [{ x: 0.5, y: 0 }]),
    (err: unknown) => err instanceof CollisionError && err.code === "INVALID_COORDINATE",
  );
  world.addObject("a", [{ x: 0, y: 0 }]);
  assert.throws(
    () => world.translate("a", Number.NaN, 1),
    (err: unknown) => err instanceof CollisionError && err.code === "INVALID_COORDINATE",
  );
});

test("batch update swaps two objects' positions atomically", () => {
  const world = new SparsePixelCollisionWorld();
  const shapeA: Pixel[] = [{ x: 0, y: 0 }, { x: 1, y: 0 }];
  const shapeB: Pixel[] = [{ x: 5, y: 5 }, { x: 6, y: 5 }];
  world.addObject("a", shapeA);
  world.addObject("b", shapeB);

  world.applyUpdates([
    { id: "a", kind: "set", pixels: shapeB },
    { id: "b", kind: "set", pixels: shapeA },
  ]);

  assert.deepEqual(world.getPixels("a"), shapeB);
  assert.deepEqual(world.getPixels("b"), shapeA);
  assert.equal(world.getCollidingPairs().length, 0);
  assertWorldMatchesBruteForce(world, ["a", "b"]);
});

test("batch swap of overlapping shapes keeps the correct overlap count", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }, { x: 1, y: 0 }]);
  world.addObject("b", [{ x: 1, y: 0 }, { x: 2, y: 0 }]);
  assert.equal(world.getOverlap("a", "b"), 1);

  world.applyUpdates([
    { id: "a", kind: "set", pixels: [{ x: 1, y: 0 }, { x: 2, y: 0 }] },
    { id: "b", kind: "set", pixels: [{ x: 0, y: 0 }, { x: 1, y: 0 }] },
  ]);

  assert.equal(world.getOverlap("a", "b"), 1);
  assert.deepEqual(world.getCollidingPairs(), [{ a: "a", b: "b", overlap: 1 }]);
});

test("batch translate swaps positions without transient pair leakage", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }]);
  world.addObject("b", [{ x: 4, y: 0 }]);
  world.addObject("watcher", [{ x: 4, y: 0 }]);
  assert.equal(world.getOverlap("b", "watcher"), 1);

  world.applyUpdates([
    { id: "a", kind: "translate", dx: 4, dy: 0 },
    { id: "b", kind: "translate", dx: -4, dy: 0 },
  ]);

  assert.deepEqual(world.getPixels("a"), [{ x: 4, y: 0 }]);
  assert.deepEqual(world.getPixels("b"), [{ x: 0, y: 0 }]);
  assert.equal(world.getOverlap("a", "watcher"), 1);
  assert.equal(world.getOverlap("a", "b"), 0);
  assert.equal(world.getOverlap("b", "watcher"), 0);
  assertWorldMatchesBruteForce(world, ["a", "b", "watcher"]);
});

test("batch update is all-or-nothing when one entry is invalid", () => {
  const world = new SparsePixelCollisionWorld();
  world.addObject("a", [{ x: 0, y: 0 }]);
  world.addObject("b", [{ x: 5, y: 5 }]);

  assert.throws(
    () =>
      world.applyUpdates([
        { id: "a", kind: "set", pixels: [{ x: 5, y: 5 }] },
        { id: "b", kind: "set", pixels: [{ x: -3, y: 0 }] },
      ]),
    (err: unknown) => err instanceof CollisionError && err.code === "NEGATIVE_COORDINATE",
  );

  assert.deepEqual(world.getPixels("a"), [{ x: 0, y: 0 }]);
  assert.deepEqual(world.getPixels("b"), [{ x: 5, y: 5 }]);
  assert.equal(world.getCollidingPairs().length, 0);

  assert.throws(
    () =>
      world.applyUpdates([
        { id: "a", kind: "set", pixels: [{ x: 1, y: 1 }] },
        { id: "a", kind: "translate", dx: 1, dy: 1 },
      ]),
    (err: unknown) => err instanceof CollisionError && err.code === "DUPLICATE_UPDATE",
  );
  assert.deepEqual(world.getPixels("a"), [{ x: 0, y: 0 }]);
});

test("incremental updates do work proportional to changed pixels, not object count", () => {
  const world = new SparsePixelCollisionWorld();
  const objectCount = 500;
  for (let i = 0; i < objectCount; i += 1) {
    world.addObject(`o${i}`, [{ x: i * 4, y: 0 }, { x: i * 4 + 1, y: 0 }]);
  }
  const before = world.stats.pairAdjustments;

  world.translate("o250", 0, 2);
  world.setShape("o251", [{ x: 0, y: 7 }, { x: 1, y: 7 }]);
  world.removeObject("o252");

  const delta = world.stats.pairAdjustments - before;
  assert.equal(delta, 0, "disjoint updates must not touch any pair counters");
  assert.equal(world.stats.objects, objectCount - 1);
  assert.equal(world.getCollidingPairs().length, 0);
});

test("randomized updates stay consistent with a brute-force reference", () => {
  let seed = 0x2f6e2b1;
  const random = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  const pick = <T>(items: readonly T[]): T => items[Math.floor(random() * items.length)];

  const world = new SparsePixelCollisionWorld();
  const ids = Array.from({ length: 8 }, (_, i) => `obj${i}`);
  const randomShape = (): Pixel[] => {
    const pixels: Pixel[] = [];
    const used = new Set<string>();
    const count = 1 + Math.floor(random() * 5);
    while (pixels.length < count) {
      const x = Math.floor(random() * 12);
      const y = Math.floor(random() * 12);
      const key = `${x},${y}`;
      if (!used.has(key)) {
        used.add(key);
        pixels.push({ x, y });
      }
    }
    return pixels;
  };

  for (const id of ids) world.addObject(id, randomShape());

  for (let step = 0; step < 400; step += 1) {
    const op = Math.floor(random() * 4);
    if (op === 0) {
      world.setShape(pick(ids), randomShape());
    } else if (op === 1) {
      const id = pick(ids);
      if (world.hasObject(id)) {
        const pixels = world.getPixels(id);
        const dx = Math.floor(random() * 5) - 2;
        const dy = Math.floor(random() * 5) - 2;
        if (pixels.every((p) => p.x + dx >= 0 && p.y + dy >= 0)) {
          world.translate(id, dx, dy);
        }
      }
    } else if (op === 2) {
      const id = pick(ids);
      if (world.hasObject(id)) world.removeObject(id);
    } else {
      const batch: ObjectUpdate[] = [];
      const chosen = new Set<string>();
      const batchSize = 1 + Math.floor(random() * 3);
      while (batch.length < batchSize) {
        const id = pick(ids);
        if (chosen.has(id)) continue;
        chosen.add(id);
        if (!world.hasObject(id) || random() < 0.5) {
          batch.push({ id, kind: "set", pixels: randomShape() });
        } else {
          batch.push({ id, kind: "remove" });
        }
      }
      world.applyUpdates(batch);
    }
    assertWorldMatchesBruteForce(world, ids);
  }
});
