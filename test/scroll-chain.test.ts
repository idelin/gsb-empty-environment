import { test } from "node:test";
import assert from "node:assert/strict";
import {
  applyScrollDelta,
  applyScale,
  applyScaleThenScroll,
  syncBounds,
  type ScrollChainResult,
  type ScrollLayer,
  type ScalableScrollLayer,
} from "../src/scroll-chain.ts";

const EPS = 1e-9;

function layer(
  position: number,
  min: number,
  max: number,
  blocksChain = false,
): ScrollLayer {
  return { position, min, max, blocksChain };
}

function assertConservation(result: ScrollChainResult, delta: number): void {
  for (const entry of result.layers) {
    assert.ok(
      Math.abs(entry.input - (entry.consumed + entry.remainder)) < EPS,
      `layer conservation violated: ${JSON.stringify(entry)}`,
    );
  }
  const accounted =
    result.totalConsumed + result.overflow + result.discarded;
  assert.ok(
    Math.abs(accounted - delta) < EPS,
    `chain conservation violated: ${accounted} !== ${delta}`,
  );
}

test("positive delta fully consumed by inner layer, nothing propagates", () => {
  const inner = layer(10, 0, 100);
  const outer = layer(0, 0, 500);
  const result = applyScrollDelta([inner, outer], 30);
  assert.equal(inner.position, 40);
  assert.equal(outer.position, 0);
  assert.equal(result.layers.length, 2);
  assert.deepEqual(result.layers[0], {
    input: 30,
    consumed: 30,
    remainder: 0,
    blocked: false,
  });
  assertConservation(result, 30);
});

test("positive delta overflow propagates to ancestors", () => {
  const inner = layer(90, 0, 100);
  const mid = layer(0, 0, 50);
  const outer = layer(0, 0, 1000);
  const result = applyScrollDelta([inner, mid, outer], 25);
  assert.equal(inner.position, 100);
  assert.equal(mid.position, 15);
  assert.equal(outer.position, 0);
  assert.equal(result.layers[0].consumed, 10);
  assert.equal(result.layers[0].remainder, 15);
  assert.equal(result.layers[1].consumed, 15);
  assertConservation(result, 25);
});

test("negative delta consumes downward and propagates remainder", () => {
  const inner = layer(5, 0, 100);
  const outer = layer(20, 0, 100);
  const result = applyScrollDelta([inner, outer], -30);
  assert.equal(inner.position, 0);
  assert.equal(outer.position, 0);
  assert.equal(result.layers[0].consumed, -5);
  assert.equal(result.layers[0].remainder, -25);
  assert.equal(result.layers[1].consumed, -20);
  assert.equal(result.overflow, -5);
  assertConservation(result, -30);
});

test("unconsumed remainder exits the chain as overflow", () => {
  const inner = layer(90, 0, 100);
  const outer = layer(0, 0, 40);
  const result = applyScrollDelta([inner, outer], 100);
  assert.equal(result.overflow, 50);
  assert.equal(result.discarded, 0);
  assert.equal(result.totalConsumed, 50);
  assertConservation(result, 100);
});

test("blocksChain swallows remainder instead of propagating", () => {
  const inner = layer(90, 0, 100, true);
  const outer = layer(0, 0, 500);
  const result = applyScrollDelta([inner, outer], 40);
  assert.equal(inner.position, 100);
  assert.equal(outer.position, 0);
  assert.equal(result.layers.length, 1);
  assert.equal(result.layers[0].blocked, true);
  assert.equal(result.discarded, 30);
  assert.equal(result.overflow, 0);
  assertConservation(result, 40);
});

test("zero-range layer consumes nothing but still blocks when blocksChain", () => {
  const zero = layer(0, 0, 0, true);
  const outer = layer(10, 0, 100);
  const result = applyScrollDelta([zero, outer], 50);
  assert.equal(zero.position, 0);
  assert.equal(outer.position, 10);
  assert.equal(result.layers[0].consumed, 0);
  assert.equal(result.layers[0].remainder, 50);
  assert.equal(result.discarded, 50);
  assertConservation(result, 50);
});

test("zero-range layer passes remainder through when not blocking", () => {
  const zero = layer(0, 0, 0);
  const outer = layer(10, 0, 100);
  const result = applyScrollDelta([zero, outer], 50);
  assert.equal(zero.position, 0);
  assert.equal(outer.position, 60);
  assert.equal(result.layers[0].consumed, 0);
  assert.equal(result.layers[1].input, 50);
  assertConservation(result, 50);
});

test("fractional pixels conserve exactly at every layer", () => {
  const inner = layer(50, 10.5, 60.25);
  const outer = layer(0.5, 0, 20.75);
  const result = applyScrollDelta([inner, outer], 37.75);
  assert.equal(inner.position, 60.25);
  assert.equal(result.layers[0].consumed, 10.25);
  assert.equal(result.layers[0].remainder, 27.5);
  assert.equal(outer.position, 20.75);
  assert.equal(result.layers[1].consumed, 20.25);
  assert.equal(result.overflow, 7.25);
  assertConservation(result, 37.75);
});

test("fractional negative delta with non-dyadic values", () => {
  const inner = layer(3.3, 0.1, 9.9);
  const outer = layer(7.7, 0, 50);
  const delta = -12.34;
  const result = applyScrollDelta([inner, outer], delta);
  assert.ok(Math.abs(inner.position - 0.1) < EPS);
  assert.ok(Math.abs(outer.position - 0) < EPS);
  assert.ok(Math.abs(result.overflow - (7.7 - (12.34 - 3.2))) < EPS);
  assertConservation(result, delta);
});

test("dynamic bounds: shrinking max clamps position on next scroll", () => {
  const inner = layer(80, 0, 100);
  const outer = layer(0, 0, 500);
  inner.max = 40;
  const result = applyScrollDelta([inner, outer], 0);
  assert.equal(inner.position, 40);
  assert.equal(result.layers[0].consumed, -40);
  assert.equal(result.layers[0].remainder, 40);
  assert.equal(outer.position, 40);
  assertConservation(result, 0);
});

test("dynamic bounds: growing max allows further consumption", () => {
  const inner = layer(50, 0, 50);
  const outer = layer(0, 0, 500);
  const first = applyScrollDelta([inner, outer], 30);
  assert.equal(inner.position, 50);
  assert.equal(outer.position, 30);
  assertConservation(first, 30);
  inner.max = 120;
  const second = applyScrollDelta([inner, outer], 100);
  assert.equal(inner.position, 120);
  assert.equal(outer.position, 60);
  assertConservation(second, 100);
});

function scalable(
  position: number,
  scale: number,
  viewportSize: number,
  contentSize: number,
  blocksChain = false,
): ScalableScrollLayer {
  const l: ScalableScrollLayer = {
    position,
    min: 0,
    max: 0,
    blocksChain,
    scale,
    viewportSize,
    contentSize,
  };
  syncBounds(l);
  return l;
}

test("scale change preserves the anchor content point", () => {
  const l = scalable(100, 1, 200, 1000);
  applyScale(l, 2, 50);
  assert.equal(l.scale, 2);
  assert.equal(l.max, 1800);
  assert.equal(l.position, 250);
  assert.equal((l.position + 50) / l.scale, 150);
});

test("scale down clamps position when anchor cannot be preserved", () => {
  const l = scalable(700, 1, 200, 1000);
  applyScale(l, 0.5, 50);
  assert.equal(l.max, 300);
  assert.equal(l.position, 300);
});

test("scale-then-scroll: anchor preserved first, displacement after", () => {
  const inner = scalable(100, 1, 200, 1000);
  const outer = scalable(0, 1, 400, 2000);
  const result = applyScaleThenScroll([inner, outer], 2, 50, 2000);
  assert.equal(inner.position, 1800);
  assert.equal(outer.position, 500);
  assertConservation(result, 2000);
});

test("scroll-then-scale would give a different result than scale-then-scroll", () => {
  const a = scalable(100, 1, 200, 1000);
  applyScale(a, 2, 50);
  applyScrollDelta([a], 500);
  const b = scalable(100, 1, 200, 1000);
  applyScrollDelta([b], 500);
  applyScale(b, 2, 50);
  assert.notEqual(a.position, b.position);
});

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("randomized conservation across signs, fractions, and dynamic bounds", () => {
  const rand = mulberry32(42);
  for (let iter = 0; iter < 500; iter++) {
    const count = 1 + Math.floor(rand() * 5);
    const layers: ScrollLayer[] = [];
    for (let i = 0; i < count; i++) {
      const min = Math.floor(rand() * 200) - 100;
      const span = rand() < 0.2 ? 0 : rand() * 300;
      const max = min + span;
      const position = min + rand() * span;
      layers.push({
        position,
        min,
        max,
        blocksChain: rand() < 0.3,
      });
    }
    const delta = (rand() - 0.5) * 1000;
    const result = applyScrollDelta(layers, delta);
    assertConservation(result, delta);
    for (const l of layers) {
      assert.ok(l.position >= l.min - EPS && l.position <= l.max + EPS);
    }
    if (result.layers.some((e) => e.blocked)) {
      const blockedIndex = result.layers.findIndex((e) => e.blocked);
      assert.equal(result.layers.length, blockedIndex + 1);
      assert.equal(result.overflow, 0);
    } else {
      assert.equal(result.discarded, 0);
    }
  }
});
