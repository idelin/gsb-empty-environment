import { visibleFragments, visibleFragmentsFlat } from "../src/opaque";
import {
  Rect, rect, area, containsPoint, overlaps, compareRects, rectToString,
} from "../src/rect";
import { assert, assertEq, rng, randInt } from "./util";
import { topmostAt, boundingBox } from "./grid";

function checkCanonical(frags: Rect[], ctx: string): void {
  for (let i = 1; i < frags.length; i++) {
    assert(
      compareRects(frags[i - 1]!, frags[i]!) < 0,
      `${ctx}: fragments not in canonical order at ${i}: ${frags.map(rectToString)}`,
    );
  }
}

function checkDisjointAcross(all: Rect[], ctx: string): void {
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) {
      assert(
        !overlaps(all[i]!, all[j]!),
        `${ctx}: fragments overlap: ${rectToString(all[i]!)} vs ${rectToString(all[j]!)}`,
      );
    }
  }
}

/** Full pixel-grid verification of one decomposition. */
function verifyOnGrid(layers: Rect[], ctx: string): void {
  const perLayer = visibleFragments(layers);
  assertEq(perLayer.length, layers.length, `${ctx}: one fragment list per layer`);

  const all: Rect[] = [];
  for (const frags of perLayer) {
    checkCanonical(frags, ctx);
    all.push(...frags);
  }
  checkDisjointAcross(all, ctx);

  const nonEmpty = layers.filter((r) => area(r) > 0);
  if (nonEmpty.length === 0) {
    assertEq(all.length, 0, `${ctx}: empty input must yield no fragments`);
    return;
  }
  const box = boundingBox(nonEmpty);

  // Pixel-by-pixel: every covered pixel is owned by exactly one fragment of
  // the topmost layer at that pixel; uncovered pixels own none.
  const pixelCount = new Map<number, number>();
  let coveredPixels = 0;
  for (let py = box.y0; py < box.y1; py++) {
    for (let px = box.x0; px < box.x1; px++) {
      const cx = px + 0.5, cy = py + 0.5;
      const expected = topmostAt(layers, px, py);
      let hits = 0;
      for (let i = 0; i < perLayer.length; i++) {
        for (const f of perLayer[i]!) {
          if (containsPoint(f, cx, cy)) {
            hits++;
            assertEq(i, expected, `${ctx}: pixel (${px},${py}) owned by layer ${i}, topmost is ${expected}`);
          }
        }
      }
      if (expected === -1) {
        assertEq(hits, 0, `${ctx}: pixel (${px},${py}) covered by fragment but no layer`);
      } else {
        assertEq(hits, 1, `${ctx}: pixel (${px},${py}) covered by ${hits} fragments`);
        coveredPixels++;
        pixelCount.set(expected, (pixelCount.get(expected) ?? 0) + 1);
      }
    }
  }

  // Area conservation: total fragment area == covered pixel count, and
  // per-layer fragment area == pixels where that layer is topmost.
  let totalArea = 0;
  for (let i = 0; i < perLayer.length; i++) {
    const layerArea = perLayer[i]!.reduce((s, f) => s + area(f), 0);
    assertEq(layerArea, pixelCount.get(i) ?? 0, `${ctx}: layer ${i} visible area mismatch`);
    totalArea += layerArea;
  }
  assertEq(totalArea, coveredPixels, `${ctx}: total visible area mismatch`);

  // Flat view agrees with the per-layer view and is canonically sorted.
  const flat = visibleFragmentsFlat(layers);
  assertEq(flat.length, all.length, `${ctx}: flat fragment count`);
  for (let i = 1; i < flat.length; i++) {
    const a = flat[i - 1]!, b = flat[i]!;
    const c = compareRects(a.rect, b.rect);
    assert(c < 0 || (c === 0 && a.layer <= b.layer), `${ctx}: flat order at ${i}`);
  }
  const perLayerSet = all.map(rectToString).sort();
  const flatSet = flat.map((f) => rectToString(f.rect)).sort();
  assertEq(JSON.stringify(flatSet), JSON.stringify(perLayerSet), `${ctx}: flat == per-layer union`);
}

function fixedCases(): void {
  // Disjoint rects stay whole.
  verifyOnGrid([rect(0, 0, 2, 2), rect(3, 3, 5, 5)], "disjoint");
  // Identical stack: only the top survives.
  verifyOnGrid([rect(0, 0, 3, 3), rect(0, 0, 3, 3), rect(0, 0, 3, 3)], "identical");
  // Partial overlap splits the bottom rect into an L (2 fragments).
  {
    const per = visibleFragments([rect(0, 0, 4, 4), rect(2, 2, 6, 6)]);
    assertEq(per[0]!.length, 2, "L-shape fragment count");
    assertEq(per[0]!.reduce((s, f) => s + area(f), 0), 12, "L-shape visible area");
    assertEq(per[1]!.length, 1, "top intact");
  }
  // Cross occlusion splits the middle rect into 4 corner fragments.
  {
    const per = visibleFragments([rect(0, 0, 5, 5), rect(2, 0, 3, 5)]);
    assertEq(per[0]!.length, 2, "cross splits bottom into left+right");
    assertEq(per[0]!.reduce((s, f) => s + area(f), 0), 20, "cross visible area");
  }
  // Half-open edges: edge-touching rects do not occlude each other.
  {
    const per = visibleFragments([rect(0, 0, 2, 2), rect(2, 0, 4, 2)]);
    assertEq(per[0]!.length, 1, "edge-touching bottom stays whole");
    assertEq(area(per[0]![0]!), 4, "edge-touching area preserved");
  }
  // Fully covered rect yields zero fragments.
  verifyOnGrid([rect(0, 0, 2, 2), rect(0, 0, 4, 4)], "fully covered");
  // Degenerate (zero-area) rects contribute nothing.
  verifyOnGrid([rect(1, 1, 1, 3), rect(0, 0, 2, 2)], "degenerate");
  // Hole punched by a small top rect: bottom splits into 4 strips.
  {
    const per = visibleFragments([rect(0, 0, 5, 5), rect(2, 2, 3, 3)]);
    assertEq(per[0]!.length, 4, "hole -> 4 strips");
    assertEq(per[0]!.reduce((s, f) => s + area(f), 0), 24, "hole visible area");
  }
}

function randomCases(): void {
  for (let seed = 1; seed <= 300; seed++) {
    const rand = rng(seed);
    const n = randInt(rand, 1, 6);
    const layers: Rect[] = [];
    for (let i = 0; i < n; i++) {
      const x0 = randInt(rand, 0, 7);
      const y0 = randInt(rand, 0, 7);
      const x1 = randInt(rand, x0, 8);
      const y1 = randInt(rand, y0, 8);
      layers.push(rect(x0, y0, x1, y1));
    }
    verifyOnGrid(layers, `random seed=${seed}`);
  }
}

export function run(): void {
  fixedCases();
  randomCases();
}
