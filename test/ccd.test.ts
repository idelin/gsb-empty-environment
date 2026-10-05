import { test } from "node:test";
import assert from "node:assert/strict";
import { Rat } from "../src/rational.ts";
import type { Body } from "../src/geometry.ts";
import { World, allPairsExact, contactInterval, pairKey } from "../src/ccd.ts";

function body(
  id: string,
  x: string,
  y: string,
  w: string,
  h: string,
  vx = "0",
  vy = "0",
): Body {
  return {
    id,
    rect: { x: Rat.of(x), y: Rat.of(y), w: Rat.of(w), h: Rat.of(h) },
    vx: Rat.of(vx),
    vy: Rat.of(vy),
  };
}

function worldWith(T: string, cellSize: string, bodies: Body[]): World {
  const world = new World({ T, cellSize });
  world.applyBatch({
    add: bodies.map((b) => ({
      id: b.id,
      rect: {
        x: b.rect.x.toString(),
        y: b.rect.y.toString(),
        w: b.rect.w.toString(),
        h: b.rect.h.toString(),
      },
      v: [b.vx.toString(), b.vy.toString()] as const,
    })),
  });
  return world;
}

test("Rat: parsing and normalization", () => {
  assert.equal(Rat.of("3/6").toString(), "1/2");
  assert.equal(Rat.of("-2/-4").toString(), "1/2");
  assert.equal(Rat.of("4/-8").toString(), "-1/2");
  assert.equal(Rat.of("1.5").toString(), "3/2");
  assert.equal(Rat.of(0.1).toString(), "1/10");
  assert.equal(Rat.of("1e-3").toString(), "1/1000");
  assert.equal(Rat.of("-0.25").toString(), "-1/4");
  assert.equal(Rat.of("0").toString(), "0");
});

test("Rat: arithmetic and floorDiv with negatives", () => {
  assert.equal(Rat.of("1/2").add(Rat.of("1/3")).toString(), "5/6");
  assert.equal(Rat.of("1/2").sub(Rat.of("2/3")).toString(), "-1/6");
  assert.equal(Rat.of("-2/3").mul(Rat.of("3/4")).toString(), "-1/2");
  assert.equal(Rat.of("1/2").div(Rat.of("-3/4")).toString(), "-2/3");
  assert.equal(Rat.of("-1/2").floorDiv(Rat.ONE), -1n);
  assert.equal(Rat.of("1/2").floorDiv(Rat.ONE), 0n);
  assert.equal(Rat.of("-3").floorDiv(Rat.of(2)), -2n);
  assert.equal(Rat.of("7/2").floorDiv(Rat.of("3/2")), 2n);
  assert.equal(Rat.of("-7/2").floorDiv(Rat.of("3/2")), -3n);
});

test("identical motion: overlapping pair stays colliding for [0, T]", () => {
  const world = worldWith("10", "1", [
    body("a", "0", "0", "2", "2", "1", "1"),
    body("b", "1", "1", "2", "2", "1", "1"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "0");
  assert.equal(collisions[0].t1.toString(), "10");
  assert.ok(stats.verified);
});

test("identical motion: separated pair with same velocity never collides", () => {
  const world = worldWith("10", "1", [
    body("a", "0", "0", "1", "1", "1", "1"),
    body("b", "3", "0", "1", "1", "1", "1"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(collisions.length, 0);
  assert.ok(stats.verified);
});

test("zero relative velocity: static overlap gives [0, T], static gap gives nothing", () => {
  const a = body("a", "0", "0", "2", "2");
  const b = body("b", "1", "1", "2", "2");
  const c = body("c", "5", "5", "1", "1");
  assert.deepEqual(
    contactInterval(a, b, Rat.of(7))?.map(String),
    ["0", "7"],
  );
  assert.equal(contactInterval(a, c, Rat.of(7)), null);
});

test("initial overlap counts from t = 0", () => {
  const world = worldWith("4", "1", [
    body("a", "0", "0", "2", "2"),
    body("b", "1", "0", "2", "2", "1", "0"),
  ]);
  const { collisions } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "0");
  // b x-range [1+ t, 3+t] overlaps a x-range [0,2] while 1+t <= 2 => t <= 1
  assert.equal(collisions[0].t1.toString(), "1");
});

test("contact exactly at t = T is reported", () => {
  const world = worldWith("1", "1", [
    body("a", "0", "0", "1", "1"),
    body("b", "2", "0", "1", "1", "-1", "0"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "1");
  assert.equal(collisions[0].t1.toString(), "1");
  assert.ok(stats.verified);
});

test("corner touch at a single instant counts as collision", () => {
  // At t=3, b occupies [-1,0]x[-1,0]: touches a=[0,1]^2 at corner (0,0).
  const world = worldWith("5", "1", [
    body("a", "0", "0", "1", "1"),
    body("b", "2", "-4", "1", "1", "-1", "1"),
  ]);
  const { collisions } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "3");
  assert.equal(collisions[0].t1.toString(), "3");
});

test("extremely narrow contact interval is exact, not sampled away", () => {
  // Contact window is [3 - 1/10^6, 3]: width 1/1000000.
  const world = worldWith("5", "1", [
    body("a", "0", "0", "1", "1"),
    body("b", "2", "-4", "1", "1000001/1000000", "-1", "1"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "2999999/1000000");
  assert.equal(collisions[0].t1.toString(), "3");
  assert.ok(stats.verified);
});

test("tunneling: fast bullet through thin wall between frames", () => {
  const world = worldWith("1/10", "1", [
    body("wall", "4", "0", "1", "10"),
    body("bullet", "0", "4", "1", "1", "100", "0"),
  ]);
  const { collisions } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "3/100");
  assert.equal(collisions[0].t1.toString(), "1/20");
});

test("negative coordinates crossing many cells", () => {
  const world = worldWith("1", "1", [
    body("a", "-5", "-5", "1", "1"),
    body("b", "5", "-5", "1", "1", "-10", "0"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(collisions.length, 1);
  assert.equal(collisions[0].t0.toString(), "9/10");
  assert.equal(collisions[0].t1.toString(), "1");
  assert.ok(stats.verified);
});

test("grid boundary: pair in many shared cells is returned exactly once", () => {
  // Both boxes sit exactly on integer cell boundaries with cellSize=1,
  // so they share cells (1..2)x(1..2): the pair is generated 4 times.
  const world = worldWith("2", "1", [
    body("a", "0", "0", "2", "2"),
    body("b", "1", "1", "2", "2"),
  ]);
  const { collisions, stats } = world.query();
  assert.equal(stats.cellPairs, 4);
  assert.equal(stats.candidates, 1);
  assert.equal(collisions.length, 1);
  assert.deepEqual(collisions[0].pair, ["a", "b"]);
  assert.equal(collisions[0].t0.toString(), "0");
  assert.equal(collisions[0].t1.toString(), "2");
});

test("batch updates: removals never resurrect from stale cells", () => {
  const world = new World({ T: "1", cellSize: "1" });
  world.applyBatch({
    add: [
      { id: "a", rect: { x: "0", y: "0", w: "1", h: "1" } },
      { id: "b", rect: { x: "2", y: "0", w: "1", h: "1" }, v: ["-1", "0"] },
    ],
  });
  assert.equal(world.query().collisions.length, 1);

  world.applyBatch({ remove: ["b"] });
  const after = world.query();
  assert.equal(after.collisions.length, 0);
  assert.equal(after.stats.candidates, 0);
  assert.ok(after.stats.verified);

  // Explicit re-add of the same id is fine and collides again.
  world.applyBatch({
    add: [{ id: "b", rect: { x: "2", y: "0", w: "1", h: "1" }, v: ["-1", "0"] }],
  });
  assert.equal(world.query().collisions.length, 1);

  // Velocity update takes effect in the next batch only.
  world.applyBatch({ update: [{ id: "b", v: ["1", "0"] }] });
  assert.equal(world.query().collisions.length, 0);

  // Duplicate live id is rejected.
  assert.throws(() => world.applyBatch({ add: [{ id: "a", rect: { x: "9", y: "9", w: "1", h: "1" } }] }));
});

test("stats report candidate and exact pair counts", () => {
  const world = worldWith("3", "1", [
    body("a", "0", "0", "1", "1"),
    body("b", "0.5", "0", "1", "1"),
    body("c", "100", "100", "1", "1"),
  ]);
  const { stats, collisions } = world.query();
  assert.equal(stats.bodies, 3);
  assert.equal(stats.exactPairs, 3); // 3*2/2, independent all-pairs pass
  assert.equal(stats.candidates, 1); // only (a,b) survives broad phase
  assert.equal(stats.collisions, 1);
  assert.equal(collisions.length, 1);
  assert.ok(stats.verified);
  assert.deepEqual(stats.missed, []);
});

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("randomized: broad phase never misses vs independent all-pairs exact pass", () => {
  const rand = mulberry32(20261005);
  const rat = (lo: number, hi: number, den: number): string => {
    const n = Math.floor(lo * den + rand() * (hi - lo) * den);
    return `${n}/${den}`;
  };
  for (let iter = 0; iter < 50; iter++) {
    const n = 2 + Math.floor(rand() * 12);
    const bodies: Body[] = [];
    for (let i = 0; i < n; i++) {
      bodies.push(
        body(
          `b${i}`,
          rat(-8, 8, 4),
          rat(-8, 8, 4),
          rat(0, 3, 2),
          rat(0, 3, 2),
          rat(-6, 6, 2),
          rat(-6, 6, 2),
        ),
      );
    }
    const world = worldWith("7", ["1", "3/2", "2"][iter % 3], bodies);
    const { collisions, stats } = world.query();

    assert.ok(stats.verified, `iteration ${iter}: missed ${stats.missed.join("; ")}`);
    assert.equal(stats.exactPairs, (n * (n - 1)) / 2);
    assert.ok(stats.candidates >= collisions.length);

    // Cross-check intervals against the independent ground truth directly.
    const truth = allPairsExact(bodies, Rat.of(7));
    assert.equal(collisions.length, truth.size);
    const seen = new Set<string>();
    for (const c of collisions) {
      const key = pairKey(c.pair[0], c.pair[1]);
      assert.ok(!seen.has(key), "pair returned twice");
      seen.add(key);
      const expected = truth.get(key);
      assert.ok(expected !== undefined, `unexpected collision ${key}`);
      assert.ok(c.t0.eq(expected.t0) && c.t1.eq(expected.t1), `interval mismatch ${key}`);
    }
  }
});
