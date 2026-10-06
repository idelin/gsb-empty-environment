import {
  compareRect,
  containsPoint,
  Rect,
  rect,
  rectArea,
  visibleFragments,
} from "../src/rect";
import { eq, ok, randInt, rng, test } from "./harness";

const W = 8;
const H = 8;

function randomRect(rand: () => number): Rect {
  const x0 = randInt(rand, 0, W - 1);
  const y0 = randInt(rand, 0, H - 1);
  return rect(x0, y0, x0 + randInt(rand, 1, W - x0), y0 + randInt(rand, 1, H - y0));
}

test("visibleFragments: pixel-exact vs brute force, area conserved (randomized)", () => {
  const rand = rng(777);
  for (let trial = 0; trial < 300; trial++) {
    const n = randInt(rand, 1, 6);
    const rects: Rect[] = [];
    for (let i = 0; i < n; i++) rects.push(randomRect(rand));
    const frags = visibleFragments(rects);
    eq(frags.length, n, "one fragment list per input rect");

    // Brute-force ownership: topmost rect covering each unit pixel.
    const owner: number[][] = [];
    for (let y = 0; y < H; y++) {
      owner.push([]);
      for (let x = 0; x < W; x++) {
        let top = -1;
        for (let i = 0; i < n; i++) {
          if (containsPoint(rects[i], x, y)) top = i;
        }
        owner[y].push(top);
      }
    }

    for (let i = 0; i < n; i++) {
      const list = frags[i];
      // canonical sort
      for (let k = 1; k < list.length; k++) {
        ok(compareRect(list[k - 1], list[k]) < 0, `fragments of rect ${i} canonically sorted`);
      }
      // mark pixels covered by this rect's fragments; detect overlap
      const mark: boolean[][] = [];
      for (let y = 0; y < H; y++) mark.push(new Array<boolean>(W).fill(false));
      let area = 0;
      for (const f of list) {
        ok(
          f.x0 >= rects[i].x0 && f.x1 <= rects[i].x1 && f.y0 >= rects[i].y0 && f.y1 <= rects[i].y1,
          `fragment inside its rect (trial ${trial}, rect ${i})`,
        );
        ok(rectArea(f) > 0, "no empty fragments");
        area += rectArea(f);
        for (let y = f.y0; y < f.y1; y++) {
          for (let x = f.x0; x < f.x1; x++) {
            ok(!mark[y][x], `fragments of rect ${i} disjoint at (${x},${y})`);
            mark[y][x] = true;
          }
        }
      }
      // pixel-by-pixel: marked iff this rect is the visible owner
      let owned = 0;
      for (let y = 0; y < H; y++) {
        for (let x = 0; x < W; x++) {
          if (owner[y][x] === i) owned++;
          ok(
            mark[y][x] === (owner[y][x] === i),
            `pixel (${x},${y}) rect ${i} trial ${trial}: fragment=${mark[y][x]} owner=${owner[y][x]}`,
          );
        }
      }
      // area conservation: fragment areas == visible pixel count
      eq(area, owned, `area conservation for rect ${i} trial ${trial}`);
    }
  }
});

test("visibleFragments: fully covered rect yields no fragments", () => {
  const frags = visibleFragments([rect(0, 0, 2, 2), rect(0, 0, 4, 4)]);
  eq(frags[0].length, 0, "bottom rect fully occluded");
  eq(frags[1].length, 1, "top rect fully visible");
});

test("visibleFragments: half-open edges — touching rects do not occlude", () => {
  const frags = visibleFragments([rect(0, 0, 2, 2), rect(2, 0, 4, 2)]);
  eq(frags[0].length, 1, "edge-adjacent rect leaves neighbor intact");
  eq(rectArea(frags[0][0]), 4, "full area preserved");
});
