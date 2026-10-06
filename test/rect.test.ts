import {
  compareRect,
  intersect,
  rect,
  rectArea,
  sortRects,
  subtractRect,
} from "../src/rect";
import { eq, ok, randInt, rng, test } from "./harness";

test("rect: subtract is area-conserving and disjoint (randomized)", () => {
  const rand = rng(12345);
  for (let t = 0; t < 5000; t++) {
    const mk = () => {
      const x0 = randInt(rand, 0, 7);
      const y0 = randInt(rand, 0, 7);
      return rect(x0, y0, x0 + randInt(rand, 1, 8 - x0), y0 + randInt(rand, 1, 8 - y0));
    };
    const a = mk();
    const b = mk();
    const pieces = subtractRect(a, b);
    const inter = intersect(a, b);
    const interArea = inter === null ? 0 : rectArea(inter);
    const sum = pieces.reduce((s, p) => s + rectArea(p), 0);
    eq(sum + interArea, rectArea(a), `area conservation for a=${JSON.stringify(a)} b=${JSON.stringify(b)}`);
    // pairwise disjoint
    for (let i = 0; i < pieces.length; i++) {
      for (let j = i + 1; j < pieces.length; j++) {
        ok(intersect(pieces[i], pieces[j]) === null, "pieces disjoint");
      }
    }
    // no piece overlaps b
    for (const p of pieces) ok(intersect(p, b) === null, "piece outside b");
  }
});

test("rect: canonical sort order", () => {
  const rs = sortRects([
    rect(1, 0, 2, 1),
    rect(0, 2, 1, 3),
    rect(0, 1, 1, 2),
    rect(0, 1, 2, 2),
  ]);
  ok(compareRect(rs[0], rect(0, 1, 1, 2)) === 0, "x0 first");
  ok(compareRect(rs[1], rect(0, 1, 2, 2)) === 0, "then x1");
  ok(compareRect(rs[2], rect(0, 2, 1, 3)) === 0, "then y0");
  ok(compareRect(rs[3], rect(1, 0, 2, 1)) === 0, "last");
});
