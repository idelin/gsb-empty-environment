import { test } from "node:test";
import assert from "node:assert/strict";
import { DistanceField } from "../src/distanceField.ts";

test("negative coordinates map correctly through the origin", () => {
  // World window x in [-8,8), y in [-4,12): origin is negative.
  const f = DistanceField.fromObstacles(16, 16, [[-5, -3]], -8, -4);
  const v = f.version;
  const c = f.cellAt(v, -4, -2); // diagonal neighbour of the obstacle
  assert.equal(c.dist, 2);
  assert.equal(c.srcX, -5);
  assert.equal(c.srcY, -3);
  assert.equal(f.cellAt(v, -5, -3).dist, 0);
  // outside the world window: Infinity, no source
  const oob = f.cellAt(v, -100, 0);
  assert.equal(oob.dist, Infinity);
  assert.equal(oob.srcX, -1);
  assert.equal(f.cellAt(v, 8, 0).dist, Infinity); // x == originX + width
});

test("old versions stay queryable; no mixed state is ever published", () => {
  const f = DistanceField.fromObstacles(16, 16, [[4, 4]]);
  const v1 = f.retain(f.version);

  const s = f.beginEdit();
  s.setObstacle(4, 4, false);
  s.setObstacle(12, 12, true);
  // Mid-edit: the published snapshot must still be the old consistent one.
  const during = f.version;
  assert.equal(during.id, v1.id);
  assert.equal(f.cellAt(during, 5, 5).dist, 2); // still sourced from (4,4)
  assert.equal(f.cellAt(during, 12, 12).dist, 128); // not yet the new obstacle
  assert.deepEqual(
    [f.cellAt(during, 12, 12).srcX, f.cellAt(during, 12, 12).srcY],
    [4, 4],
  );

  s.commit();
  const v2 = f.version;
  assert.notEqual(v2.id, v1.id);
  // New version reflects the edit...
  assert.equal(f.cellAt(v2, 12, 12).dist, 0);
  assert.deepEqual(
    [f.cellAt(v2, 5, 5).srcX, f.cellAt(v2, 5, 5).srcY],
    [12, 12],
  );
  // ...while the retained old version is untouched and fully consistent.
  assert.equal(f.cellAt(v1, 5, 5).dist, 2);
  assert.deepEqual(
    [f.cellAt(v1, 5, 5).srcX, f.cellAt(v1, 5, 5).srcY],
    [4, 4],
  );
  assert.equal(f.cellAt(v1, 12, 12).dist, 128); // still sourced from (4,4)
  f.release(v1);
});

test("cancel rolls back: nothing is published and the version id is unchanged", () => {
  const f = DistanceField.fromObstacles(16, 16, [[2, 2]]);
  const v0 = f.version;
  const s = f.beginEdit();
  s.setObstacle(2, 2, false);
  s.setObstacle(7, 7, true);
  s.moveRegion(0, 0, 4, 4, 3, 3);
  s.cancel();
  assert.equal(f.version.id, v0.id);
  assert.equal(f.cellAt(f.version, 2, 2).dist, 0);
  assert.equal(f.cellAt(f.version, 3, 3).dist, 2);
  assert.equal(f.cellAt(f.version, 7, 7).dist, 50); // still sourced from (2,2)
});

test("a session cannot be reused after commit or cancel", () => {
  const f = DistanceField.fromObstacles(8, 8, [[1, 1]]);
  const s = f.beginEdit();
  s.setObstacle(2, 2, true);
  s.commit();
  assert.throws(() => s.setObstacle(3, 3, true));
  assert.throws(() => s.commit());
  assert.throws(() => s.cancel());
  const s2 = f.beginEdit();
  s2.cancel();
  assert.throws(() => s2.commit());
});

test("chunk pages are shared across versions and reclaimed by GC", () => {
  // Dense 40x40 obstacle field -> 3x3 = 9 chunk pages, all materialized.
  const all: Array<[number, number]> = [];
  for (let y = 0; y < 40; y++) for (let x = 0; x < 40; x++) all.push([x, y]);
  const f = DistanceField.fromObstacles(40, 40, all);
  const v1 = f.retain(f.version);
  const pagesAfterBuild = f.pageCount;
  assert.equal(pagesAfterBuild, 9);

  // Remove one obstacle: its Voronoi region is just itself, so the repair
  // wave stays inside a single page. Copy-on-write clones exactly that
  // page; the other 8 pages are shared between v1 and v2.
  const s = f.beginEdit();
  s.setObstacle(5, 5, false);
  s.commit();
  const v2 = f.version;
  assert.equal(f.pageCount, pagesAfterBuild + 1);
  assert.equal(f.cellAt(v2, 5, 5).dist, 1);  // now served by a neighbour
  assert.equal(f.cellAt(v1, 5, 5).dist, 0);  // old version unchanged

  // v1 still pins its pages: GC reclaims nothing.
  assert.equal(f.collectGarbage(), 0);
  assert.equal(f.pageCount, pagesAfterBuild + 1);

  // Releasing v1 makes the old version of the cloned page collectable.
  f.release(v1);
  assert.equal(f.collectGarbage(), 1);
  assert.equal(f.pageCount, pagesAfterBuild);

  // Cancelling a session allocates no lasting pages.
  const s2 = f.beginEdit();
  s2.setObstacle(30, 30, true);
  s2.setObstacle(31, 31, true);
  s2.cancel();
  assert.equal(f.collectGarbage(), 0);
  assert.equal(f.pageCount, pagesAfterBuild);
});
