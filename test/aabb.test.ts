import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { contains, expanded, overlaps, surfaceArea, union } from "../src/aabb.ts";

describe("AABB 基础运算", () => {
  const unit = { minX: 0, minY: 0, minZ: 0, maxX: 1, maxY: 1, maxZ: 1 };

  it("overlaps: 相交、分离、边界相触", () => {
    assert.equal(overlaps(unit, { minX: 0.5, minY: 0.5, minZ: 0.5, maxX: 2, maxY: 2, maxZ: 2 }), true);
    assert.equal(overlaps(unit, { minX: 2, minY: 0, minZ: 0, maxX: 3, maxY: 1, maxZ: 1 }), false);
    // 边界相触视为相交（闭区间）
    assert.equal(overlaps(unit, { minX: 1, minY: 0, minZ: 0, maxX: 2, maxY: 1, maxZ: 1 }), true);
    assert.equal(overlaps(unit, unit), true);
  });

  it("contains: 包含关系", () => {
    assert.equal(contains(unit, { minX: 0.2, minY: 0.2, minZ: 0.2, maxX: 0.8, maxY: 0.8, maxZ: 0.8 }), true);
    assert.equal(contains(unit, { minX: -0.1, minY: 0, minZ: 0, maxX: 0.5, maxY: 1, maxZ: 1 }), false);
    assert.equal(contains(unit, unit), true);
  });

  it("union / expanded / surfaceArea", () => {
    const other = { minX: 1, minY: -1, minZ: 0, maxX: 3, maxY: 0.5, maxZ: 2 };
    assert.deepEqual(union(unit, other), { minX: 0, minY: -1, minZ: 0, maxX: 3, maxY: 1, maxZ: 2 });
    assert.deepEqual(expanded(unit, 0.5), { minX: -0.5, minY: -0.5, minZ: -0.5, maxX: 1.5, maxY: 1.5, maxZ: 1.5 });
    assert.equal(surfaceArea({ minX: 0, minY: 0, minZ: 0, maxX: 1, maxY: 2, maxZ: 3 }), 22);
  });
});
