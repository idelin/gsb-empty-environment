import { frac } from "../src/fraction";
import { Rect, rect, area, containsPoint, overlaps, compareRects } from "../src/rect";
import { PremulColor, equalsPremul, premulToString, straight } from "../src/color";
import { Scene, SceneNode, scene, rectNode, groupNode } from "../src/scene";
import { ColoredFragment, renderScene } from "../src/composite";
import { assert, assertEq, rng, randInt } from "./util";
import { evalSceneAtPoint, boundingBox, sceneRects } from "./grid";

function colorAt(frags: ColoredFragment[], x: number, y: number): PremulColor | null {
  let found: PremulColor | null = null;
  for (const f of frags) {
    if (containsPoint(f.rect, x, y)) {
      assert(found === null, `two fragments contain (${x},${y})`);
      found = f.color;
    }
  }
  return found;
}

function expectPixel(
  frags: ColoredFragment[],
  px: number,
  py: number,
  expected: PremulColor | null,
  ctx: string,
): void {
  const c = colorAt(frags, px + 0.5, py + 0.5);
  if (expected === null) {
    assert(c === null, `${ctx}: pixel (${px},${py}) expected transparent, got ${c && premulToString(c)}`);
  } else {
    assert(c !== null, `${ctx}: pixel (${px},${py}) expected ${premulToString(expected)}, got transparent`);
    assert(
      equalsPremul(c!, expected),
      `${ctx}: pixel (${px},${py}) expected ${premulToString(expected)}, got ${premulToString(c!)}`,
    );
  }
}

function pm(r: string, g: string, b: string, a: string): PremulColor {
  const F = (s: string) => {
    const [n, d] = s.split("/");
    return frac(BigInt(n!), BigInt(d ?? "1"));
  };
  return { r: F(r), g: F(g), b: F(b), a: F(a) };
}

function fixedCases(): void {
  const red = straight(frac(1), frac(0), frac(0), frac(1));
  const greenHalf = straight(frac(0), frac(1), frac(0), frac(1, 2));
  const blue = straight(frac(0), frac(0), frac(1), frac(1));
  const blueHalf = straight(frac(0), frac(0), frac(1), frac(1, 2));
  const redHalf = straight(frac(1), frac(0), frac(0), frac(1, 2));

  // Opaque red + half green overlapping.
  {
    const sc = scene([
      rectNode("red", rect(0, 0, 4, 4), red),
      rectNode("green", rect(2, 2, 6, 6), greenHalf),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 0, 0, pm("1", "0", "0", "1"), "red-only");
    expectPixel(fr, 3, 3, pm("1/2", "1/2", "0", "1"), "green-over-red");
    expectPixel(fr, 5, 5, pm("0", "1/2", "0", "1/2"), "green-only");
    expectPixel(fr, 6, 6, null, "outside");
  }

  // Group opacity scales the isolated buffer (alpha 1 -> 1/2).
  {
    const sc = scene([
      groupNode("g", frac(1, 2), [rectNode("b", rect(0, 0, 2, 2), blue)]),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 1, 1, pm("0", "0", "1/2", "1/2"), "group opacity");
  }

  // Two opaque children in a half-opacity group: buffer is the top child,
  // NOT per-child alpha scaling (which would give a=3/4).
  {
    const sc = scene([
      groupNode("g", frac(1, 2), [
        rectNode("r", rect(0, 0, 2, 2), red),
        rectNode("b", rect(0, 0, 2, 2), blue),
      ]),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 1, 1, pm("0", "0", "1/2", "1/2"), "group buffer semantics");
  }

  // Nested groups multiply opacities: 1/2 * 1/2 = 1/4.
  {
    const sc = scene([
      groupNode("outer", frac(1, 2), [
        groupNode("inner", frac(1, 2), [rectNode("r", rect(0, 0, 2, 2), red)]),
      ]),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 1, 1, pm("1/4", "0", "0", "1/4"), "nested groups");
  }

  // Opaque top prunes translucent layers below: result is exactly the
  // opaque color wherever it covers.
  {
    const sc = scene([
      rectNode("rh", rect(0, 0, 4, 4), redHalf),
      rectNode("bh", rect(0, 0, 4, 4), blueHalf),
      rectNode("solid", rect(1, 1, 3, 3), straight(frac(1), frac(1), frac(1), frac(1))),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 2, 2, pm("1", "1", "1", "1"), "opaque prune");
    // blue half over red half outside the solid square:
    // P = (0,0,1/2) + (1/2,0,0)*1/2 = (1/4,0,1/2), a = 3/4
    expectPixel(fr, 0, 0, pm("1/4", "0", "1/2", "3/4"), "half over half");
  }

  // Half-open edges: touching translucent rects do not blend across the seam.
  {
    const sc = scene([
      rectNode("a", rect(0, 0, 2, 2), redHalf),
      rectNode("b", rect(2, 0, 4, 2), blueHalf),
    ]);
    const fr = renderScene(sc);
    expectPixel(fr, 0, 0, pm("1/2", "0", "0", "1/2"), "seam left");
    expectPixel(fr, 3, 0, pm("0", "0", "1/2", "1/2"), "seam right");
  }
}

function randomScene(rand: () => number, idGen: { n: number }): Scene {
  const dens = [1, 2, 3, 4];
  const channel = () => {
    const d = dens[randInt(rand, 0, dens.length - 1)]!;
    return frac(randInt(rand, 0, d), d);
  };
  const randRect = () => {
    const x0 = randInt(rand, 0, 6);
    const y0 = randInt(rand, 0, 6);
    return rect(x0, y0, randInt(rand, x0 + 1, 7), randInt(rand, y0 + 1, 7));
  };
  const randLeaf = (): SceneNode =>
    rectNode(`n${idGen.n++}`, randRect(), straight(channel(), channel(), channel(), channel()));

  const childCount = randInt(rand, 1, 4);
  const children: SceneNode[] = [];
  for (let i = 0; i < childCount; i++) {
    if (rand() < 0.35) {
      const innerCount = randInt(rand, 1, 3);
      const inner: SceneNode[] = [];
      for (let j = 0; j < innerCount; j++) inner.push(randLeaf());
      children.push(groupNode(`g${idGen.n++}`, channel(), inner));
    } else {
      children.push(randLeaf());
    }
  }
  return scene(children);
}

function verifyScene(sc: Scene, ctx: string): void {
  const frags = renderScene(sc);

  // Canonical order.
  for (let i = 1; i < frags.length; i++) {
    assert(
      compareRects(frags[i - 1]!.rect, frags[i]!.rect) < 0,
      `${ctx}: fragments not canonical at ${i}`,
    );
  }
  // Pairwise disjoint.
  for (let i = 0; i < frags.length; i++) {
    for (let j = i + 1; j < frags.length; j++) {
      assert(!overlaps(frags[i]!.rect, frags[j]!.rect), `${ctx}: fragments ${i},${j} overlap`);
    }
  }

  const leaves = sceneRects(sc.root).filter((r) => area(r) > 0);
  if (leaves.length === 0) {
    assertEq(frags.length, 0, `${ctx}: no leaves -> no fragments`);
    return;
  }
  const box = boundingBox(leaves);

  // Unit-grid fraction compositing: every pixel's fragment color must equal
  // the exact rational oracle color at the pixel center.
  let visiblePixels = 0;
  for (let py = box.y0; py < box.y1; py++) {
    for (let px = box.x0; px < box.x1; px++) {
      const oracle = evalSceneAtPoint(sc.root, px + 0.5, py + 0.5);
      if (oracle.a.isZero) {
        expectPixel(frags, px, py, null, `${ctx} pixel(${px},${py})`);
      } else {
        expectPixel(frags, px, py, oracle, `${ctx} pixel(${px},${py})`);
        visiblePixels++;
      }
    }
  }

  // Area conservation: fragment areas sum to the visible pixel count.
  const totalArea = frags.reduce((s, f) => s + area(f.rect), 0);
  assertEq(totalArea, visiblePixels, `${ctx}: area conservation`);
}

function randomCases(): void {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = rng(seed * 7919);
    const sc = randomScene(rand, { n: 0 });
    verifyScene(sc, `random seed=${seed}`);
  }
}

export function run(): void {
  fixedCases();
  randomCases();
}
