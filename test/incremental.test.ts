import { test } from "node:test";
import assert from "node:assert/strict";
import { DistanceField } from "../src/distanceField.ts";
import { bruteForceField, makeRng } from "./reference.ts";

function obstaclesOf(f: DistanceField, v = f.version): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  for (let y = f.originY; y < f.originY + f.height; y++) {
    for (let x = f.originX; x < f.originX + f.width; x++) {
      if (f.cellAt(v, x, y).obstacle) out.push([x - f.originX, y - f.originY]);
    }
  }
  return out;
}

function assertMatchesReference(f: DistanceField, ctx: string): void {
  const v = f.version;
  const obs = obstaclesOf(f, v);
  const ref = bruteForceField(f.width, f.height, obs);
  for (let y = 0; y < f.height; y++) {
    for (let x = 0; x < f.width; x++) {
      const c = f.cellAt(v, x + f.originX, y + f.originY);
      const r = ref[y * f.width + x];
      assert.equal(c.dist, r.dist, `${ctx}: dist at ${x},${y}`);
      assert.equal(c.srcX, r.srcX < 0 ? -1 : r.srcX + f.originX, `${ctx}: srcX at ${x},${y}`);
      assert.equal(c.srcY, r.srcY < 0 ? -1 : r.srcY + f.originY, `${ctx}: srcY at ${x},${y}`);
    }
  }
}

test("deleting the main obstacle reassigns a large region to the second-nearest source", () => {
  // Main obstacle A near the left, secondary B far right. Cells around A
  // belong to A's Voronoi region; removing A must reassign ALL of them
  // to B -- not just A's direct neighbours.
  const f = DistanceField.fromObstacles(24, 8, [[2, 2], [20, 2]]);
  const before = f.version;
  assert.deepEqual(
    [f.cellAt(before, 3, 3).srcX, f.cellAt(before, 3, 3).srcY],
    [2, 2],
  );

  const s = f.beginEdit();
  s.setObstacle(2, 2, false);
  s.commit();

  // A cell 3+ steps away from the deleted obstacle: repaired through
  // wavefront propagation, now sourced from B at (20,2).
  const c = f.cellAt(f.version, 5, 5);
  assert.equal(c.srcX, 20);
  assert.equal(c.srcY, 2);
  assert.equal(c.dist, (20 - 5) ** 2 + (5 - 2) ** 2);
  assertMatchesReference(f, "after main-obstacle deletion");
});

test("deleting the only obstacle leaves Infinity everywhere", () => {
  const f = DistanceField.fromObstacles(6, 6, [[3, 3]]);
  const s = f.beginEdit();
  s.setObstacle(3, 3, false);
  s.commit();
  for (let y = 0; y < 6; y++) {
    for (let x = 0; x < 6; x++) {
      assert.equal(f.cellAt(f.version, x, y).dist, Infinity);
    }
  }
});

test("tie-breaking stays coordinate-stable after incremental updates", () => {
  const f = DistanceField.fromObstacles(9, 3, [[0, 1], [8, 1], [4, 0]]);
  // cell (4,1): dist to (4,0) is 1. Remove (4,0): equidistant (dist 16)
  // to (0,1) and (8,1) -> smaller source x must win.
  const s = f.beginEdit();
  s.setObstacle(4, 0, false);
  s.commit();
  const c = f.cellAt(f.version, 4, 1);
  assert.equal(c.dist, 16);
  assert.equal(c.srcX, 0);
  assert.equal(c.srcY, 1);
  assertMatchesReference(f, "tie stability");
});

test("moveRegion relocates obstacles and repairs both ends", () => {
  const f = DistanceField.fromObstacles(20, 12, [[3, 3], [4, 3], [15, 8]]);
  const s = f.beginEdit();
  s.moveRegion(3, 3, 2, 1, 5, 4); // move the 2x1 block by (+5,+4)
  s.commit();
  const obs = obstaclesOf(f);
  assert.deepEqual(obs.sort(), [[8, 7], [9, 7], [15, 8]].sort());
  assertMatchesReference(f, "after moveRegion");
});

test("random local updates match the exact full transform", () => {
  const rng = makeRng(987654);
  for (let iter = 0; iter < 25; iter++) {
    const w = 10 + Math.floor(rng() * 30);
    const h = 10 + Math.floor(rng() * 25);
    const initial: Array<[number, number]> = [];
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        if (rng() < 0.08) initial.push([x, y]);
      }
    }
    const f = DistanceField.fromObstacles(w, h, initial);
    for (let round = 0; round < 12; round++) {
      const s = f.beginEdit();
      const ops = 1 + Math.floor(rng() * 6);
      for (let op = 0; op < ops; op++) {
        const kind = rng();
        if (kind < 0.4) {
          s.setObstacle(Math.floor(rng() * w), Math.floor(rng() * h), true);
        } else if (kind < 0.8) {
          s.setObstacle(Math.floor(rng() * w), Math.floor(rng() * h), false);
        } else {
          const rw = 1 + Math.floor(rng() * 4);
          const rh = 1 + Math.floor(rng() * 4);
          const rx = Math.floor(rng() * (w - rw));
          const ry = Math.floor(rng() * (h - rh));
          const dx = Math.floor(rng() * 9) - 4;
          const dy = Math.floor(rng() * 9) - 4;
          s.moveRegion(rx, ry, rw, rh, dx, dy);
        }
      }
      s.commit();
      assertMatchesReference(f, `iter ${iter} round ${round}`);
    }
  }
});

test("incremental commit and full rebuild agree exactly", () => {
  const rng = makeRng(42);
  const w = 40, h = 30;
  const initial: Array<[number, number]> = [];
  for (let i = 0; i < 60; i++) initial.push([Math.floor(rng() * w), Math.floor(rng() * h)]);
  const a = DistanceField.fromObstacles(w, h, initial);
  const b = DistanceField.fromObstacles(w, h, initial);
  // Pre-generate one edit script so both fields receive identical edits.
  type Edit =
    | { kind: "set"; x: number; y: number; present: boolean }
    | { kind: "move"; x: number; y: number; w: number; h: number; dx: number; dy: number };
  const script: Edit[] = [];
  for (let i = 0; i < 15; i++) {
    script.push({
      kind: "set",
      x: Math.floor(rng() * w),
      y: Math.floor(rng() * h),
      present: rng() < 0.5,
    });
  }
  script.push({ kind: "move", x: 5, y: 5, w: 6, h: 4, dx: 10, dy: 7 });
  const edits = (f: DistanceField, full: boolean) => {
    const s = f.beginEdit();
    for (const e of script) {
      if (e.kind === "set") s.setObstacle(e.x, e.y, e.present);
      else s.moveRegion(e.x, e.y, e.w, e.h, e.dx, e.dy);
    }
    if (full) s.commitFull(); else s.commit();
  };
  edits(a, false);
  edits(b, true);
  const va = a.version, vb = b.version;
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const ca = a.cellAt(va, x, y);
      const cb = b.cellAt(vb, x, y);
      assert.equal(ca.dist, cb.dist, `dist at ${x},${y}`);
      assert.equal(ca.srcX, cb.srcX, `srcX at ${x},${y}`);
      assert.equal(ca.srcY, cb.srcY, `srcY at ${x},${y}`);
    }
  }
});
