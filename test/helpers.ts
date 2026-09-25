import assert from "node:assert/strict";
import { Mask } from "../src/mask.ts";

/**
 * Compares the fast path against the brute-force reference per pixel.
 * Fully-inside / fully-outside pixels must match (nearly) exactly;
 * edge pixels allow a tolerance derived from the supersampling rates.
 */
export function assertCoverageClose(
  fast: Mask,
  ref: Mask,
  tolerance = 0.15,
): void {
  assert.equal(fast.width, ref.width);
  assert.equal(fast.height, ref.height);
  for (let y = 0; y < fast.height; y++) {
    for (let x = 0; x < fast.width; x++) {
      const f = fast.at(x, y);
      const r = ref.at(x, y);
      assert.ok(f >= 0 && f <= 1, `fast coverage ${f} out of [0,1] at (${x},${y})`);
      const diff = Math.abs(f - r);
      if (r <= 0.02) {
        assert.ok(f <= 0.1, `(${x},${y}): ref=${r} (empty) but fast=${f}`);
      } else if (r >= 0.98) {
        assert.ok(f >= 0.9, `(${x},${y}): ref=${r} (full) but fast=${f}`);
      } else {
        assert.ok(
          diff <= tolerance,
          `(${x},${y}): ref=${r} fast=${f} diff=${diff} > ${tolerance}`,
        );
      }
    }
  }
}
