import { test } from "node:test";
import assert from "node:assert/strict";
import { cross, grahamScan, polygonArea } from "../src/geometry.ts";
import type { Point } from "../src/geometry.ts";
import { DynamicConvexHullEvolver } from "../src/evolver.ts";
import type { TimedPoint } from "../src/evolver.ts";

/** Rotate a hull so it starts at the lexicographically smallest vertex. */
function normalize(hull: readonly Point[]): Point[] {
  if (hull.length === 0) return [];
  let best = 0;
  for (let i = 1; i < hull.length; i++) {
    if (
      hull[i].x < hull[best].x ||
      (hull[i].x === hull[best].x && hull[i].y < hull[best].y)
    ) {
      best = i;
    }
  }
  return [...hull.slice(best), ...hull.slice(0, best)].map((p) => ({
    x: p.x,
    y: p.y,
  }));
}

function assertHullEquals(actual: readonly Point[], expected: readonly Point[]) {
  assert.deepEqual(normalize(actual), normalize(expected));
}

/** Deterministic PRNG (mulberry32) for reproducible fuzz runs. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("grahamScan: square with interior and collinear edge points", () => {
  const hull = grahamScan([
    { x: 0, y: 0 },
    { x: 4, y: 0 },
    { x: 4, y: 4 },
    { x: 0, y: 4 },
    { x: 2, y: 2 }, // interior
    { x: 2, y: 0 }, // collinear on bottom edge
    { x: 0, y: 0 }, // duplicate
  ]);
  assertHullEquals(hull, [
    { x: 0, y: 0 },
    { x: 4, y: 0 },
    { x: 4, y: 4 },
    { x: 0, y: 4 },
  ]);
});

test("grahamScan: degenerate sets", () => {
  assert.deepEqual(grahamScan([]), []);
  assert.deepEqual(grahamScan([{ x: 1, y: 1 }]), [{ x: 1, y: 1 }]);
  assertHullEquals(
    grahamScan([
      { x: 0, y: 0 },
      { x: 1, y: 1 },
      { x: 2, y: 2 },
    ]),
    [
      { x: 0, y: 0 },
      { x: 2, y: 2 },
    ],
  );
});

test("evolver: incremental inserts always match a full Graham scan", () => {
  const random = rng(42);
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  const live: TimedPoint[] = [];
  for (let i = 0; i < 200; i++) {
    const p: TimedPoint = {
      id: `p${i}`,
      x: Math.floor(random() * 50),
      y: Math.floor(random() * 50),
    };
    evolver.addPoint(p, 1000);
    live.push(p);
    assertHullEquals(evolver.getHull(), grahamScan(live));
  }
  assert.ok(evolver.stats.incrementalInserts > 0);
  assert.ok(evolver.stats.interiorInserts > 0);
});

test("evolver: TTL expiry fades points out of the live set", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  evolver.addPoint({ id: "a", x: 0, y: 0 }, 3);
  evolver.addPoint({ id: "b", x: 10, y: 0 }, 3);
  evolver.addPoint({ id: "c", x: 10, y: 10 }, 3);
  evolver.addPoint({ id: "d", x: 0, y: 10 }, 5);
  assert.equal(evolver.size, 4);

  evolver.advanceTo(2);
  assert.equal(evolver.size, 4);

  const hull = evolver.advanceTo(3.5); // a, b, c expired
  assert.equal(evolver.size, 1);
  assertHullEquals(hull, [{ x: 0, y: 10 }]);
  assert.equal(evolver.stats.expirations, 3);

  evolver.advanceTo(6); // d expired too
  assert.equal(evolver.size, 0);
  assert.deepEqual(evolver.getHull(), []);
});

test("evolver: interior expiry never triggers a full rebuild", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  evolver.addPoint({ id: "v1", x: 0, y: 0 }, 100);
  evolver.addPoint({ id: "v2", x: 10, y: 0 }, 100);
  evolver.addPoint({ id: "v3", x: 10, y: 10 }, 100);
  evolver.addPoint({ id: "v4", x: 0, y: 10 }, 100);
  evolver.addPoint({ id: "inner", x: 5, y: 5 }, 2);
  const rebuildsBefore = evolver.stats.fullRebuilds;

  const hull = evolver.advanceTo(3); // only the interior point expires
  assert.equal(evolver.stats.fullRebuilds, rebuildsBefore);
  assertHullEquals(hull, [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 10 },
    { x: 0, y: 10 },
  ]);
});

test("evolver: hull-vertex expiry triggers exactly one lazy rebuild", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  evolver.addPoint({ id: "tip", x: 20, y: 5 }, 2);
  evolver.addPoint({ id: "v1", x: 0, y: 0 }, 100);
  evolver.addPoint({ id: "v2", x: 10, y: 0 }, 100);
  evolver.addPoint({ id: "v3", x: 10, y: 10 }, 100);
  evolver.addPoint({ id: "v4", x: 0, y: 10 }, 100);
  assert.equal(evolver.getHull().length, 5);
  const rebuildsBefore = evolver.stats.fullRebuilds;

  const hull = evolver.advanceTo(2.5); // tip expires
  assert.equal(evolver.stats.fullRebuilds, rebuildsBefore + 1);
  assertHullEquals(hull, [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 10 },
    { x: 0, y: 10 },
  ]);
});

test("evolver: re-adding an id replaces the stale observation", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  evolver.addPoint({ id: "drone", x: 0, y: 0 }, 10);
  evolver.addPoint({ id: "drone", x: 8, y: 8 }, 10);
  assert.equal(evolver.size, 1);
  assertHullEquals(evolver.getHull(), [{ x: 8, y: 8 }]);
  evolver.advanceTo(11); // replaced entry expires at 10, stale heap entry skipped
  assert.equal(evolver.size, 0);
});

test("evolver: rejects non-positive ttl and backwards time travel", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(5);
  assert.throws(() => evolver.addPoint({ id: "x", x: 0, y: 0 }, 0));
  assert.throws(() => evolver.advanceTo(4));
});

test("evolver: fuzz against brute-force oracle over a random timeline", () => {
  const random = rng(20261002);
  const evolver = new DynamicConvexHullEvolver();
  const live = new Map<string, TimedPoint & { expiresAt: number }>();
  let nextId = 0;

  for (let slot = 0; slot < 400; slot++) {
    evolver.advanceTo(slot);
    for (const [id, p] of live) {
      if (p.expiresAt <= slot) live.delete(id);
    }
    const arrivals = Math.floor(random() * 4);
    for (let k = 0; k < arrivals; k++) {
      const ttl = 1 + Math.floor(random() * 12);
      const p = {
        id: `d${nextId++}`,
        x: Math.floor(random() * 30),
        y: Math.floor(random() * 30),
        expiresAt: slot + ttl,
      };
      evolver.addPoint(p, ttl);
      live.set(p.id, p);
    }
    const expected = grahamScan([...live.values()]);
    assertHullEquals(evolver.getHull(), expected);
    // Structural invariants: strictly convex, counter-clockwise.
    const hull = evolver.getHull();
    if (hull.length >= 3) {
      assert.ok(polygonArea(hull) > 0);
      for (let i = 0; i < hull.length; i++) {
        const turn = cross(
          hull[i],
          hull[(i + 1) % hull.length],
          hull[(i + 2) % hull.length],
        );
        assert.ok(turn > 0, `reflex/collinear vertex at slot ${slot}`);
      }
    }
  }
  assert.ok(evolver.stats.expirations > 0);
});

test("evolver: collinear-heavy grid stays strictly convex", () => {
  const evolver = new DynamicConvexHullEvolver();
  evolver.advanceTo(0);
  const live: TimedPoint[] = [];
  for (let x = 0; x < 6; x++) {
    for (let y = 0; y < 6; y++) {
      const p = { id: `g${x}-${y}`, x, y };
      evolver.addPoint(p, 100);
      live.push(p);
    }
  }
  assertHullEquals(evolver.getHull(), grahamScan(live));
  assert.equal(evolver.getHull().length, 4);
});
