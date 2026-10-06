import { BLACK, Color, colorEquals, rgb } from "../src/color";
import { compositeLayers, Fragment } from "../src/composite";
import { Rational } from "../src/rational";
import { compareRect, containsPoint, Rect, rect, rectArea } from "../src/rect";
import {
  FlatLayer,
  flattenScene,
  layerGroup,
  rectLayer,
  SceneNode,
} from "../src/scene";
import { eq, ok, randInt, rng, test } from "./harness";

const W = 8;
const H = 8;

/** Reference compositing over the unit grid: exact, no pruning. */
function compositeExact(layers: readonly FlatLayer[]): { color: Color; alpha: Rational } {
  let t = Rational.ONE;
  let r = Rational.ZERO;
  let g = Rational.ZERO;
  let b = Rational.ZERO;
  for (let k = layers.length - 1; k >= 0; k--) {
    const a = layers[k].alpha;
    r = r.add(t.mul(a).mul(layers[k].color.r));
    g = g.add(t.mul(a).mul(layers[k].color.g));
    b = b.add(t.mul(a).mul(layers[k].color.b));
    t = t.mul(Rational.ONE.sub(a));
  }
  const alpha = Rational.ONE.sub(t);
  if (alpha.isZero()) return { color: BLACK, alpha };
  return { color: rgb(r.div(alpha), g.div(alpha), b.div(alpha)), alpha };
}

function randomRect(rand: () => number): Rect {
  const x0 = randInt(rand, 0, W - 1);
  const y0 = randInt(rand, 0, H - 1);
  return rect(x0, y0, x0 + randInt(rand, 1, W - x0), y0 + randInt(rand, 1, H - y0));
}

function randomFraction(rand: () => number): Rational {
  const d = randInt(rand, 1, 4);
  return Rational.of(randInt(rand, 0, d), d);
}

function randomColor(rand: () => number): Color {
  return rgb(randomFraction(rand), randomFraction(rand), randomFraction(rand));
}

/** Random scene: rect layers, some wrapped in opacity groups (two levels). */
function randomScene(rand: () => number, trial: number): SceneNode {
  const n = randInt(rand, 1, 5);
  const leaves: SceneNode[] = [];
  for (let i = 0; i < n; i++) {
    leaves.push(
      rectLayer(`t${trial}r${i}`, randomRect(rand), randomColor(rand), randomFraction(rand)),
    );
  }
  // split leaves into 1..3 consecutive chunks, each chunk maybe grouped
  const nodes: SceneNode[] = [];
  let i = 0;
  let gid = 0;
  while (i < leaves.length) {
    const take = randInt(rand, 1, leaves.length - i);
    const chunk = leaves.slice(i, i + take);
    i += take;
    if (chunk.length > 1 || rand() < 0.4) {
      nodes.push(layerGroup(`t${trial}g${gid++}`, randomFraction(rand), chunk));
    } else {
      nodes.push(chunk[0]);
    }
  }
  if (nodes.length > 1 && rand() < 0.5) {
    return layerGroup(`t${trial}root`, randomFraction(rand), nodes);
  }
  return nodes.length === 1 ? nodes[0] : layerGroup(`t${trial}root`, Rational.ONE, nodes);
}

test("compositeLayers: unit-grid fraction compositing matches fragments", () => {
  const rand = rng(4242);
  for (let trial = 0; trial < 200; trial++) {
    const scene = randomScene(rand, trial);
    const layers = flattenScene(scene);
    const frags = compositeLayers(layers);

    // canonical sort
    for (let k = 1; k < frags.length; k++) {
      ok(compareRect(frags[k - 1].rect, frags[k].rect) < 0, "fragments canonically sorted");
    }

    // per-pixel ownership: at most one fragment covers any pixel
    const hit: (Fragment | null)[][] = [];
    for (let y = 0; y < H; y++) hit.push(new Array<Fragment | null>(W).fill(null));
    for (const f of frags) {
      ok(rectArea(f.rect) > 0, "no empty fragments");
      for (let y = f.rect.y0; y < f.rect.y1; y++) {
        for (let x = f.rect.x0; x < f.rect.x1; x++) {
          ok(hit[y][x] === null, `fragments disjoint at (${x},${y}) trial ${trial}`);
          hit[y][x] = f;
        }
      }
    }

    // per-pixel exact reference compositing
    let coveredPixels = 0;
    for (let y = 0; y < H; y++) {
      for (let x = 0; x < W; x++) {
        const covering = layers.filter((l) => containsPoint(l.rect, x, y));
        const f = hit[y][x];
        if (covering.every((l) => l.alpha.isZero()) || covering.length === 0) {
          ok(f === null, `no fragment over empty pixel (${x},${y}) trial ${trial}`);
          continue;
        }
        coveredPixels++;
        ok(f !== null, `fragment covers pixel (${x},${y}) trial ${trial}`);
        const ref = compositeExact(covering);
        ok(
          f!.alpha.equals(ref.alpha) && colorEquals(f!.color, ref.color),
          `color match at (${x},${y}) trial ${trial}: got a=${f!.alpha} c=${f!.color.r},${f!.color.g},${f!.color.b} want a=${ref.alpha} c=${ref.color.r},${ref.color.g},${ref.color.b}`,
        );
        // pruning correctness: recompositing only the kept sources is identical
        const byId = new Map(layers.map((l) => [l.id, l]));
        const kept = f!.sources.map((id) => byId.get(id)!);
        const re = compositeExact(kept);
        ok(
          re.alpha.equals(ref.alpha) && colorEquals(re.color, ref.color),
          `pruned sources reproduce color at (${x},${y}) trial ${trial}`,
        );
      }
    }

    // area conservation: fragment areas tile exactly the covered pixels
    const totalArea = frags.reduce((s, f) => s + rectArea(f.rect), 0);
    eq(totalArea, coveredPixels, `area conservation trial ${trial}`);
  }
});

test("compositeLayers: opaque top prunes, translucent does not", () => {
  const red = rgb(Rational.ONE, Rational.ZERO, Rational.ZERO);
  const green = rgb(Rational.ZERO, Rational.ONE, Rational.ZERO);
  const blue = rgb(Rational.ZERO, Rational.ZERO, Rational.ONE);
  const layers: FlatLayer[] = [
    { id: "red", rect: rect(0, 0, 4, 4), color: red, alpha: Rational.ONE },
    { id: "green", rect: rect(1, 1, 3, 3), color: green, alpha: Rational.ONE },
    { id: "blue", rect: rect(2, 0, 4, 2), color: blue, alpha: Rational.of(1, 2) },
  ];
  const frags = compositeLayers(layers);
  const at = (x: number, y: number): Fragment =>
    frags.find((f) => containsPoint(f.rect, x, y))!;
  // (0,0): only red, pruned to just red
  eq(at(0, 0).sources.join(","), "red", "uncovered red");
  // (1,1): green fully occludes red -> red pruned
  eq(at(1, 1).sources.join(","), "green", "opaque green prunes red");
  // (3,1): half-alpha blue over red -> red NOT pruned, color = 1/2 blue + 1/2 red
  eq(at(3, 1).sources.join(","), "red,blue", "translucent blue keeps red");
  const mixed = at(3, 1);
  ok(mixed.alpha.isOne(), "half blue over opaque red is opaque");
  ok(mixed.color.r.equals(Rational.of(1, 2)), "r = 1/2");
  ok(mixed.color.b.equals(Rational.of(1, 2)), "b = 1/2");
  ok(mixed.color.g.isZero(), "g = 0");
});

test("compositeLayers: group opacity multiplies into effective alpha", () => {
  const white = rgb(Rational.ONE, Rational.ONE, Rational.ONE);
  const black = rgb(Rational.ZERO, Rational.ZERO, Rational.ZERO);
  const scene = layerGroup("root", Rational.ONE, [
    rectLayer("bg", rect(0, 0, 2, 2), black, Rational.ONE),
    layerGroup("g", Rational.of(1, 2), [
      rectLayer("fg", rect(0, 0, 2, 2), white, Rational.of(1, 2)),
    ]),
  ]);
  const frags = compositeLayers(flattenScene(scene));
  eq(frags.length, 1, "single uniform fragment");
  // effective alpha 1/4 white over opaque black: gray 1/4, fully covered
  ok(frags[0].alpha.isOne(), "opaque bg makes stack fully opaque");
  ok(frags[0].color.r.equals(Rational.of(1, 4)), "1/4 white over black");
  eq(frags[0].sources.join(","), "bg,fg", "translucent stack keeps both");
  // without the opaque bg the group alpha shows through directly
  const lone = compositeLayers(
    flattenScene(
      layerGroup("g", Rational.of(1, 2), [
        rectLayer("fg", rect(0, 0, 2, 2), white, Rational.of(1, 2)),
      ]),
    ),
  );
  ok(lone[0].alpha.equals(Rational.of(1, 4)), "effective alpha = 1/2 * 1/2");
  ok(lone[0].color.r.isOne(), "color stays white over transparent bg");
});
